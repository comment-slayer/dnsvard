package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setIsolatedHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_UID", "")
	t.Setenv("DNSVARD_SUFFIX", "")
	t.Setenv("DNSVARD_HOST_PATTERN", "")
}

func writeGlobalConfigYAML(t *testing.T, content string) string {
	t.Helper()
	path := GlobalConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	return path
}

func TestLoadDefaultsSuffixToTest(t *testing.T) {
	setIsolatedHome(t)

	_ = writeGlobalConfigYAML(t, "")
	wd := t.TempDir()

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
	if len(cfg.Domains) != 1 || cfg.Domains[0] != "test" {
		t.Fatalf("Domains = %v, want [test]", cfg.Domains)
	}
}

func TestLoadUsesGlobalSuffix(t *testing.T) {
	setIsolatedHome(t)

	writeGlobalConfigYAML(t, "suffix: dev.test\n")
	wd := t.TempDir()

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "dev.test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "dev.test")
	}
	if len(cfg.Domains) != 1 || cfg.Domains[0] != "dev.test" {
		t.Fatalf("Domains = %v, want [dev.test]", cfg.Domains)
	}
}

func TestLoadUsesGlobalHostPattern(t *testing.T) {
	setIsolatedHome(t)

	writeGlobalConfigYAML(t, "host_pattern: workspace-tld\n")
	wd := t.TempDir()

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.HostPattern != "workspace-tld" {
		t.Fatalf("HostPattern = %q, want %q", cfg.HostPattern, "workspace-tld")
	}
}

func TestLoadSkipGlobalInitDoesNotCreateGlobalConfig(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	if _, err := os.Stat(GlobalConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing global config before load, got: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: wd, SkipGlobalInit: true})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
	if _, err := os.Stat(GlobalConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected global config to remain missing, got: %v", err)
	}
}

func TestLoadLocalSuffixOverridesGlobal(t *testing.T) {
	setIsolatedHome(t)

	writeGlobalConfigYAML(t, "suffix: cs\n")
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, DefaultLocalConfigName), []byte("suffix: foo.bar.baz\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "foo.bar.baz" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "foo.bar.baz")
	}
	if len(cfg.Domains) != 1 || cfg.Domains[0] != "foo.bar.baz" {
		t.Fatalf("Domains = %v, want [foo.bar.baz]", cfg.Domains)
	}
}

func TestLoadFindsNearestAncestorLocalConfig(t *testing.T) {
	setIsolatedHome(t)

	root := t.TempDir()
	child := filepath.Join(root, "www")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, DefaultLocalConfigName), []byte("suffix: dnsvard\nhost_pattern: service-workspace-tld\n"), 0o644); err != nil {
		t.Fatalf("write root local config: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: child, SkipGlobalInit: true})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "dnsvard" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "dnsvard")
	}
	if cfg.HostPattern != "service-workspace-tld" {
		t.Fatalf("HostPattern = %q, want %q", cfg.HostPattern, "service-workspace-tld")
	}
}

func TestLoadMergesAncestorLocalConfigsNearestWins(t *testing.T) {
	setIsolatedHome(t)

	root := t.TempDir()
	levelA := filepath.Join(root, "a")
	levelB := filepath.Join(levelA, "b")
	levelC := filepath.Join(levelB, "c")
	if err := os.MkdirAll(levelC, 0o755); err != nil {
		t.Fatalf("mkdir nested dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(levelA, DefaultLocalConfigName), []byte("suffix: outer.test\nhost_pattern: workspace-project-tld\n"), 0o644); err != nil {
		t.Fatalf("write ancestor config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(levelB, DefaultLocalConfigName), []byte("host_pattern: service-workspace-tld\n"), 0o644); err != nil {
		t.Fatalf("write nearest config: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: levelC, SkipGlobalInit: true})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "outer.test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "outer.test")
	}
	if cfg.HostPattern != "service-workspace-tld" {
		t.Fatalf("HostPattern = %q, want %q", cfg.HostPattern, "service-workspace-tld")
	}
}

func TestLoadStopsAncestorSearchAtHomeDir(t *testing.T) {
	outsideRoot := t.TempDir()
	home := filepath.Join(outsideRoot, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SUDO_UID", "")
	t.Setenv("DNSVARD_SUFFIX", "")
	t.Setenv("DNSVARD_HOST_PATTERN", "")

	if err := os.WriteFile(filepath.Join(outsideRoot, DefaultLocalConfigName), []byte("suffix: leaked.test\n"), 0o644); err != nil {
		t.Fatalf("write outside config: %v", err)
	}
	cwd := filepath.Join(home, "projects", "repo", "www")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: cwd, SkipGlobalInit: true})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
}

func TestLoadIgnoresSuffixesInGlobalConfig(t *testing.T) {
	setIsolatedHome(t)

	writeGlobalConfigYAML(t, "suffixes:\n  - cs\n")
	cfg, err := Load(LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
}

func TestLoadIgnoresDomainInGlobalConfig(t *testing.T) {
	setIsolatedHome(t)

	writeGlobalConfigYAML(t, "domain: cs\n")
	cfg, err := Load(LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
}

func TestLoadIgnoresSuffixesInLocalConfig(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, DefaultLocalConfigName), []byte("suffixes:\n  - test\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
}

func TestLoadIgnoresDomainInLocalConfig(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, DefaultLocalConfigName), []byte("domain: cs\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Domain != "test" {
		t.Fatalf("Domain = %q, want %q", cfg.Domain, "test")
	}
}

func TestLoadDefaultDockerDiscoveryMode(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DockerDiscoveryMode != DockerDiscoveryModeRequired {
		t.Fatalf("DockerDiscoveryMode = %q, want %q", cfg.DockerDiscoveryMode, DockerDiscoveryModeRequired)
	}
}

func TestLoadDockerDiscoveryModeValidation(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	configPath := filepath.Join(wd, DefaultLocalConfigName)
	if err := os.WriteFile(configPath, []byte("suffix: test\ndocker_discovery_mode: invalid\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	_, err := Load(LoadOptions{CWD: wd})
	if err == nil {
		t.Fatal("expected validation error for invalid docker_discovery_mode")
	}
	if !strings.Contains(err.Error(), "docker_discovery_mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDockerDiscoveryModeOptional(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	configPath := filepath.Join(wd, DefaultLocalConfigName)
	if err := os.WriteFile(configPath, []byte("suffix: test\ndocker_discovery_mode: optional\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	cfg, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DockerDiscoveryMode != DockerDiscoveryModeOptional {
		t.Fatalf("DockerDiscoveryMode = %q, want %q", cfg.DockerDiscoveryMode, DockerDiscoveryModeOptional)
	}
}

func TestLoadCreatesCommentedGlobalConfigTemplate(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	_, err := Load(LoadOptions{CWD: wd})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	path := GlobalConfigPath()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "#suffix: test") {
		t.Fatalf("expected commented default suffix, got:\n%s", content)
	}
	if !strings.Contains(content, "#host_pattern: service-workspace-project-tld") {
		t.Fatalf("expected commented default host_pattern, got:\n%s", content)
	}
	if strings.Contains(content, "suffixes:") {
		t.Fatalf("expected no suffixes key in template, got:\n%s", content)
	}
	if !strings.Contains(content, "#docker_discovery_mode: required") {
		t.Fatalf("expected commented default docker mode, got:\n%s", content)
	}
	if strings.HasSuffix(content, "\n\n") {
		t.Fatalf("expected no trailing blank line, got:\n%s", content)
	}
}

func TestSetLocalSuffixAllowsMultiLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultLocalConfigName)
	changed, err := SetLocalSuffix(path, "dev.test")
	if err != nil {
		t.Fatalf("SetLocalSuffix returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	if !strings.Contains(string(b), "suffix: dev.test") {
		t.Fatalf("expected suffix in local config, got:\n%s", string(b))
	}
}

func TestSetLocalSuffixRejectsLocalSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultLocalConfigName)
	_, err := SetLocalSuffix(path, "dev.local")
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "mDNS/Bonjour") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsLocalSuffix(t *testing.T) {
	setIsolatedHome(t)

	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, DefaultLocalConfigName), []byte("suffix: dev.local\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	_, err := Load(LoadOptions{CWD: wd})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "mDNS/Bonjour") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfigConflictDNSListenAndHTTPPort(t *testing.T) {
	setIsolatedHome(t)
	home := os.Getenv("HOME")
	stateDir := filepath.Join(home, ".local", "state", "dnsvard")

	err := validate(Config{
		Domains:      []string{"test"},
		Domain:       "test",
		HostPattern:  "service-workspace-project-tld",
		DNSListen:    "127.0.0.1:8080",
		LoopbackCIDR: "127.90.0.0/16",
		StateDir:     stateDir,
		DNSTTL:       5,
		HTTPPort:     8080,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "config conflict") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "fix:") {
		t.Fatalf("expected fix lines in error: %v", err)
	}
}

func TestValidateInvalidDNSListenIncludesFix(t *testing.T) {
	err := validate(Config{
		Domains:      []string{"test"},
		Domain:       "test",
		HostPattern:  "service-workspace-project-tld",
		DNSListen:    "not-a-listen",
		LoopbackCIDR: "127.90.0.0/16",
		StateDir:     "/tmp",
		DNSTTL:       5,
		HTTPPort:     80,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "dns_listen") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "fix:") {
		t.Fatalf("expected fix lines in error: %v", err)
	}
}

func TestSafeStateDirPathTable(t *testing.T) {
	setIsolatedHome(t)
	home := os.Getenv("HOME")

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "default under home", path: filepath.Join(home, ".local", "state", "dnsvard")},
		{name: "custom nested dnsvard", path: filepath.Join(home, "work", "state", "dnsvard")},
		{name: "relative path rejected", path: "dnsvard", wantErr: "must be an absolute path"},
		{name: "outside home rejected", path: filepath.Join(string(filepath.Separator), "tmp", "dnsvard"), wantErr: "outside home directory"},
		{name: "broad home path rejected", path: home, wantErr: "too broad"},
		{name: "non-dnsvard leaf rejected", path: filepath.Join(home, ".local", "state", "cache"), wantErr: "too broad"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := SafeStateDirPath(tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("SafeStateDirPath(%q) error: %v", tc.path, err)
				}
				if !strings.HasSuffix(got, string(filepath.Separator)+"dnsvard") {
					t.Fatalf("resolved path = %q, expected dnsvard leaf", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("SafeStateDirPath(%q) expected error containing %q", tc.path, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSafeStateDirPathRejectsSymlinkEscape(t *testing.T) {
	setIsolatedHome(t)
	home := os.Getenv("HOME")
	outside := t.TempDir()

	symlinkBase := filepath.Join(home, "unsafe-link")
	if err := os.Symlink(outside, symlinkBase); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := SafeStateDirPath(filepath.Join(symlinkBase, "dnsvard"))
	if err == nil {
		t.Fatal("expected symlink safety error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsMalformedStateDirFromEnv(t *testing.T) {
	setIsolatedHome(t)

	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "relative state dir", value: "dnsvard", wantErr: "must be an absolute path"},
		{name: "outside home", value: filepath.Join(string(filepath.Separator), "tmp", "dnsvard"), wantErr: "outside home directory"},
		{name: "broad home path", value: os.Getenv("HOME"), wantErr: "is too broad"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DNSVARD_STATE_DIR", tc.value)
			_, err := Load(LoadOptions{CWD: t.TempDir()})
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSafeStateDirPathRejectsExistingFile(t *testing.T) {
	setIsolatedHome(t)
	home := os.Getenv("HOME")

	stateFile := filepath.Join(home, ".local", "state", "dnsvard")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := SafeStateDirPath(stateFile)
	if err == nil {
		t.Fatal("expected file path rejection")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}
