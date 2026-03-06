package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newUpgradeCommand(_ context.Context, appVersion string) *cobra.Command {
	targetVersion := "latest"
	allowDowngrade := false

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade dnsvard in place",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return usageError("upgrade does not accept positional arguments\nusage: dnsvard upgrade [--version <latest|vX.Y.Z>] [--allow-downgrade]")
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUpgrade(appVersion, targetVersion, allowDowngrade)
		},
	}
	cmd.Flags().StringVar(&targetVersion, "version", "latest", "Version to install (default: latest)")
	cmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, "Allow downgrading when --version latest resolves older than current")
	return cmd
}

var upgradeVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

var (
	defaultInstallerURL = "https://dnsvard.com/install"

	defaultInstallerHosts = map[string]struct{}{
		"dnsvard.com":           {},
		"www.dnsvard.com":       {},
		"downloads.dnsvard.com": {},
	}
	defaultReleaseHosts = map[string]struct{}{
		"downloads.dnsvard.com": {},
	}
)

func runUpgrade(currentVersion string, targetVersion string, allowDowngrade bool) error {
	if _, err := exec.LookPath("curl"); err != nil {
		return fmt.Errorf("upgrade requires curl in PATH")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if isBrewManagedInstall(exePath) {
		return fmt.Errorf("`dnsvard upgrade` is disabled for Homebrew-managed installs\nfix: run `brew upgrade --cask comment-slayer/tap/dnsvard`")
	}
	installDir := filepath.Dir(exePath)

	tmpDir, err := os.MkdirTemp("", "dnsvard-upgrade-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		_ = os.Remove(filepath.Join(tmpDir, "install.sh"))
		_ = os.Remove(filepath.Join(tmpDir, "LATEST"))
		_ = os.Remove(filepath.Join(tmpDir, "checksums.txt"))
		_ = os.Remove(filepath.Join(tmpDir, "archive.tar.gz"))
		_ = os.Remove(filepath.Join(tmpDir, "dnsvard"))
		_ = os.Remove(tmpDir)
	}()

	version := strings.TrimSpace(targetVersion)
	if version == "" {
		return fmt.Errorf("invalid --version value %q\nallowed values: latest, vX.Y.Z (example: v0.1.0)", targetVersion)
	}
	if version != "latest" && !upgradeVersionPattern.MatchString(version) {
		return fmt.Errorf("invalid --version value %q\nallowed values: latest, vX.Y.Z (example: v0.1.0)", version)
	}

	installer := defaultInstallerURL
	u, err := url.Parse(installer)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || strings.TrimSpace(u.Host) == "" || !strings.HasSuffix(strings.TrimSpace(u.Path), "/install") {
		return fmt.Errorf("invalid installer URL %q\nexpected an HTTPS URL ending with /install", installer)
	}
	if err := enforceHostAllowlistPolicy("installer URL", installer, defaultInstallerHosts); err != nil {
		return err
	}

	installerPath := filepath.Join(tmpDir, "install.sh")
	if err := fetchURLToFile(installer, installerPath); err != nil {
		return fmt.Errorf("download installer: %w", err)
	}
	releaseBaseURL, err := installerReleaseBaseURL(installerPath)
	if err != nil {
		return err
	}
	if err := enforceHostAllowlistPolicy("release BASE_URL", releaseBaseURL, defaultReleaseHosts); err != nil {
		return err
	}

	resolvedVersion := version
	if resolvedVersion == "latest" {
		latestPath := filepath.Join(tmpDir, "LATEST")
		if err := fetchURLToFile(releaseBaseURL+"/LATEST", latestPath); err != nil {
			return fmt.Errorf("resolve latest version: %w", err)
		}
		latestBytes, err := os.ReadFile(latestPath)
		if err != nil {
			return fmt.Errorf("read latest version marker: %w", err)
		}
		resolvedVersion = strings.TrimSpace(string(latestBytes))
		if !upgradeVersionPattern.MatchString(resolvedVersion) {
			return fmt.Errorf("resolved latest version %q is invalid", resolvedVersion)
		}
		if err := enforceLatestDowngradePolicy(currentVersion, resolvedVersion, allowDowngrade); err != nil {
			return err
		}
	}
	if sameResolvedVersion(currentVersion, resolvedVersion) {
		fmt.Printf("dnsvard already at latest version: %s\n", resolvedVersion)
		return nil
	}
	archiveName, err := releaseArchiveName(resolvedVersion)
	if err != nil {
		return err
	}

	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	archivePath := filepath.Join(tmpDir, "archive.tar.gz")

	if err := fetchURLToFile(releaseBaseURL+"/"+resolvedVersion+"/checksums.txt", checksumsPath); err != nil {
		return fmt.Errorf("download checksums manifest: %w", err)
	}
	if err := fetchURLToFile(releaseBaseURL+"/"+resolvedVersion+"/"+archiveName, archivePath); err != nil {
		return fmt.Errorf("download release archive: %w", err)
	}
	expectedChecksum, err := expectedChecksumForArtifact(checksumsPath, archiveName)
	if err != nil {
		return err
	}
	if err := verifyArchiveChecksum(archivePath, expectedChecksum); err != nil {
		return err
	}

	extractedBinary := filepath.Join(tmpDir, "dnsvard")
	if err := extractBinaryFromArchive(archivePath, extractedBinary); err != nil {
		return err
	}
	if err := installBinary(extractedBinary, exePath); err != nil {
		return err
	}

	fmt.Printf("upgrading dnsvard (%s -> %s) into %s\n", strings.TrimSpace(currentVersion), resolvedVersion, installDir)
	fmt.Printf("upgrade complete\n")
	return nil
}

func fetchURLToFile(fetchURL string, destinationPath string) error {
	cmd := exec.Command("curl", "--fail", "--silent", "--show-error", "--location", "--proto", "=https", "--tlsv1.2", fetchURL, "-o", destinationPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			return fmt.Errorf("%w\n%s", err, trimmed)
		}
		return err
	}
	return nil
}

func installerReleaseBaseURL(installerPath string) (string, error) {
	b, err := os.ReadFile(installerPath)
	if err != nil {
		return "", fmt.Errorf("read installer script: %w", err)
	}
	return extractInstallerBaseURL(string(b))
}

func extractInstallerBaseURL(script string) (string, error) {
	re := regexp.MustCompile(`(?m)^BASE_URL="([^"]+)"\s*$`)
	m := re.FindStringSubmatch(script)
	if len(m) != 2 {
		return "", errors.New("installer script missing BASE_URL declaration")
	}
	base := strings.TrimSpace(m[1])
	u, err := url.Parse(base)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("installer BASE_URL %q is invalid", base)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func enforceHostAllowlistPolicy(sourceLabel string, rawURL string, allowed map[string]struct{}) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", sourceLabel, rawURL, err)
	}
	if u.User != nil {
		return fmt.Errorf("invalid %s %q: userinfo is not allowed", sourceLabel, rawURL)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("invalid %s %q: host is required", sourceLabel, rawURL)
	}
	port := strings.TrimSpace(u.Port())
	_, trustedHost := allowed[host]
	trustedPort := port == "" || port == "443"
	if trustedHost && trustedPort {
		return nil
	}
	hosts := make([]string, 0, len(allowed))
	for h := range allowed {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return fmt.Errorf("%s %q is outside dnsvard default source policy\nallowed hosts: %s\nfix: use official dnsvard sources", sourceLabel, rawURL, strings.Join(hosts, ", "))
}

func enforceLatestDowngradePolicy(currentVersion string, resolvedVersion string, allowDowngrade bool) error {
	currentVersion = strings.TrimSpace(currentVersion)
	resolvedVersion = strings.TrimSpace(resolvedVersion)
	if !upgradeVersionPattern.MatchString(currentVersion) || !upgradeVersionPattern.MatchString(resolvedVersion) {
		return nil
	}
	cmp, err := compareSemver(currentVersion, resolvedVersion)
	if err != nil || cmp <= 0 {
		return err
	}
	if allowDowngrade {
		fmt.Fprintf(os.Stderr, "WARNING: resolved latest version %s is older than current version %s\n", resolvedVersion, currentVersion)
		fmt.Fprintf(os.Stderr, "WARNING: continuing because --allow-downgrade was set\n")
		return nil
	}
	return fmt.Errorf("resolved latest version %s is older than current version %s\nfix: rerun with --allow-downgrade to proceed intentionally", resolvedVersion, currentVersion)
}

type semver struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func compareSemver(a string, b string) (int, error) {
	av, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	if av.major != bv.major {
		if av.major < bv.major {
			return -1, nil
		}
		return 1, nil
	}
	if av.minor != bv.minor {
		if av.minor < bv.minor {
			return -1, nil
		}
		return 1, nil
	}
	if av.patch != bv.patch {
		if av.patch < bv.patch {
			return -1, nil
		}
		return 1, nil
	}
	return comparePrerelease(av.prerelease, bv.prerelease), nil
}

func parseSemver(v string) (semver, error) {
	v = strings.TrimSpace(v)
	if !upgradeVersionPattern.MatchString(v) {
		return semver{}, fmt.Errorf("invalid version %q", v)
	}
	v = strings.TrimPrefix(v, "v")
	if idx := strings.Index(v, "+"); idx >= 0 {
		v = v[:idx]
	}
	parsed := semver{}
	if idx := strings.Index(v, "-"); idx >= 0 {
		parsed.prerelease = v[idx+1:]
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid version %q", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid version %q", v)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, fmt.Errorf("invalid version %q", v)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return semver{}, fmt.Errorf("invalid version %q", v)
	}
	parsed.major = major
	parsed.minor = minor
	parsed.patch = patch
	return parsed, nil
}

func comparePrerelease(a string, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		ai, aNum := parseNumericIdentifier(aParts[i])
		bi, bNum := parseNumericIdentifier(bParts[i])
		switch {
		case aNum && bNum:
			if ai < bi {
				return -1
			}
			if ai > bi {
				return 1
			}
		case aNum && !bNum:
			return -1
		case !aNum && bNum:
			return 1
		default:
			if aParts[i] < bParts[i] {
				return -1
			}
			if aParts[i] > bParts[i] {
				return 1
			}
		}
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	if len(aParts) > len(bParts) {
		return 1
	}
	return 0
}

func parseNumericIdentifier(v string) (int, bool) {
	if strings.TrimSpace(v) == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func sameResolvedVersion(currentVersion string, resolvedVersion string) bool {
	currentVersion = strings.TrimSpace(currentVersion)
	resolvedVersion = strings.TrimSpace(resolvedVersion)
	if currentVersion == "" || resolvedVersion == "" {
		return false
	}
	if currentVersion == resolvedVersion {
		return true
	}
	if !upgradeVersionPattern.MatchString(currentVersion) || !upgradeVersionPattern.MatchString(resolvedVersion) {
		return false
	}
	cmp, err := compareSemver(currentVersion, resolvedVersion)
	return err == nil && cmp == 0
}

func releaseArchiveName(version string) (string, error) {
	goos := runtime.GOOS
	if goos != "darwin" && goos != "linux" {
		return "", fmt.Errorf("unsupported runtime OS %q for self-upgrade", goos)
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported runtime architecture %q for self-upgrade", arch)
	}
	artifactVersion := strings.TrimSpace(version)
	if artifactVersion == "" {
		return "", fmt.Errorf("invalid version %q for archive lookup", version)
	}
	return fmt.Sprintf("dnsvard_%s_%s_%s.tar.gz", artifactVersion, goos, arch), nil
}

func expectedChecksumForArtifact(checksumsPath string, archiveName string) (string, error) {
	b, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", fmt.Errorf("read checksums manifest: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		candidate := strings.TrimPrefix(strings.TrimSpace(fields[1]), "*")
		if candidate == archiveName {
			sum := strings.ToLower(strings.TrimSpace(fields[0]))
			if len(sum) != 64 {
				return "", fmt.Errorf("checksums manifest entry for %s has invalid sha256 digest", archiveName)
			}
			if _, err := hex.DecodeString(sum); err != nil {
				return "", fmt.Errorf("checksums manifest entry for %s has non-hex digest", archiveName)
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("checksums manifest missing entry for %s", archiveName)
}

func verifyArchiveChecksum(archivePath string, expectedHex string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive for checksum verification: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash archive for checksum verification: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expectedHex)) {
		return fmt.Errorf("archive checksum mismatch\nfix: retry upgrade; if mismatch persists, do not install this artifact")
	}
	return nil
}

func extractBinaryFromArchive(archivePath string, destinationBinaryPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open release archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read release archive gzip header: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read release archive: %w", err)
		}
		if h == nil || h.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(h.Name) != "dnsvard" {
			continue
		}
		out, err := os.OpenFile(destinationBinaryPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return fmt.Errorf("create extracted binary: %w", err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return fmt.Errorf("write extracted binary: %w", err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close extracted binary: %w", err)
		}
		return nil
	}
	return errors.New("release archive does not contain dnsvard binary")
}

func installBinary(extractedBinaryPath string, destinationBinaryPath string) error {
	data, err := os.ReadFile(extractedBinaryPath)
	if err != nil {
		return fmt.Errorf("read extracted binary: %w", err)
	}
	tmpPath := destinationBinaryPath + ".new"
	if err := os.WriteFile(tmpPath, data, 0o755); err != nil {
		return fmt.Errorf("write replacement binary: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := os.Rename(tmpPath, destinationBinaryPath); err != nil {
		return fmt.Errorf("replace installed binary: %w", err)
	}
	return nil
}

func isBrewManagedInstall(exePath string) bool {
	paths := []string{exePath}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil && strings.TrimSpace(resolved) != "" {
		paths = append(paths, resolved)
	}
	for _, p := range paths {
		normalized := filepath.ToSlash(p)
		if strings.Contains(normalized, "/Cellar/") || strings.Contains(normalized, "/Caskroom/") || strings.Contains(normalized, "/Homebrew/") || strings.Contains(normalized, "/.linuxbrew/") {
			return true
		}
	}
	for _, p := range paths {
		if !isLikelyBrewLinkedBinaryPath(p) {
			continue
		}
		if brewHasPackage("dnsvard", true) || brewHasPackage("dnsvard", false) {
			return true
		}
	}
	return false
}

func isLikelyBrewLinkedBinaryPath(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if normalized == "" {
		return false
	}
	return strings.HasSuffix(normalized, "/opt/homebrew/bin/dnsvard") || strings.HasSuffix(normalized, "/usr/local/bin/dnsvard") || strings.HasSuffix(normalized, "/home/linuxbrew/.linuxbrew/bin/dnsvard")
}

func brewHasPackage(name string, cask bool) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if _, err := exec.LookPath("brew"); err != nil {
		return false
	}
	args := []string{"list", "--versions"}
	if cask {
		args = append(args, "--cask")
	}
	args = append(args, name)
	out, err := exec.Command("brew", args...).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
