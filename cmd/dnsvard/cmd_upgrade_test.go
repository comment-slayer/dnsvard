package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractInstallerBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		script   string
		wantBase string
		wantErr  string
	}{
		{name: "valid base url", script: "BASE_URL=\"https://downloads.dnsvard.com\"\n", wantBase: "https://downloads.dnsvard.com"},
		{name: "missing declaration", script: "VERSION=latest\n", wantErr: "missing BASE_URL"},
		{name: "invalid scheme", script: "BASE_URL=\"http://downloads.dnsvard.com\"\n", wantErr: "invalid"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := extractInstallerBaseURL(tc.script)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("extractInstallerBaseURL returned error: %v", err)
				}
				if got != tc.wantBase {
					t.Fatalf("base = %q, want %q", got, tc.wantBase)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestEnforceHostAllowlistPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		label   string
		rawURL  string
		wantErr string
	}{
		{name: "trusted installer host", label: "installer URL", rawURL: "https://dnsvard.com/install"},
		{name: "trusted release host", label: "release BASE_URL", rawURL: "https://downloads.dnsvard.com"},
		{name: "trusted host explicit https port", label: "installer URL", rawURL: "https://dnsvard.com:443/install"},
		{name: "userinfo blocked", label: "installer URL", rawURL: "https://user@dnsvard.com/install", wantErr: "userinfo is not allowed"},
		{name: "missing host blocked", label: "installer URL", rawURL: "https:///install", wantErr: "host is required"},
		{name: "untrusted host blocked", label: "installer URL", rawURL: "https://mirror.example.com/install", wantErr: "outside dnsvard default source policy"},
		{name: "trusted host non-standard port blocked", label: "installer URL", rawURL: "https://dnsvard.com:8443/install", wantErr: "outside dnsvard default source policy"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := enforceHostAllowlistPolicy(tc.label, tc.rawURL, defaultInstallerHosts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("enforceHostAllowlistPolicy returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestEnforceLatestDowngradePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		current        string
		resolved       string
		allowDowngrade bool
		wantErr        string
	}{
		{name: "downgrade blocked by default", current: "v0.3.0", resolved: "v0.2.9", wantErr: "allow-downgrade"},
		{name: "downgrade allowed with flag", current: "v0.3.0", resolved: "v0.2.9", allowDowngrade: true},
		{name: "same version accepted", current: "v0.3.0", resolved: "v0.3.0"},
		{name: "upgrade accepted", current: "v0.3.0", resolved: "v0.3.1"},
		{name: "non-semver current ignored", current: "dev", resolved: "v0.3.1"},
		{name: "prerelease compare", current: "v0.3.0", resolved: "v0.3.0-rc.1", wantErr: "allow-downgrade"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := enforceLatestDowngradePolicy(tc.current, tc.resolved, tc.allowDowngrade)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("enforceLatestDowngradePolicy returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCompareSemver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a    string
		b    string
		want int
	}{
		{a: "v1.2.3", b: "v1.2.3", want: 0},
		{a: "v1.2.3", b: "v1.2.4", want: -1},
		{a: "v1.3.0", b: "v1.2.9", want: 1},
		{a: "v1.2.3-rc.1", b: "v1.2.3", want: -1},
		{a: "v1.2.3", b: "v1.2.3-rc.1", want: 1},
		{a: "v1.2.3-alpha.10", b: "v1.2.3-alpha.2", want: 1},
		{a: "v1.2.3+meta", b: "v1.2.3", want: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("%s__%s", tc.a, tc.b), func(t *testing.T) {
			t.Parallel()
			got, err := compareSemver(tc.a, tc.b)
			if err != nil {
				t.Fatalf("compareSemver returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestSameResolvedVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  string
		resolved string
		want     bool
	}{
		{name: "exact match", current: "v0.3.0", resolved: "v0.3.0", want: true},
		{name: "build metadata treated same", current: "v0.3.0+local", resolved: "v0.3.0", want: true},
		{name: "different patch", current: "v0.3.0", resolved: "v0.3.1", want: false},
		{name: "non semver must match exactly", current: "dev", resolved: "v0.3.0", want: false},
		{name: "blank current", current: "", resolved: "v0.3.0", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sameResolvedVersion(tc.current, tc.resolved)
			if got != tc.want {
				t.Fatalf("sameResolvedVersion(%q, %q) = %t, want %t", tc.current, tc.resolved, got, tc.want)
			}
		})
	}
}

func TestExpectedChecksumForArtifact(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	manifest := filepath.Join(d, "checksums.txt")
	if err := os.WriteFile(manifest, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  dnsvard_v0.1.0_darwin_arm64.tar.gz\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	sum, err := expectedChecksumForArtifact(manifest, "dnsvard_v0.1.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("expectedChecksumForArtifact returned error: %v", err)
	}
	if sum != strings.Repeat("a", 64) {
		t.Fatalf("sum = %q", sum)
	}
}

func TestReleaseArchiveNamePreservesVPrefix(t *testing.T) {
	t.Parallel()

	archive, err := releaseArchiveName("v0.1.2")
	if err != nil {
		t.Fatalf("releaseArchiveName returned error: %v", err)
	}
	if !strings.Contains(archive, "_v0.1.2_") {
		t.Fatalf("archive name should include v prefix: %q", archive)
	}
}

func TestIsBrewManagedInstall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "brew cellar path", path: "/opt/homebrew/Cellar/dnsvard/0.1.0/bin/dnsvard", want: true},
		{name: "brew caskroom path", path: "/opt/homebrew/Caskroom/dnsvard/0.1.0/dnsvard", want: true},
		{name: "linuxbrew path", path: "/home/linuxbrew/.linuxbrew/bin/dnsvard", want: true},
		{name: "user local install", path: "/Users/some-user/.local/bin/dnsvard", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isBrewManagedInstall(tc.path)
			if got != tc.want {
				t.Fatalf("isBrewManagedInstall(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsLikelyBrewLinkedBinaryPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "apple silicon brew bin", path: "/opt/homebrew/bin/dnsvard", want: true},
		{name: "intel mac brew bin", path: "/usr/local/bin/dnsvard", want: true},
		{name: "linuxbrew bin", path: "/home/linuxbrew/.linuxbrew/bin/dnsvard", want: true},
		{name: "non-brew bin", path: "/Users/some-user/.local/bin/dnsvard", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isLikelyBrewLinkedBinaryPath(tc.path)
			if got != tc.want {
				t.Fatalf("isLikelyBrewLinkedBinaryPath(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
}
