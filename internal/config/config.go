package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/comment-slayer/dnsvard/internal/netutil"
	"github.com/comment-slayer/dnsvard/internal/ownership"

	"gopkg.in/yaml.v3"
)

const DefaultLocalConfigName = "dnsvard.yaml"

type Config struct {
	Domains             []string `yaml:"-"`
	Domain              string   `yaml:"suffix"`
	HostPattern         string   `yaml:"host_pattern"`
	LoopbackCIDR        string   `yaml:"loopback_cidr"`
	DNSListen           string   `yaml:"dns_listen"`
	DNSTTL              uint32   `yaml:"dns_ttl"`
	HTTPPort            int      `yaml:"http_port"`
	StateDir            string   `yaml:"state_dir"`
	LogLevel            string   `yaml:"log_level"`
	DockerDiscoveryMode string   `yaml:"docker_discovery_mode"`

	Project   ProjectInfo   `yaml:"-"`
	Workspace WorkspaceInfo `yaml:"-"`
}

type ProjectInfo struct {
	RemoteURL string `yaml:"-"`
	RepoBase  string `yaml:"-"`
}

type WorkspaceInfo struct {
	WorktreeBase string `yaml:"-"`
	Branch       string `yaml:"-"`
	CwdBase      string `yaml:"-"`
	Path         string `yaml:"-"`
}

type LoadOptions struct {
	CWD            string
	ExplicitPath   string
	LocalName      string
	SkipGlobalInit bool
}

const (
	DockerDiscoveryModeRequired = "required"
	DockerDiscoveryModeOptional = "optional"
)

func Default() Config {
	home := effectiveHomeDir()
	stateDir := filepath.Join(home, ".local", "state", "dnsvard")
	return Config{
		Domains:             []string{"test"},
		Domain:              "test",
		HostPattern:         "service-workspace-project-tld",
		LoopbackCIDR:        "127.90.0.0/16",
		DNSListen:           "127.0.0.1:1053",
		DNSTTL:              5,
		HTTPPort:            80,
		StateDir:            stateDir,
		LogLevel:            "info",
		DockerDiscoveryMode: DockerDiscoveryModeRequired,
	}
}

func Load(opts LoadOptions) (Config, error) {
	if opts.CWD == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd: %w", err)
		}
		opts.CWD = wd
	}

	if opts.LocalName == "" {
		opts.LocalName = DefaultLocalConfigName
	}

	cfg := Default()

	globalPath := GlobalConfigPath()
	localPath := filepath.Join(opts.CWD, opts.LocalName)

	if err := loadGlobalYAMLFile(globalPath, &cfg, !opts.SkipGlobalInit); err != nil {
		return Config{}, err
	}
	if err := loadYAMLFile(localPath, &cfg); err != nil {
		return Config{}, err
	}

	if opts.ExplicitPath != "" {
		if err := loadYAMLFile(opts.ExplicitPath, &cfg); err != nil {
			return Config{}, err
		}
	}

	applyEnv(&cfg)
	cfg.Domain = normalizeSuffix(cfg.Domain)
	if cfg.Domain == "" {
		cfg.Domain = normalizeSuffix(Default().Domain)
	}
	if cfg.Domain != "" {
		cfg.Domains = []string{cfg.Domain}
	}
	if strings.TrimSpace(cfg.HostPattern) == "" {
		cfg.HostPattern = "service-workspace-project-tld"
	}
	mode := normalizeDockerDiscoveryMode(cfg.DockerDiscoveryMode)
	if mode == "" {
		mode = DockerDiscoveryModeRequired
	}
	cfg.DockerDiscoveryMode = mode
	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	repoRoot := gitValue(opts.CWD, "rev-parse", "--show-toplevel")
	if repoRoot == "" {
		repoRoot = opts.CWD
	}

	repoBase := filepath.Base(repoRoot)
	if commonGitDir := gitValue(opts.CWD, "rev-parse", "--path-format=absolute", "--git-common-dir"); commonGitDir != "" {
		if strings.HasSuffix(commonGitDir, ".git") {
			repoBase = filepath.Base(filepath.Dir(commonGitDir))
		}
	}

	cfg.Project = ProjectInfo{
		RemoteURL: gitValue(opts.CWD, "remote", "get-url", "origin"),
		RepoBase:  repoBase,
	}

	branch := gitValue(opts.CWD, "branch", "--show-current")
	if branch == "" {
		branch = gitValue(opts.CWD, "rev-parse", "--abbrev-ref", "HEAD")
	}

	cfg.Workspace = WorkspaceInfo{
		WorktreeBase: filepath.Base(opts.CWD),
		Branch:       branch,
		CwdBase:      filepath.Base(opts.CWD),
		Path:         opts.CWD,
	}

	return cfg, nil
}

type globalConfigFile struct {
	Suffix              string `yaml:"suffix"`
	HostPattern         string `yaml:"host_pattern"`
	LoopbackCIDR        string `yaml:"loopback_cidr"`
	DNSListen           string `yaml:"dns_listen"`
	DNSTTL              uint32 `yaml:"dns_ttl"`
	HTTPPort            int    `yaml:"http_port"`
	StateDir            string `yaml:"state_dir"`
	LogLevel            string `yaml:"log_level"`
	DockerDiscoveryMode string `yaml:"docker_discovery_mode"`
}

func loadGlobalYAMLFile(path string, cfg *Config, initializeMissing bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !initializeMissing {
				return nil
			}
			if err := writeGlobalConfigFile(path, globalConfigFile{}); err != nil {
				return fmt.Errorf("initialize global config %s: %w", path, err)
			}
			if err := ownership.ChownPathAndParentToSudoInvoker(path); err != nil {
				return fmt.Errorf("initialize global config %s: %w", path, err)
			}
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}

	loaded := globalConfigFile{}
	if err := yaml.Unmarshal(b, &loaded); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if v := normalizeSuffix(loaded.Suffix); v != "" {
		cfg.Domain = v
	}
	if v := strings.TrimSpace(loaded.HostPattern); v != "" {
		cfg.HostPattern = v
	}
	if strings.TrimSpace(loaded.LoopbackCIDR) != "" {
		cfg.LoopbackCIDR = loaded.LoopbackCIDR
	}
	if strings.TrimSpace(loaded.DNSListen) != "" {
		cfg.DNSListen = loaded.DNSListen
	}
	if loaded.DNSTTL != 0 {
		cfg.DNSTTL = loaded.DNSTTL
	}
	if loaded.HTTPPort != 0 {
		cfg.HTTPPort = loaded.HTTPPort
	}
	if strings.TrimSpace(loaded.StateDir) != "" {
		cfg.StateDir = loaded.StateDir
	}
	if strings.TrimSpace(loaded.LogLevel) != "" {
		cfg.LogLevel = loaded.LogLevel
	}
	if mode := normalizeDockerDiscoveryMode(loaded.DockerDiscoveryMode); mode != "" {
		cfg.DockerDiscoveryMode = mode
	}
	return nil
}

func loadYAMLFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(b, cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("DNSVARD_SUFFIX")); v != "" {
		cfg.Domain = v
	}
	if v := strings.TrimSpace(os.Getenv("DNSVARD_HOST_PATTERN")); v != "" {
		cfg.HostPattern = v
	}
	if v := os.Getenv("DNSVARD_LOOPBACK_CIDR"); v != "" {
		cfg.LoopbackCIDR = v
	}
	if v := os.Getenv("DNSVARD_DNS_LISTEN"); v != "" {
		cfg.DNSListen = v
	}
	if v := os.Getenv("DNSVARD_STATE_DIR"); v != "" {
		cfg.StateDir = v
	}
	if v := os.Getenv("DNSVARD_HTTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.HTTPPort = p
		}
	}
	if v := os.Getenv("DNSVARD_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if mode := normalizeDockerDiscoveryMode(os.Getenv("DNSVARD_DOCKER_DISCOVERY_MODE")); mode != "" {
		cfg.DockerDiscoveryMode = mode
	}
}

func validate(cfg Config) error {
	domain := normalizeSuffix(cfg.Domain)
	if domain == "" {
		return errors.New("suffix is required\nfix: set `suffix` in config (for example: test)")
	}
	if err := validateSuffixValue(domain); err != nil {
		return fmt.Errorf("suffix %q is invalid\nwhy: %v", cfg.Domain, err)
	}

	if strings.TrimSpace(cfg.HostPattern) == "" {
		return errors.New("host_pattern is required\nfix: set `host_pattern` (for example: service-workspace-project-tld)")
	}
	if strings.TrimSpace(cfg.DNSListen) == "" {
		return errors.New("dns_listen is required\nfix: set `dns_listen` to host:port (for example: 127.0.0.1:1053)")
	}
	if _, _, err := net.SplitHostPort(strings.TrimSpace(cfg.DNSListen)); err != nil {
		return fmt.Errorf("dns_listen %q is invalid\nfix: set `dns_listen` to host:port (for example: 127.0.0.1:1053)", cfg.DNSListen)
	}
	if strings.TrimSpace(cfg.LoopbackCIDR) == "" {
		return errors.New("loopback_cidr is required\nfix: set `loopback_cidr` to a valid private CIDR (for example: 127.90.0.0/16)")
	}
	if strings.TrimSpace(cfg.StateDir) == "" {
		return errors.New("state_dir is required\nfix: set `state_dir` to a writable directory path")
	}
	if _, err := SafeStateDirPath(cfg.StateDir); err != nil {
		return err
	}
	if cfg.DNSTTL == 0 {
		return errors.New("dns_ttl must be greater than zero\nfix: set `dns_ttl` to a positive integer")
	}
	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		return errors.New("http_port must be a valid TCP port\nfix: set `http_port` between 1 and 65535")
	}
	if listenPort, ok := netutil.ParseListenPort(cfg.DNSListen); ok && listenPort == cfg.HTTPPort {
		return fmt.Errorf("config conflict: dns_listen port %d and http_port %d are identical\nfix: set `dns_listen` and `http_port` to different ports", listenPort, cfg.HTTPPort)
	}
	mode := normalizeDockerDiscoveryMode(cfg.DockerDiscoveryMode)
	if mode != "" && mode != DockerDiscoveryModeRequired && mode != DockerDiscoveryModeOptional {
		return fmt.Errorf("docker_discovery_mode must be %q or %q\nfix: set `docker_discovery_mode` to one of those values", DockerDiscoveryModeRequired, DockerDiscoveryModeOptional)
	}
	return nil
}

func normalizeDockerDiscoveryMode(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func ValidateStateDirSafety(stateDir string) error {
	_, err := SafeStateDirPath(stateDir)
	return err
}

func SafeStateDirPath(stateDir string) (string, error) {
	home := effectiveHomeDir()
	if strings.TrimSpace(home) == "" {
		return "", errors.New("state_dir validation failed: unable to resolve home directory\nfix: ensure HOME is set and rerun")
	}
	return resolveAndValidateStateDir(stateDir, home)
}

func resolveAndValidateStateDir(stateDir string, homeDir string) (string, error) {
	raw := strings.TrimSpace(stateDir)
	if raw == "" {
		return "", errors.New("state_dir is required\nfix: set `state_dir` to an absolute path under your home directory")
	}
	cleaned := filepath.Clean(raw)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("state_dir %q must be an absolute path\nfix: set `state_dir` to an absolute path under your home directory", stateDir)
	}
	absStateDir, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("state_dir %q cannot be resolved\nwhy: %v\nfix: set `state_dir` to a valid absolute path", stateDir, err)
	}
	absHomeDir, err := filepath.Abs(filepath.Clean(strings.TrimSpace(homeDir)))
	if err != nil {
		return "", fmt.Errorf("state_dir validation failed: cannot resolve home directory\nwhy: %v\nfix: ensure HOME points to a valid path", err)
	}
	if !pathWithin(absHomeDir, absStateDir) {
		return "", fmt.Errorf("state_dir %q is outside home directory %q\nfix: set `state_dir` under your home directory (example: %s)", stateDir, absHomeDir, filepath.Join(absHomeDir, ".local", "state", "dnsvard"))
	}
	resolvedStateDir, err := resolvePathWithoutSymlinkEscape(absStateDir)
	if err != nil {
		return "", fmt.Errorf("state_dir %q is unsafe\nwhy: %v\nfix: use a real directory path (not symlinked) under your home directory ending in /dnsvard", stateDir, err)
	}
	resolvedHomeDir, err := resolvePathWithoutSymlinkEscape(absHomeDir)
	if err != nil {
		return "", fmt.Errorf("state_dir validation failed: home directory is unsafe\nwhy: %v\nfix: use a real home directory path", err)
	}
	if !pathWithin(resolvedHomeDir, resolvedStateDir) {
		return "", fmt.Errorf("state_dir %q resolves outside home directory %q\nfix: set `state_dir` under your home directory (example: %s)", stateDir, resolvedHomeDir, filepath.Join(resolvedHomeDir, ".local", "state", "dnsvard"))
	}
	if filepath.Base(resolvedStateDir) != "dnsvard" {
		return "", fmt.Errorf("state_dir %q is too broad\nfix: set `state_dir` to a dedicated dnsvard directory ending with /dnsvard (example: %s)", stateDir, filepath.Join(resolvedHomeDir, ".local", "state", "dnsvard"))
	}
	if resolvedStateDir == resolvedHomeDir {
		return "", fmt.Errorf("state_dir %q cannot point to the home directory\nfix: set `state_dir` to a dedicated dnsvard directory (example: %s)", stateDir, filepath.Join(resolvedHomeDir, ".local", "state", "dnsvard"))
	}
	if info, err := os.Lstat(resolvedStateDir); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("state_dir %q points to a file\nfix: set `state_dir` to a directory path ending with /dnsvard", stateDir)
		}
	}
	return resolvedStateDir, nil
}

func resolvePathWithoutSymlinkEscape(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", errors.New("empty path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}
	missing := []string{}
	current := path
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("symlink component detected at %q", current)
			}
			if len(missing) == 0 && !info.IsDir() {
				return "", fmt.Errorf("path %q is not a directory", current)
			}
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			resolved = filepath.Clean(resolved)
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing parent for %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathWithin(base string, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func GlobalConfigPath() string {
	home := effectiveHomeDir()
	return filepath.Join(home, ".config", "dnsvard", "config.yaml")
}

func LocalConfigPath(cwd string, explicitPath string) string {
	if strings.TrimSpace(explicitPath) != "" {
		return explicitPath
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	return filepath.Join(cwd, DefaultLocalConfigName)
}

func SetLocalSuffix(path string, suffix string) (bool, error) {
	normalized := normalizeSuffix(suffix)
	if normalized == "" {
		return false, errors.New("suffix is required")
	}
	if err := validateSuffixValue(normalized); err != nil {
		return false, fmt.Errorf("suffix %q is invalid\nwhy: %v", suffix, err)
	}

	local := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read local config %s: %w", path, err)
	}
	if len(b) > 0 {
		if err := yaml.Unmarshal(b, &local); err != nil {
			return false, fmt.Errorf("parse local config %s: %w", path, err)
		}
	}
	if current, ok := local["suffix"].(string); ok && normalizeSuffix(current) == normalized {
		return false, nil
	}
	local["suffix"] = normalized
	out, err := yaml.Marshal(local)
	if err != nil {
		return false, fmt.Errorf("encode local config %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create local config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("write local config %s: %w", path, err)
	}
	return true, nil
}

func SetLocalHostPattern(path string, pattern string) (bool, error) {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false, errors.New("host_pattern is required")
	}

	local := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read local config %s: %w", path, err)
	}
	if len(b) > 0 {
		if err := yaml.Unmarshal(b, &local); err != nil {
			return false, fmt.Errorf("parse local config %s: %w", path, err)
		}
	}
	if current, ok := local["host_pattern"].(string); ok && strings.ToLower(strings.TrimSpace(current)) == pattern {
		return false, nil
	}
	local["host_pattern"] = pattern
	out, err := yaml.Marshal(local)
	if err != nil {
		return false, fmt.Errorf("encode local config %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create local config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("write local config %s: %w", path, err)
	}
	return true, nil
}

var domainLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeSuffix(v string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(v), "."))
}

func validateSuffixValue(v string) error {
	v = normalizeSuffix(v)
	if v == "" {
		return errors.New("suffix is empty")
	}
	if v == "local" || strings.HasSuffix(v, ".local") {
		return errors.New("`.local` is reserved for mDNS/Bonjour and conflicts with unicast DNS routing\nfix: use a non-.local suffix (for example: test or dev.test)")
	}
	if len(v) > 253 {
		return errors.New("suffix length exceeds DNS limit (253 characters)")
	}
	parts := strings.Split(v, ".")
	for _, part := range parts {
		if !domainLabelPattern.MatchString(part) {
			return errors.New("use lowercase alphanumeric/hyphen labels separated by dots")
		}
	}
	return nil
}

func writeGlobalConfigFile(path string, loaded globalConfigFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create global config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(renderGlobalConfigFile(loaded)), 0o644); err != nil {
		return err
	}
	return nil
}

func renderGlobalConfigFile(loaded globalConfigFile) string {
	defaults := Default()

	loopbackCIDR := firstNonEmpty(loaded.LoopbackCIDR, defaults.LoopbackCIDR)
	dnsListen := firstNonEmpty(loaded.DNSListen, defaults.DNSListen)
	dnsTTL := firstNonZero(loaded.DNSTTL, defaults.DNSTTL)
	httpPort := firstNonZeroInt(loaded.HTTPPort, defaults.HTTPPort)
	stateDir := firstNonEmpty(loaded.StateDir, defaults.StateDir)
	logLevel := firstNonEmpty(loaded.LogLevel, defaults.LogLevel)
	dockerMode := firstNonEmpty(normalizeDockerDiscoveryMode(loaded.DockerDiscoveryMode), defaults.DockerDiscoveryMode)
	suffix := normalizeSuffix(loaded.Suffix)
	hostPattern := firstNonEmpty(loaded.HostPattern, defaults.HostPattern)
	if suffix == "" {
		suffix = normalizeSuffix(defaults.Domain)
	}
	commentSuffix := normalizeSuffix(loaded.Suffix) == "" && suffix == normalizeSuffix(defaults.Domain)

	b := strings.Builder{}

	writeScalarOption(&b, scalarOption{
		Key:       "suffix",
		Value:     renderScalar(suffix),
		Commented: commentSuffix,
		Comments: []string{
			"Default active DNS suffix when local config does not set `suffix`.",
			"Type: string",
			"Values: DNS suffix, e.g. test, dev.test",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "host_pattern",
		Value:     renderScalar(hostPattern),
		Commented: hostPattern == defaults.HostPattern,
		Comments: []string{
			"Default host naming pattern when local config does not set `host_pattern`.",
			"Type: string",
			"Values: workspace-tld, workspace-project-tld, service-workspace-project-tld",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "loopback_cidr",
		Value:     renderScalar(loopbackCIDR),
		Commented: loopbackCIDR == defaults.LoopbackCIDR,
		Comments: []string{
			"Loopback CIDR used for workspace IP allocation.",
			"Type: string (IPv4 CIDR)",
			"Values: valid CIDR, e.g. 127.90.0.0/16",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "dns_listen",
		Value:     renderScalar(dnsListen),
		Commented: dnsListen == defaults.DNSListen,
		Comments: []string{
			"DNS server listen address.",
			"Type: string (host:port)",
			"Values: valid TCP/UDP listen endpoint, e.g. 127.0.0.1:1053",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "dns_ttl",
		Value:     fmt.Sprintf("%d", dnsTTL),
		Commented: dnsTTL == defaults.DNSTTL,
		Comments: []string{
			"DNS answer TTL in seconds.",
			"Type: uint32",
			"Values: integer > 0",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "http_port",
		Value:     fmt.Sprintf("%d", httpPort),
		Commented: httpPort == defaults.HTTPPort,
		Comments: []string{
			"HTTP host-router listen port.",
			"Type: integer",
			"Values: 1-65535 (ports <1024 may require elevated privileges)",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "state_dir",
		Value:     renderScalar(stateDir),
		Commented: stateDir == defaults.StateDir,
		Comments: []string{
			"Persistent state directory for allocator, runtime leases, and daemon state.",
			"Type: string (filesystem path)",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "log_level",
		Value:     renderScalar(logLevel),
		Commented: logLevel == defaults.LogLevel,
		Comments: []string{
			"Daemon log verbosity.",
			"Type: string",
			"Values: debug, info, warn, warning, error",
		},
	})
	writeScalarOption(&b, scalarOption{
		Key:       "docker_discovery_mode",
		Value:     renderScalar(dockerMode),
		Commented: dockerMode == defaults.DockerDiscoveryMode,
		Comments: []string{
			"Docker discovery strictness when enumerating containers.",
			"Type: string",
			"Values: required, optional",
		},
	})

	out := strings.TrimRight(b.String(), "\n")
	return out + "\n"
}

type scalarOption struct {
	Key       string
	Value     string
	Commented bool
	Comments  []string
}

func writeScalarOption(b *strings.Builder, option scalarOption) {
	writeCommentLines(b, option.Comments)
	writeLine(b, option.Key+": "+option.Value, option.Commented)
	b.WriteString("\n")
}

func writeCommentLines(b *strings.Builder, lines []string) {
	for _, line := range lines {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteString("\n")
	}
}

func writeLine(b *strings.Builder, line string, commented bool) {
	if commented {
		b.WriteString("#")
	}
	b.WriteString(line)
	b.WriteString("\n")
}

func renderScalar(v string) string {
	if plainScalarPattern.MatchString(v) {
		return v
	}
	return strconv.Quote(v)
}

func firstNonEmpty(value string, fallback string) string {
	v := strings.TrimSpace(value)
	if v != "" {
		return v
	}
	return fallback
}

func firstNonZero(value uint32, fallback uint32) uint32 {
	if value != 0 {
		return value
	}
	return fallback
}

func firstNonZeroInt(value int, fallback int) int {
	if value != 0 {
		return value
	}
	return fallback
}

var plainScalarPattern = regexp.MustCompile(`^[a-zA-Z0-9._/:-]+$`)

func gitValue(cwd string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func effectiveHomeDir() string {
	if os.Geteuid() == 0 {
		sudoUID := strings.TrimSpace(os.Getenv("SUDO_UID"))
		if sudoUID != "" {
			u, err := user.LookupId(sudoUID)
			if err == nil && strings.TrimSpace(u.HomeDir) != "" {
				return u.HomeDir
			}
		}
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home
	}
	return "."
}
