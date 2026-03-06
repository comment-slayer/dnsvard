package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/logx"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

const bootstrapStateVersion = 2

var version = ""

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "dnsvard: %s\n", userFacingError(err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := newRootCommand(ctx)
	return root.Execute()
}

func newRootCommand(ctx context.Context) *cobra.Command {
	configPath := ""
	appVersion := resolvedVersion()

	root := &cobra.Command{
		Use:           "dnsvard",
		Short:         "Local workspace DNS and routing daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.Version = appVersion
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file")

	root.AddCommand(newBootstrapCommand(ctx, &configPath))
	root.AddCommand(newUninstallCommand(ctx, &configPath))
	root.AddCommand(newDoctorCommand(ctx, &configPath))
	root.AddCommand(newEnvCommand(ctx, &configPath))
	root.AddCommand(newConfigCommand(ctx, &configPath))
	root.AddCommand(newDaemonCommand(ctx, &configPath))
	root.AddCommand(newPSCommand(ctx, &configPath))
	root.AddCommand(newStopCommand(ctx, &configPath))
	root.AddCommand(newKillCommand(ctx, &configPath))
	root.AddCommand(newRemoveCommand(ctx, &configPath))
	root.AddCommand(newRunCommand(ctx, &configPath))
	root.AddCommand(newCompletionCommand(root))
	root.AddCommand(newUpgradeCommand(ctx, appVersion))
	root.AddCommand(newVersionCommand(appVersion))

	return root
}

func newVersionCommand(appVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print dnsvard version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), appVersion)
		},
	}
}

func resolvedVersion() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(bi.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

func loadRuntime(configPath string, autoHealDomain bool) (config.Config, *logx.Logger, platform.Controller, error) {
	_ = autoHealDomain
	cfg, err := config.Load(config.LoadOptions{CWD: mustGetwd(), ExplicitPath: configPath})
	if err != nil {
		return config.Config{}, nil, nil, err
	}

	plat := platform.New()
	if err := plat.ValidateConfig(cfg); err != nil {
		return config.Config{}, nil, nil, err
	}

	logger, err := logx.New(cfg.LogLevel)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	return cfg, logger, plat, nil
}

func loadRuntimeForUninstall(configPath string) (config.Config, *logx.Logger, platform.Controller, error) {
	cfg, err := config.Load(config.LoadOptions{CWD: mustGetwd(), ExplicitPath: configPath, SkipGlobalInit: true})
	if err != nil {
		return config.Config{}, nil, nil, err
	}

	plat := platform.New()
	if err := plat.ValidateConfig(cfg); err != nil {
		return config.Config{}, nil, nil, err
	}

	logger, err := logx.New(cfg.LogLevel)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	return cfg, logger, plat, nil
}

func maybeHandleResolverSyncStatePermissionError(cfg config.Config, err error) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return err
	}
	if os.Geteuid() == 0 {
		return err
	}

	statePath := resolverSyncStatePath(cfg.StateDir)
	userName := strings.TrimSpace(os.Getenv("USER"))
	if userName == "" {
		userName = "<your-user>"
	}

	fmt.Fprintf(os.Stderr, "warning: resolver sync state is not writable: %s\n", statePath)
	fmt.Fprintf(os.Stderr, "warning: dnsvard will continue, but resolver auto-sync updates may be stale\n")
	fmt.Fprintf(os.Stderr, "fix: sudo chown -R %s %q\n", userName, cfg.StateDir)
	fmt.Fprintf(os.Stderr, "or: run `sudo dnsvard bootstrap --force`\n")
	return nil
}

func newBootstrapCommand(ctx context.Context, configPath *string) *cobra.Command {
	force := false
	quick := false
	asUser := ""
	verbose := false
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Install and reconcile dnsvard system setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, plat, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runBootstrapTo(cmd.OutOrStdout(), cmd.ErrOrStderr(), bootstrapRunOptions{
				Logger:     nil,
				Cfg:        cfg,
				Platform:   plat,
				ConfigPath: *configPath,
				Force:      force,
				Quick:      quick,
				AsUser:     asUser,
				Verbose:    verbose,
			})
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force reinstall/restart even when healthy")
	cmd.Flags().BoolVar(&quick, "quick", false, "Prefer fast reconcile path; falls back to full bootstrap when required")
	cmd.Flags().StringVar(&asUser, "as-user", "", "Linux only: target user for user-daemon pass when running as root")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show detailed bootstrap diagnostics")
	return cmd
}

func newUninstallCommand(ctx context.Context, configPath *string) *cobra.Command {
	removeBinary := false
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove dnsvard platform setup and managed resolvers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, plat, err := loadRuntimeForUninstall(*configPath)
			if err != nil {
				return err
			}
			return runUninstallTo(cmd.OutOrStdout(), nil, cfg, plat, removeBinary)
		},
	}
	cmd.Flags().BoolVar(&removeBinary, "remove", false, "Also remove the current dnsvard binary path")
	cmd.Flags().BoolVar(&removeBinary, "delete", false, "Alias for --remove")
	return cmd
}

func newDoctorCommand(ctx context.Context, configPath *string) *cobra.Command {
	flushCache := false
	checkLocalNetwork := false
	probeRouting := false
	jsonOutput := false
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Inspect dnsvard setup and auto-heal status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, plat, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runDoctor(doctorRunOptions{
				Cfg:               cfg,
				Platform:          plat,
				FlushCache:        flushCache,
				CheckLocalNetwork: checkLocalNetwork,
				ProbeRouting:      probeRouting,
				JSONOutput:        jsonOutput,
			})
		},
	}
	cmd.Flags().BoolVar(&flushCache, "flush-cache", false, "Flush local DNS cache (macOS)")
	cmd.Flags().BoolVar(&checkLocalNetwork, "check-local-network", false, "Actively probe macOS Local Network permission (adds small delay)")
	cmd.Flags().BoolVar(&probeRouting, "probe-routing", false, "Probe effective DNS and HTTP routing for current workspace")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable diagnostics JSON")
	return cmd
}

func newEnvCommand(ctx context.Context, configPath *string) *cobra.Command {
	shell := true
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print environment variables for current workspace",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, logger, _, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runEnv(ctx, logger, cfg, shell)
		},
	}
	cmd.Flags().BoolVar(&shell, "shell", true, "Print shell export lines")
	return cmd
}

func newDaemonCommand(ctx context.Context, configPath *string) *cobra.Command {
	daemonCmd := &cobra.Command{Use: "daemon", Short: "Manage dnsvard daemon processes"}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start daemon in background or foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			foreground, _ := cmd.Flags().GetBool("foreground")
			cfg, logger, _, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			_ = ctx
			return runDaemonStartTo(cmd.OutOrStdout(), cmd.ErrOrStderr(), logger, cfg, *configPath, foreground)
		},
	}
	startCmd.Flags().Bool("foreground", false, "Run daemon in foreground")
	daemonCmd.AddCommand(startCmd)

	daemonCmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop running daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, plat, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runDaemonStopTo(cmd.OutOrStdout(), cfg, plat)
		},
	})

	daemonCmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, plat, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runDaemonRestartTo(cmd.OutOrStdout(), cfg, plat, *configPath, false)
		},
	})

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon and helper status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose, _ := cmd.Flags().GetBool("verbose")
			cfg, _, plat, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runDaemonStatusTo(cmd.OutOrStdout(), cfg, plat, verbose)
		},
	}
	statusCmd.Flags().Bool("verbose", false, "Show platform daemon helper details")
	daemonCmd.AddCommand(statusCmd)

	clearLogs := false
	daemonLogsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Show recent daemon logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, plat, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runDaemonLogsTo(cmd.OutOrStdout(), cfg, plat, clearLogs)
		},
	}
	daemonLogsCmd.Flags().BoolVar(&clearLogs, "clear", false, "Clear daemon log file before showing logs")
	daemonCmd.AddCommand(daemonLogsCmd)

	loopbackState := ""
	loopbackResolverState := ""
	loopbackCIDR := "127.90.0.0/16"
	loopbackSyncCmd := &cobra.Command{
		Use:    "loopback-sync",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runLoopbackSync(loopbackState, loopbackResolverState, loopbackCIDR)
		},
	}
	loopbackSyncCmd.Flags().StringVar(&loopbackState, "state-file", "", "Path to allocator state file")
	loopbackSyncCmd.Flags().StringVar(&loopbackResolverState, "resolver-state-file", "", "Path to resolver sync state file")
	loopbackSyncCmd.Flags().StringVar(&loopbackCIDR, "cidr", "127.90.0.0/16", "Loopback CIDR to manage")
	daemonCmd.AddCommand(loopbackSyncCmd)

	return daemonCmd
}

func newRunCommand(ctx context.Context, configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "run [service] [--vite|--next|--nuxt|--astro|--svelte|--webpack|--adapter <name>] -- <cmd...>",
		Short:              "Run a local process with dnsvard runtime routing",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runHelpRequested(args) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s", runCommandHelpText())
				return nil
			}
			cfg, logger, _, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			return runRun(ctx, logger, cfg, args)
		},
	}
	return cmd
}

func runHelpRequested(args []string) bool {
	separator := len(args)
	for i, a := range args {
		if a == "--" {
			separator = i
			break
		}
	}
	for i := 0; i < separator; i++ {
		if args[i] == "-h" || args[i] == "--help" {
			return true
		}
	}
	return false
}

func runCommandHelpText() string {
	adapters := strings.Join(listRunAdapterNames(), "|")
	return fmt.Sprintf(`Run a local process with dnsvard runtime routing.

Usage:
  dnsvard run [service] [--vite|--next|--nuxt|--astro|--svelte|--webpack|--adapter <name>] -- <cmd...>

Examples:
  dnsvard run -- bun dev
  dnsvard run api -- bun dev
  dnsvard run --adapter vite -- bun dev
  dnsvard run --next -- npm run dev
  dnsvard run --adapter webpack -- npm run dev

Adapter behavior:
  - If adapter flags are omitted, dnsvard attempts auto-detection from command tokens and package.json scripts/dependencies.
  - If auto-detection is not possible for a likely frontend dev command, dnsvard fails fast with explicit adapter guidance.
  - Known adapters: %s
`, adapters)
}

func configHomeDir() string {
	if os.Geteuid() == 0 {
		if sudoHome := strings.TrimSpace(os.Getenv("SUDO_HOME")); sudoHome != "" {
			return sudoHome
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

func userPath(path string) string {
	home := strings.TrimSpace(configHomeDir())
	if home == "" || home == "." {
		return path
	}
	cleanHome := strings.TrimRight(home, string(os.PathSeparator))
	if path == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(os.PathSeparator)
	if strings.HasPrefix(path, prefix) {
		return "~" + string(os.PathSeparator) + strings.TrimPrefix(path, prefix)
	}
	return path
}

func userFacingError(err error) string {
	if err == nil {
		return ""
	}
	return userFacingPathText(err.Error())
}

func userFacingPathText(text string) string {
	home := strings.TrimSpace(configHomeDir())
	if home == "" || home == "." {
		return text
	}
	cleanHome := strings.TrimRight(home, string(os.PathSeparator))
	if cleanHome == "" {
		return text
	}
	prefix := cleanHome + string(os.PathSeparator)
	text = strings.ReplaceAll(text, prefix, "~"+string(os.PathSeparator))
	text = strings.ReplaceAll(text, cleanHome, "~")
	return text
}

func loopbackHasResolverSync(status string) bool {
	return strings.Contains(status, "--resolver-state-file")
}

func usageError(msg string) error {
	return errors.New(msg)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
