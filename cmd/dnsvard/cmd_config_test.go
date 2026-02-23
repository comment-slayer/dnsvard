package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/comment-slayer/dnsvard/internal/config"
)

func setConfigTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DNSVARD_SUFFIX", "")
	t.Setenv("DNSVARD_HOST_PATTERN", "")
}

func TestConfigCommandTreeIncludesScopedAPI(t *testing.T) {
	configPath := ""
	cmd := newConfigCommand(context.Background(), &configPath)

	names := map[string]struct{}{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = struct{}{}
	}
	for _, required := range []string{"global", "local"} {
		if _, ok := names[required]; !ok {
			t.Fatalf("missing %q subcommand", required)
		}
	}
	if _, ok := names["effective"]; ok {
		t.Fatalf("unexpected %q subcommand", "effective")
	}
}

func TestConfigGlobalDefaultShowsHelpWithSettableKeys(t *testing.T) {
	setConfigTestEnv(t)
	configPath := ""
	cmd := newConfigGlobalCommand(&configPath)

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("global default RunE: %v", err)
	}
	for _, key := range []string{"Settable keys:", "suffix (type: string", "host_pattern (type: string", "loopback_cidr (type: cidr", "docker_discovery_mode (type: string", "dnsvard config global show"} {
		if !strings.Contains(out.String(), key) {
			t.Fatalf("global help output missing %q:\n%s", key, out.String())
		}
	}
}

func TestConfigLocalDefaultShowsHelpWithSettableKeys(t *testing.T) {
	setConfigTestEnv(t)
	configPath := ""
	cmd := newConfigLocalCommand(&configPath)

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("local default RunE: %v", err)
	}
	for _, key := range []string{"Settable keys:", "suffix (type: string", "host_pattern (type: string", "dnsvard config local show"} {
		if !strings.Contains(out.String(), key) {
			t.Fatalf("local help output missing %q:\n%s", key, out.String())
		}
	}
}

func TestConfigShowCommandNameAndAlias(t *testing.T) {
	configPath := ""
	cmd := newConfigGlobalCommand(&configPath)

	var show *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "show" {
			show = sub
			break
		}
	}
	if show == nil {
		t.Fatal("missing show command")
	}
	aliases := map[string]struct{}{}
	for _, alias := range show.Aliases {
		aliases[alias] = struct{}{}
	}
	if _, ok := aliases["list"]; !ok {
		t.Fatal("show command should keep list alias")
	}
	for _, sub := range cmd.Commands() {
		if sub.Name() == "dump" {
			t.Fatal("dump command should not exist")
		}
	}
}

func TestConfigShowGlobalShowsValuesWithDefaultMarkers(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	var out bytes.Buffer
	if err := printScopedConfigTo(&out, configScopeGlobal, cwd, "", nil); err != nil {
		t.Fatalf("printScopedConfig(global): %v", err)
	}
	if strings.Contains(out.String(), "<unset>") {
		t.Fatalf("show output should not contain <unset>:\n%s", out.String())
	}
	for _, line := range []string{"suffix=test (default)", "host_pattern=service-workspace-project-tld (default)", "loopback_cidr=127.90.0.0/16 (default)", "docker_discovery_mode=required (default)"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("show output missing %q:\n%s", line, out.String())
		}
	}
}

func TestConfigShowLocalShowsResolvedValuesAndDefaultMarker(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	globalPath := config.GlobalConfigPath()
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("suffix: dev.test\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	var out bytes.Buffer
	if err := printScopedConfigTo(&out, configScopeLocal, cwd, "", nil); err != nil {
		t.Fatalf("printScopedConfig(local): %v", err)
	}
	if strings.Contains(out.String(), "<unset>") {
		t.Fatalf("show output should not contain <unset>:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "suffix=dev.test") {
		t.Fatalf("show output missing resolved suffix:\n%s", out.String())
	}
	if strings.Contains(out.String(), "suffix=dev.test (default)") {
		t.Fatalf("suffix should not be marked default when inherited non-default:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "host_pattern=service-workspace-project-tld (default)") {
		t.Fatalf("show output missing host_pattern default marker:\n%s", out.String())
	}
}

func TestSetScopedConfigValueGlobalWritesSuffix(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	path, changed, err := setScopedConfigValue(scopedConfigSetRequest{Scope: configScopeGlobal, CWD: cwd, ExplicitPath: "", Key: "suffix", RawValue: "dev.test"})
	if err != nil {
		t.Fatalf("setScopedConfigValue: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "suffix: dev.test") {
		t.Fatalf("expected suffix key in global config, got:\n%s", content)
	}
	if strings.Contains(content, "suffixes:") {
		t.Fatalf("global config should not contain suffixes key, got:\n%s", content)
	}
}

func TestSetScopedConfigValueGlobalWritesHostPattern(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	path, changed, err := setScopedConfigValue(scopedConfigSetRequest{Scope: configScopeGlobal, CWD: cwd, ExplicitPath: "", Key: "host_pattern", RawValue: "workspace-tld"})
	if err != nil {
		t.Fatalf("setScopedConfigValue: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(b), "host_pattern: workspace-tld") {
		t.Fatalf("expected host_pattern in global config, got:\n%s", string(b))
	}
}

func TestConfigGlobalSetHostPatternViaCLI(t *testing.T) {
	setConfigTestEnv(t)

	badConfigPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badConfigPath, []byte("suffix: ["), 0o644); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}

	root := newRootCommand(context.Background())
	root.SetArgs([]string{"--config", badConfigPath, "config", "global", "set", "host_pattern", "workspace-project-tld"})
	var out bytes.Buffer
	var errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute root command: %v", err)
	}
	if !strings.Contains(out.String(), "updated global host_pattern in") {
		t.Fatalf("expected update message, got:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "warning: config updated but runtime reload skipped") {
		t.Fatalf("expected runtime reload warning on stderr, got:\n%s", errOut.String())
	}

	b, err := os.ReadFile(config.GlobalConfigPath())
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(b), "host_pattern: workspace-project-tld") {
		t.Fatalf("expected host_pattern written by CLI, got:\n%s", string(b))
	}
}

func TestSetScopedConfigValueLocalWritesHostPattern(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	path, changed, err := setScopedConfigValue(scopedConfigSetRequest{Scope: configScopeLocal, CWD: cwd, ExplicitPath: "", Key: "host_pattern", RawValue: "workspace-tld"})
	if err != nil {
		t.Fatalf("setScopedConfigValue: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if path != filepath.Join(cwd, config.DefaultLocalConfigName) {
		t.Fatalf("unexpected local config path: %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	if !strings.Contains(string(b), "host_pattern: workspace-tld") {
		t.Fatalf("expected host_pattern in local config, got:\n%s", string(b))
	}
}

func TestSetScopedConfigValueRejectsUnsupportedScopeKey(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	_, _, err := setScopedConfigValue(scopedConfigSetRequest{Scope: configScopeGlobal, CWD: cwd, ExplicitPath: "", Key: "not_a_real_key", RawValue: "workspace-tld"})
	if err == nil {
		t.Fatal("expected unsupported key error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetScopedConfigValueRollsBackOnValidationFailure(t *testing.T) {
	setConfigTestEnv(t)
	cwd := t.TempDir()

	path := config.GlobalConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("suffix: test\n"), 0o644); err != nil {
		t.Fatalf("seed global config: %v", err)
	}

	_, _, err := setScopedConfigValue(scopedConfigSetRequest{Scope: configScopeGlobal, CWD: cwd, ExplicitPath: "", Key: "suffix", RawValue: "dev.local"})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	b, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read global config: %v", readErr)
	}
	if strings.TrimSpace(string(b)) != "suffix: test" {
		t.Fatalf("expected rollback to original config, got:\n%s", string(b))
	}
}
