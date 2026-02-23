package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/identity"
	"github.com/comment-slayer/dnsvard/internal/ownership"
)

type configScope string

const (
	configScopeGlobal configScope = "global"
	configScopeLocal  configScope = "local"
)

var globalConfigKeys = []string{
	"suffix",
	"host_pattern",
	"loopback_cidr",
	"dns_listen",
	"dns_ttl",
	"http_port",
	"state_dir",
	"log_level",
	"docker_discovery_mode",
}

var localConfigKeys = []string{
	"suffix",
	"host_pattern",
}

type configKeyDescriptor struct {
	ValueType string
	Example   string
}

type writableScopeMutation struct {
	Scope          configScope
	ConfigPath     string
	Key            string
	Mutate         func(cwd string) (string, bool, error)
	UnchangedState string
	ChangedVerb    string
	Out            io.Writer
	Err            io.Writer
}

type scopedConfigSetRequest struct {
	Scope        configScope
	CWD          string
	ExplicitPath string
	Key          string
	RawValue     string
}

type scopedConfigMutationRequest struct {
	Scope        configScope
	CWD          string
	ExplicitPath string
	Key          string
	Mutator      func(values map[string]any, normalizedKey string) (bool, error)
}

type configPrintRequest struct {
	Keys     []string
	Values   map[string]any
	Args     []string
	Scope    string
	Defaults map[string]any
}

var configKeyDescriptors = map[string]configKeyDescriptor{
	"suffix":                {ValueType: "string", Example: "dev.test"},
	"host_pattern":          {ValueType: "string", Example: "workspace-tld"},
	"loopback_cidr":         {ValueType: "cidr", Example: "127.90.0.0/16"},
	"dns_listen":            {ValueType: "host:port", Example: "127.0.0.1:1053"},
	"dns_ttl":               {ValueType: "uint32", Example: "5"},
	"http_port":             {ValueType: "int", Example: "80"},
	"state_dir":             {ValueType: "path", Example: "~/.local/state/dnsvard"},
	"log_level":             {ValueType: "string", Example: "info"},
	"docker_discovery_mode": {ValueType: "string", Example: "required"},
}

func newConfigCommand(_ context.Context, configPath *string) *cobra.Command {
	configCmd := &cobra.Command{Use: "config", Short: "Manage dnsvard configuration"}

	configCmd.AddCommand(newConfigGlobalCommand(configPath))
	configCmd.AddCommand(newConfigLocalCommand(configPath))

	return configCmd
}

func newConfigGlobalCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "global",
		Short: "Manage global config",
		Long:  scopedConfigHelpText(configScopeGlobal),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigSetCommand(configScopeGlobal, configPath))
	cmd.AddCommand(newConfigGetCommand(configScopeGlobal, configPath))
	cmd.AddCommand(newConfigUnsetCommand(configScopeGlobal, configPath))
	cmd.AddCommand(newConfigShowCommand(configScopeGlobal, configPath))
	return cmd
}

func newConfigLocalCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "Manage local repo config",
		Long:  scopedConfigHelpText(configScopeLocal),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigSetCommand(configScopeLocal, configPath))
	cmd.AddCommand(newConfigGetCommand(configScopeLocal, configPath))
	cmd.AddCommand(newConfigUnsetCommand(configScopeLocal, configPath))
	cmd.AddCommand(newConfigShowCommand(configScopeLocal, configPath))
	return cmd
}

func newConfigSetCommand(scope configScope, configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: fmt.Sprintf("Set %s config value", scope),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return usageError(fmt.Sprintf("config %s set expects <key> <value>\nusage: dnsvard config %s set <key> <value>\nkeys: %s", scope, scope, strings.Join(scopeKeys(scope), ", ")))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWritableScopeMutation(writableScopeMutation{
				Scope:      scope,
				ConfigPath: *configPath,
				Key:        args[0],
				Out:        cmd.OutOrStdout(),
				Err:        cmd.ErrOrStderr(),
				Mutate: func(cwd string) (string, bool, error) {
					return setScopedConfigValue(scopedConfigSetRequest{Scope: scope, CWD: cwd, ExplicitPath: *configPath, Key: args[0], RawValue: args[1]})
				},
				UnchangedState: "already set",
				ChangedVerb:    "updated",
			})
		},
	}
	return cmd
}

func newConfigGetCommand(scope configScope, configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [key]",
		Short: fmt.Sprintf("Get %s config value(s)", scope),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageError(fmt.Sprintf("config %s get expects zero or one key", scope))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return printScopedConfigTo(cmd.OutOrStdout(), scope, mustGetwd(), *configPath, args)
		},
	}
	return cmd
}

func newConfigUnsetCommand(scope configScope, configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: fmt.Sprintf("Unset %s config value", scope),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError(fmt.Sprintf("config %s unset expects <key>\nusage: dnsvard config %s unset <key>\nkeys: %s", scope, scope, strings.Join(scopeKeys(scope), ", ")))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWritableScopeMutation(writableScopeMutation{
				Scope:      scope,
				ConfigPath: *configPath,
				Key:        args[0],
				Out:        cmd.OutOrStdout(),
				Err:        cmd.ErrOrStderr(),
				Mutate: func(cwd string) (string, bool, error) {
					return unsetScopedConfigValue(scope, cwd, *configPath, args[0])
				},
				UnchangedState: "already unset",
				ChangedVerb:    "unset",
			})
		},
	}
	return cmd
}

func newConfigShowCommand(scope configScope, configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "show",
		Aliases: []string{"list"},
		Short:   fmt.Sprintf("Show resolved %s config values", scope),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printScopedConfigTo(cmd.OutOrStdout(), scope, mustGetwd(), *configPath, nil)
		},
	}
	return cmd
}

func reloadRuntimeAfterConfigMutation(configPath string, out io.Writer, errOut io.Writer) {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	cfg, _, plat, err := loadRuntime(configPath, true)
	if err != nil {
		fmt.Fprintf(errOut, "warning: config updated but runtime reload skipped: %v\n", err)
		return
	}
	if err := runDaemonRestartTo(out, cfg, plat, configPath, true); err != nil {
		fmt.Fprintf(out, "warning: daemon restart failed; run `dnsvard daemon restart` in this repo: %v\n", err)
	}
	printResolverHelperHintTo(out, plat)
}

func runWritableScopeMutation(req writableScopeMutation) error {
	out := req.Out
	errOut := req.Err
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	path, changed, err := req.Mutate(mustGetwd())
	if err != nil {
		return err
	}
	key := normalizeConfigKey(req.Key)
	if !changed {
		fmt.Fprintf(out, "%s %s %s\n", req.Scope, key, req.UnchangedState)
		return nil
	}
	fmt.Fprintf(out, "%s %s %s in %s\n", req.ChangedVerb, req.Scope, key, userPath(path))
	reloadRuntimeAfterConfigMutation(req.ConfigPath, out, errOut)
	return nil
}

func printScopedConfig(scope configScope, cwd string, explicitPath string, args []string) error {
	return printScopedConfigTo(os.Stdout, scope, cwd, explicitPath, args)
}

func printScopedConfigTo(out io.Writer, scope configScope, cwd string, explicitPath string, args []string) error {
	resolved, defaults, err := resolvedScopeValues(scope, cwd, explicitPath)
	if err != nil {
		return err
	}
	return printConfigValuesTo(out, configPrintRequest{Keys: scopeKeys(scope), Values: resolved, Args: args, Scope: string(scope), Defaults: defaults})
}

func resolvedScopeValues(scope configScope, cwd string, explicitPath string) (map[string]any, map[string]any, error) {
	defaults := scopeDefaultValues(scope)
	if scope == configScopeGlobal {
		path, err := scopeConfigPath(scope, cwd, explicitPath)
		if err != nil {
			return nil, nil, err
		}
		values, _, _, err := readConfigYAMLMap(path)
		if err != nil {
			return nil, nil, err
		}
		resolved := make(map[string]any, len(defaults))
		for key, v := range defaults {
			resolved[key] = v
		}
		for _, key := range scopeKeys(scope) {
			if v, ok := values[key]; ok {
				resolved[key] = v
			}
		}
		return resolved, defaults, nil
	}
	opts := config.LoadOptions{CWD: cwd}
	if strings.TrimSpace(explicitPath) != "" {
		opts.ExplicitPath = explicitPath
	}
	cfg, err := config.Load(opts)
	if err != nil {
		return nil, nil, err
	}
	resolved := map[string]any{
		"suffix":       cfg.Domain,
		"host_pattern": cfg.HostPattern,
	}
	return resolved, defaults, nil
}

func scopeDefaultValues(scope configScope) map[string]any {
	defaults := config.Default()
	if scope == configScopeGlobal {
		return map[string]any{
			"suffix":                defaults.Domain,
			"host_pattern":          defaults.HostPattern,
			"loopback_cidr":         defaults.LoopbackCIDR,
			"dns_listen":            defaults.DNSListen,
			"dns_ttl":               defaults.DNSTTL,
			"http_port":             defaults.HTTPPort,
			"state_dir":             defaults.StateDir,
			"log_level":             defaults.LogLevel,
			"docker_discovery_mode": defaults.DockerDiscoveryMode,
		}
	}
	return map[string]any{
		"suffix":       defaults.Domain,
		"host_pattern": defaults.HostPattern,
	}
}

func printConfigValues(req configPrintRequest) error {
	return printConfigValuesTo(os.Stdout, req)
}

func printConfigValuesTo(out io.Writer, req configPrintRequest) error {
	if len(req.Args) == 1 {
		key := normalizeConfigKey(req.Args[0])
		if !containsKey(req.Keys, key) {
			return usageError(fmt.Sprintf("config %s key %q is unsupported\nkeys: %s", req.Scope, key, strings.Join(req.Keys, ", ")))
		}
		printConfigValueLineTo(out, key, req.Values[key], configValuesEquivalent(req.Values[key], req.Defaults[key]))
		return nil
	}
	for _, key := range req.Keys {
		printConfigValueLineTo(out, key, req.Values[key], configValuesEquivalent(req.Values[key], req.Defaults[key]))
	}
	return nil
}

func printConfigValueLine(key string, value any, defaultValue bool) {
	printConfigValueLineTo(os.Stdout, key, value, defaultValue)
}

func printConfigValueLineTo(out io.Writer, key string, value any, defaultValue bool) {
	line := fmt.Sprintf("%s=%s", key, renderConfigValue(value))
	if defaultValue {
		line += " (default)"
	}
	fmt.Fprintln(out, line)
}

func scopedConfigHelpText(scope configScope) string {
	return fmt.Sprintf("Manage %s config.\n\nSettable keys:\n%s\n\nUse `dnsvard config %s show` to show resolved values.", scope, scopeKeyCatalog(scope), scope)
}

func scopeKeyCatalog(scope configScope) string {
	lines := make([]string, 0, len(scopeKeys(scope)))
	for _, key := range scopeKeys(scope) {
		desc, ok := configKeyDescriptors[key]
		if !ok {
			lines = append(lines, fmt.Sprintf("- %s", key))
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (type: %s, example: %s)", key, desc.ValueType, desc.Example))
	}
	return strings.Join(lines, "\n")
}

func setScopedConfigValue(req scopedConfigSetRequest) (string, bool, error) {
	return mutateScopedConfigValue(scopedConfigMutationRequest{Scope: req.Scope, CWD: req.CWD, ExplicitPath: req.ExplicitPath, Key: req.Key, Mutator: func(values map[string]any, normalizedKey string) (bool, error) {
		value, err := parseConfigValue(normalizedKey, req.RawValue)
		if err != nil {
			return false, err
		}
		if v, ok := values[normalizedKey]; ok && configValuesEquivalent(v, value) {
			return false, nil
		}
		values[normalizedKey] = value
		return true, nil
	}})
}

func unsetScopedConfigValue(scope configScope, cwd string, explicitPath string, key string) (string, bool, error) {
	return mutateScopedConfigValue(scopedConfigMutationRequest{Scope: scope, CWD: cwd, ExplicitPath: explicitPath, Key: key, Mutator: func(values map[string]any, normalizedKey string) (bool, error) {
		if _, ok := values[normalizedKey]; !ok {
			return false, nil
		}
		delete(values, normalizedKey)
		return true, nil
	}})
}

func mutateScopedConfigValue(req scopedConfigMutationRequest) (string, bool, error) {
	key, err := validateScopeKey(req.Scope, req.Key)
	if err != nil {
		return "", false, err
	}
	path, err := scopeConfigPath(req.Scope, req.CWD, req.ExplicitPath)
	if err != nil {
		return "", false, err
	}
	values, original, existed, err := readConfigYAMLMap(path)
	if err != nil {
		return path, false, err
	}
	changed, err := req.Mutator(values, key)
	if err != nil {
		return path, false, err
	}
	if !changed {
		return path, false, nil
	}
	if err := writeConfigYAMLMap(path, values); err != nil {
		return path, false, err
	}
	if err := maybeChownGlobalConfig(req.Scope, path); err != nil {
		return path, false, err
	}
	if err := validateScopedConfig(req.Scope, req.CWD, req.ExplicitPath); err != nil {
		_ = restoreConfigYAMLMap(path, original, existed)
		_ = maybeChownGlobalConfig(req.Scope, path)
		return path, false, err
	}
	return path, true, nil
}

func maybeChownGlobalConfig(scope configScope, path string) error {
	if scope != configScopeGlobal {
		return nil
	}
	return ownership.ChownPathAndParentToSudoInvoker(path)
}

func validateScopedConfig(scope configScope, cwd string, explicitPath string) error {
	if scope == configScopeGlobal {
		tmp, err := os.MkdirTemp("", "dnsvard-config-global-validate-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		_, err = config.Load(config.LoadOptions{CWD: tmp})
		return err
	}
	opts := config.LoadOptions{CWD: cwd}
	if strings.TrimSpace(explicitPath) != "" {
		opts.ExplicitPath = explicitPath
	}
	_, err := config.Load(opts)
	return err
}

func scopeConfigPath(scope configScope, cwd string, explicitPath string) (string, error) {
	switch scope {
	case configScopeGlobal:
		return config.GlobalConfigPath(), nil
	case configScopeLocal:
		return config.LocalConfigPath(cwd, explicitPath), nil
	default:
		return "", fmt.Errorf("scope %q does not map to a config file", scope)
	}
}

func readConfigYAMLMap(path string) (map[string]any, []byte, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("read config %s: %w", path, err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return map[string]any{}, b, true, nil
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, nil, false, fmt.Errorf("parse config %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, b, true, nil
}

func writeConfigYAMLMap(path string, values map[string]any) error {
	if values == nil {
		values = map[string]any{}
	}
	out, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode config %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func restoreConfigYAMLMap(path string, original []byte, existed bool) error {
	if existed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, original, 0o644)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateScopeKey(scope configScope, key string) (string, error) {
	key = normalizeConfigKey(key)
	if containsKey(scopeKeys(scope), key) {
		return key, nil
	}
	return "", usageError(fmt.Sprintf("config %s key %q is unsupported\nkeys: %s", scope, key, strings.Join(scopeKeys(scope), ", ")))
}

func scopeKeys(scope configScope) []string {
	switch scope {
	case configScopeGlobal:
		return globalConfigKeys
	case configScopeLocal:
		return localConfigKeys
	default:
		return nil
	}
}

func containsKey(keys []string, key string) bool {
	for _, v := range keys {
		if v == key {
			return true
		}
	}
	return false
}

func normalizeConfigKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func parseConfigValue(key string, raw string) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, usageError(fmt.Sprintf("config key %q requires a non-empty value", key))
	}

	switch key {
	case "suffix":
		return strings.ToLower(strings.Trim(value, ".")), nil
	case "host_pattern":
		pattern := strings.ToLower(value)
		if _, err := identity.ParseHostPattern(pattern); err != nil {
			return nil, usageError(fmt.Sprintf("%v\nallowed patterns:\n- %s", err, strings.Join(exampleHostPatterns(), "\n- ")))
		}
		return pattern, nil
	case "loopback_cidr", "dns_listen", "state_dir":
		return value, nil
	case "dns_ttl":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return nil, usageError("dns_ttl must be an integer > 0")
		}
		if n > int(^uint32(0)) {
			return nil, usageError("dns_ttl exceeds uint32 range")
		}
		return uint32(n), nil
	case "http_port":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 65535 {
			return nil, usageError("http_port must be an integer between 1 and 65535")
		}
		return n, nil
	case "log_level":
		return strings.ToLower(value), nil
	case "docker_discovery_mode":
		mode := strings.ToLower(value)
		if mode != config.DockerDiscoveryModeRequired && mode != config.DockerDiscoveryModeOptional {
			return nil, usageError(fmt.Sprintf("docker_discovery_mode must be %q or %q", config.DockerDiscoveryModeRequired, config.DockerDiscoveryModeOptional))
		}
		return mode, nil
	default:
		return nil, usageError(fmt.Sprintf("unsupported config key %q", key))
	}
}

func configValuesEquivalent(current any, next any) bool {
	return normalizeRenderedConfigValue(current) == normalizeRenderedConfigValue(next)
}

func renderConfigValue(v any) string {
	return normalizeRenderedConfigValue(v)
}

func normalizeRenderedConfigValue(v any) string {
	switch n := v.(type) {
	case nil:
		return "<nil>"
	case string:
		return n
	default:
		b, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return strings.TrimSpace(string(b))
	}
}

func printResolverHelperHint(plat interface{ LoopbackStatus() (string, error) }) {
	printResolverHelperHintTo(os.Stdout, plat)
}

func printResolverHelperHintTo(out io.Writer, plat interface{ LoopbackStatus() (string, error) }) {
	status, err := plat.LoopbackStatus()
	if err != nil {
		fmt.Fprintf(out, "note: root helper not detected; run `sudo dnsvard bootstrap --force` once to enable automatic /etc/resolver reconciliation\n")
		return
	}
	if !loopbackHasResolverSync(status) {
		fmt.Fprintf(out, "note: root helper is running without resolver sync support; run `sudo dnsvard bootstrap --force`\n")
	}
}

func exampleHostPatterns() []string {
	patterns := []string{fmt.Sprintf("%s (default)", identity.DefaultHostPattern)}
	for _, candidate := range []string{"workspace-tld", "workspace-project-tld", "service-workspace-project-tld"} {
		if candidate == identity.DefaultHostPattern {
			continue
		}
		patterns = append(patterns, candidate)
	}
	return patterns
}
