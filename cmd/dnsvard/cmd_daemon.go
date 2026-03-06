package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/comment-slayer/dnsvard/internal/allocator"
	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/dnsserver"
	"github.com/comment-slayer/dnsvard/internal/docker"
	"github.com/comment-slayer/dnsvard/internal/httprouter"
	"github.com/comment-slayer/dnsvard/internal/identity"
	"github.com/comment-slayer/dnsvard/internal/logx"
	"github.com/comment-slayer/dnsvard/internal/netutil"
	"github.com/comment-slayer/dnsvard/internal/platform"
	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
	"github.com/comment-slayer/dnsvard/internal/tcpproxy"
)

func runDaemonStart(logger *logx.Logger, cfg config.Config, configPath string, foreground bool) error {
	return runDaemonStartTo(os.Stdout, os.Stderr, logger, cfg, configPath, foreground)
}

func runDaemonStartTo(out io.Writer, errOut io.Writer, logger *logx.Logger, cfg config.Config, configPath string, foreground bool) error {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	if err := stopStaleDaemonProcesses(cfg, platform.New()); err != nil {
		fmt.Fprintf(errOut, "warning: stale daemon cleanup before start failed: %v\n", err)
	}
	if foreground {
		return runDaemonForegroundTo(out, errOut, logger, cfg, configPath)
	}
	return runDaemonBackgroundTo(out, cfg, configPath, false)
}

func runDaemonStop(cfg config.Config, plat platform.Controller) error {
	return runDaemonStopTo(os.Stdout, cfg, plat)
}

func runDaemonStopTo(out io.Writer, cfg config.Config, plat platform.Controller) error {
	if out == nil {
		out = os.Stdout
	}
	_ = plat.StopDaemon()
	pid, err := daemon.ReadPID(cfg.StateDir)
	if err != nil {
		if stopErr := stopStaleDaemonProcesses(cfg, plat); stopErr != nil {
			return fmt.Errorf("daemon is not running (pid file missing in %s) and stale cleanup failed: %w", cfg.StateDir, stopErr)
		}
		fmt.Fprintf(out, "daemon stopped (pid file missing; stale processes cleaned)\n")
		return nil
	}
	if err := killPID(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop daemon process %d: %w", pid, err)
	}
	if stopErr := stopStaleDaemonProcesses(cfg, plat); stopErr != nil {
		return fmt.Errorf("stop daemon process %d: %w", pid, stopErr)
	}
	fmt.Fprintf(out, "daemon stop signal sent to pid %d\n", pid)
	return nil
}

func runDaemonRestart(cfg config.Config, plat platform.Controller, configPath string, quiet bool) error {
	return runDaemonRestartTo(os.Stdout, cfg, plat, configPath, quiet)
}

func runDaemonRestartTo(out io.Writer, cfg config.Config, plat platform.Controller, configPath string, quiet bool) error {
	if out == nil {
		out = os.Stdout
	}
	deps := daemonRestartDeps{
		readPID:        daemon.ReadPID,
		processRunning: daemon.ProcessRunning,
		killPID: func(pid int) error {
			return killPID(pid, syscall.SIGTERM)
		},
		sleep:         time.Sleep,
		runBackground: runDaemonBackground,
		waitForPID:    waitForDaemonPID,
	}
	return runDaemonRestartWithDeps(daemonRestartRequest{
		Cfg:        cfg,
		ConfigPath: configPath,
		Quiet:      quiet,
		Out:        out,
		Deps:       deps,
		ManagedRestart: func() (bool, error) {
			return tryManagedDaemonRestart(managedDaemonRestartRequest{
				Cfg:            cfg,
				Installer:      plat,
				ConfigPath:     configPath,
				Quiet:          quiet,
				Out:            out,
				ExecutablePath: os.Executable,
				WaitForPID:     waitForDaemonPID,
			})
		},
	})
}

type daemonRestartDeps struct {
	readPID        func(string) (int, error)
	processRunning func(int) bool
	killPID        func(int) error
	sleep          func(time.Duration)
	runBackground  func(config.Config, string, bool) error
	waitForPID     func(string, time.Duration) bool
}

type daemonProcessState struct {
	pid     int
	running bool
}

type daemonRestartRequest struct {
	Cfg            config.Config
	ConfigPath     string
	Quiet          bool
	Out            io.Writer
	Deps           daemonRestartDeps
	ManagedRestart func() (bool, error)
}

func runDaemonRestartWithDeps(req daemonRestartRequest) error {
	cfg := req.Cfg
	configPath := req.ConfigPath
	quiet := req.Quiet
	out := req.Out
	if out == nil {
		out = os.Stdout
	}
	deps := req.Deps
	managedRestart := req.ManagedRestart

	before := readDaemonProcessState(cfg.StateDir, deps)
	restartedManaged, managedErr := managedRestart()
	if managedErr != nil {
		fmt.Fprintf(out, "warning: managed daemon restart failed; falling back to direct process restart: %v\n", managedErr)
	}
	if restartedManaged {
		return nil
	}
	after := readDaemonProcessState(cfg.StateDir, deps)
	killPID, skipDirectRestart := directRestartPlan(before, after)
	if skipDirectRestart {
		if !quiet {
			fmt.Fprintln(out, "daemon restarted")
		}
		return nil
	}
	if killPID > 0 {
		_ = deps.killPID(killPID)
		for range 20 {
			if !deps.processRunning(killPID) {
				break
			}
			deps.sleep(100 * time.Millisecond)
		}
	}
	if err := deps.runBackground(cfg, configPath, quiet); err != nil {
		if isDaemonAlreadyRunningErr(err) && deps.waitForPID(cfg.StateDir, 2*time.Second) {
			if !quiet {
				fmt.Fprintln(out, "daemon restarted")
			}
			return nil
		}
		return err
	}
	if !deps.waitForPID(cfg.StateDir, 2*time.Second) {
		return errors.New("daemon restart did not result in a running daemon; run `dnsvard daemon logs`")
	}
	if !quiet {
		fmt.Fprintln(out, "daemon restarted")
	}
	return nil
}

func readDaemonProcessState(stateDir string, deps daemonRestartDeps) daemonProcessState {
	pid, err := deps.readPID(stateDir)
	if err != nil {
		return daemonProcessState{}
	}
	if !deps.processRunning(pid) {
		return daemonProcessState{}
	}
	return daemonProcessState{pid: pid, running: true}
}

func directRestartPlan(before daemonProcessState, after daemonProcessState) (int, bool) {
	if after.running && (!before.running || after.pid != before.pid) {
		return 0, true
	}
	if before.running && after.running && before.pid == after.pid {
		return after.pid, false
	}
	return 0, false
}

func isDaemonAlreadyRunningErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "daemon already running")
}

func stopStaleDaemonProcesses(_ config.Config, _ platform.Controller) error {
	return stopStaleDaemonProcessesWith(stopAnyForegroundDaemons)
}

func stopStaleDaemonProcessesWith(stopForeground func() error) error {
	if err := stopForeground(); err != nil {
		return err
	}
	return nil
}

type daemonInstaller interface {
	InstallDaemon(spec platform.DaemonSpec) (string, error)
}

type healActionID string
type healDetector string

const (
	healProxyReconcile         healActionID = "proxy_reconcile"
	healHTTPRouterReset        healActionID = "http_router_reset"
	healDockerWatchRestart     healActionID = "docker_watch_restart"
	healPlatformDaemonRepair   healActionID = "platform_daemon_repair"
	healResolverDriftReconcile healActionID = "resolver_drift_reconcile"
	healHTTPRoutePromotion     healActionID = "http_route_promotion"
)

const (
	healDetectorHTTPProxy   healDetector = "http_proxy"
	healDetectorDockerWatch healDetector = "docker_watch"
	healDetectorTimer       healDetector = "timer"
)

type healActionSpec struct {
	component string
	detector  healDetector
	trigger   string
	action    string
	cooldown  time.Duration
}

type healEvent struct {
	detector healDetector
	actionID healActionID
	detail   string
}

var daemonHealMatrix = map[healActionID]healActionSpec{
	healProxyReconcile: {
		component: "route_reconcile",
		detector:  healDetectorHTTPProxy,
		trigger:   "http_proxy_error",
		action:    "reconcile",
		cooldown:  0,
	},
	healHTTPRouterReset: {
		component: "http_router",
		detector:  healDetectorHTTPProxy,
		trigger:   "http_proxy_error",
		action:    "http_router_reset",
		cooldown:  2 * time.Minute,
	},
	healDockerWatchRestart: {
		component: "docker_watch",
		detector:  healDetectorDockerWatch,
		trigger:   "docker_watch",
		action:    "restart",
		cooldown:  5 * time.Second,
	},
	healPlatformDaemonRepair: {
		component: "platform_daemon",
		detector:  healDetectorTimer,
		trigger:   "platform_daemon",
		action:    "auto_repair",
		cooldown:  2 * time.Minute,
	},
	healResolverDriftReconcile: {
		component: "resolver_drift",
		detector:  healDetectorTimer,
		trigger:   "resolver_drift",
		action:    "ensure_resolver",
		cooldown:  30 * time.Second,
	},
	healHTTPRoutePromotion: {
		component: "http_router",
		detector:  healDetectorTimer,
		trigger:   "http_route_promotion",
		action:    "fallback_previous_target",
		cooldown:  0,
	},
}

type healCoordinator struct {
	mu   sync.Mutex
	last map[healActionID]time.Time
}

func newHealCoordinator() *healCoordinator {
	return &healCoordinator{last: map[healActionID]time.Time{}}
}

func (h *healCoordinator) emit(event healEvent, markSelfHeal func(healActionSpec, healEvent)) bool {
	spec, ok := daemonHealMatrix[event.actionID]
	if !ok {
		return false
	}
	if spec.detector != "" && event.detector != "" && spec.detector != event.detector {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	lastRun := h.last[event.actionID]
	if spec.cooldown > 0 && !lastRun.IsZero() && now.Sub(lastRun) < spec.cooldown {
		h.mu.Unlock()
		return false
	}
	h.last[event.actionID] = now
	h.mu.Unlock()
	markSelfHeal(spec, event)
	_ = spec.component
	return true
}

type managedDaemonRestartRequest struct {
	Cfg            config.Config
	Installer      daemonInstaller
	ConfigPath     string
	Quiet          bool
	Out            io.Writer
	ExecutablePath func() (string, error)
	WaitForPID     func(string, time.Duration) bool
}

func tryManagedDaemonRestart(req managedDaemonRestartRequest) (bool, error) {
	cfg := req.Cfg
	installer := req.Installer
	configPath := req.ConfigPath
	quiet := req.Quiet
	out := req.Out
	if out == nil {
		out = os.Stdout
	}
	executablePath := req.ExecutablePath
	waitFn := req.WaitForPID

	if pid, err := daemon.ReadPID(cfg.StateDir); err == nil && daemon.ProcessRunning(pid) {
		_ = killPID(pid, syscall.SIGTERM)
		for range 20 {
			if !daemon.ProcessRunning(pid) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	binPath, err := executablePath()
	if err != nil {
		return false, err
	}
	if isGoRunExecutable(binPath) {
		return false, fmt.Errorf("managed restart disabled for go-run executable %s", userPath(binPath))
	}
	_, installErr := installer.InstallDaemon(platform.DaemonSpec{
		BinaryPath: binPath,
		ConfigPath: configPath,
		StateDir:   cfg.StateDir,
		WorkingDir: cfg.Workspace.Path,
	})
	if installErr != nil {
		return false, installErr
	}
	if waitFn != nil && !waitFn(cfg.StateDir, 2*time.Second) {
		return false, errors.New("managed restart did not result in a running daemon")
	}
	if !quiet {
		fmt.Fprintln(out, "daemon restarted")
	}
	return true, nil
}

func runDaemonStatus(cfg config.Config, plat platform.Controller, verbose bool) error {
	return runDaemonStatusTo(os.Stdout, cfg, plat, verbose)
}

func runDaemonStatusTo(out io.Writer, cfg config.Config, plat platform.Controller, verbose bool) error {
	if out == nil {
		out = os.Stdout
	}
	status, statusErr := "stopped", ""
	fmt.Fprintf(out, "configured suffix: %s\n", cfg.Domain)
	fmt.Fprintf(out, "managed suffixes: %s\n", strings.Join(cfg.Domains, ","))
	pid, err := daemon.ReadPID(cfg.StateDir)
	launchLoaded := false
	launchDegraded := false
	if err == nil && daemon.ProcessRunning(pid) {
		status = "running"
		fmt.Fprintf(out, "daemon status: %s (pid=%d)\n", status, pid)
	} else {
		if err != nil {
			statusErr = err.Error()
		}
		fmt.Fprintf(out, "daemon status: %s\n", status)
	}
	if launchStatus, err := plat.DaemonStatus(); err == nil {
		launchLoaded = true
		if plat.DaemonStatusDegraded(launchStatus) {
			launchDegraded = true
		}
		fmt.Fprintln(out, "platform-daemon status: loaded")
		if verbose {
			for _, line := range plat.DaemonStatusDetails(cfg.StateDir, launchStatus, launchDegraded) {
				fmt.Fprintf(out, "- %s\n", line)
			}
		}
	} else {
		fmt.Fprintf(out, "platform-daemon status: %s\n", err)
	}
	if launchLoaded && launchDegraded && status == "running" {
		fmt.Fprintln(out, "daemon_management: degraded (platform daemon manager is retrying while a daemon is already running; run `dnsvard daemon stop` then `dnsvard daemon restart`)")
	}
	loopbackLoaded := false
	loopbackResolverSync := false
	if loopStatus, err := plat.LoopbackStatus(); err == nil {
		loopbackLoaded = true
		loopbackResolverSync = loopbackHasResolverSync(loopStatus)
		fmt.Fprintln(out, "platform-loopback status: loaded")
		if verbose {
			fmt.Fprintf(out, "- loopback resolver sync: %t\n", loopbackResolverSync)
			for _, line := range plat.LoopbackStatusDetails(cfg.StateDir, loopStatus) {
				fmt.Fprintf(out, "- %s\n", line)
			}
		}
	} else {
		fmt.Fprintf(out, "platform-loopback status: %s\n", err)
	}
	autoHeal, autoHealDetail := resolverAutoHealStatus(cfg, plat, loopbackLoaded, loopbackResolverSync)
	fmt.Fprintf(out, "resolver_auto_heal: %s (%s)\n", autoHeal, autoHealDetail)
	if verbose {
		state, recErr := daemon.ReadReconcileState(cfg.StateDir)
		if recErr != nil {
			fmt.Fprintf(out, "- last reconcile: unavailable (%v)\n", recErr)
		} else {
			summary, stale, reason := formatReconcileStateSummary(state, pid)
			if stale {
				fmt.Fprintf(out, "- last reconcile: %s (stale: %s)\n", summary, reason)
			} else {
				fmt.Fprintf(out, "- last reconcile: %s\n", summary)
			}
			fmt.Fprintf(out, "- reconcile: sequence=%d cause=%s result=%s updated_at=%s\n", state.Sequence, state.Cause, state.Result, state.UpdatedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "- docker_watch: running=%t restarts=%d last_start=%s last_event=%s\n", state.DockerWatchRunning, state.DockerWatchRestartCount, state.DockerWatchLastStartAt.Format(time.RFC3339), state.DockerWatchLastEventAt.Format(time.RFC3339))
			if state.DockerWatchLastError != "" {
				fmt.Fprintf(out, "- docker_watch_error: %s\n", state.DockerWatchLastError)
			}
			if state.SelfHealTrigger != "" || state.SelfHealAction != "" || state.SelfHealDetail != "" {
				fmt.Fprintf(out, "- self_heal: trigger=%s action=%s detail=%s\n", state.SelfHealTrigger, state.SelfHealAction, state.SelfHealDetail)
				if state.SelfHealDetector != "" || state.SelfHealComponent != "" || state.SelfHealActionID != "" {
					fmt.Fprintf(out, "- self_heal_meta: detector=%s component=%s action_id=%s\n", state.SelfHealDetector, state.SelfHealComponent, state.SelfHealActionID)
				}
			}
			for _, problem := range selfHealProblemsFromState(state, cfg, plat) {
				for _, line := range selfHealProblemLines(problem) {
					fmt.Fprintf(out, "%s\n", line)
				}
			}
		}
	}
	if statusErr != "" {
		fmt.Fprintf(out, "details: %s\n", statusErr)
	}
	return nil
}

func resolverAutoHealStatus(cfg config.Config, plat platform.Controller, loopbackLoaded bool, loopbackResolverSync bool) (string, string) {
	desired := map[string]struct{}{}
	for _, suffix := range cfg.Domains {
		n := strings.ToLower(strings.Trim(strings.TrimSpace(suffix), "."))
		if n == "" {
			continue
		}
		desired[n] = struct{}{}
	}
	if len(desired) == 0 {
		return "unknown", "no desired managed suffixes"
	}

	ready := true
	for suffix := range desired {
		spec, err := resolverSpecFromListen(cfg, suffix)
		if err != nil {
			return "blocked", resolverStatusFailureDetail(err)
		}
		match, err := plat.ResolverMatches(spec)
		if err != nil {
			return "blocked", resolverStatusFailureDetail(err)
		}
		if !match {
			ready = false
			break
		}
	}

	managed, err := plat.ListManagedResolvers()
	if err != nil {
		return "blocked", resolverStatusFailureDetail(err)
	}
	managedSet := map[string]struct{}{}
	for _, suffix := range managed {
		n := strings.ToLower(strings.Trim(strings.TrimSpace(suffix), "."))
		if n == "" {
			continue
		}
		managedSet[n] = struct{}{}
	}
	if ready {
		for suffix := range managedSet {
			if _, ok := desired[suffix]; !ok {
				ready = false
				break
			}
		}
	}
	if ready {
		return "ready", "managed resolvers match desired suffix set"
	}

	if loopbackLoaded && loopbackResolverSync {
		return "pending", "root helper resolver sync should reconcile drift automatically"
	}
	if !loopbackLoaded {
		return "blocked", "root helper not loaded; run `sudo dnsvard bootstrap --force`"
	}
	return "blocked", "root helper loaded without resolver sync; run `sudo dnsvard bootstrap --force`"
}

func startupFailure(problemCode string, err error, fixes ...string) error {
	problemCode = strings.TrimSpace(problemCode)
	if problemCode == "" {
		problemCode = "startup_failed"
	}
	if err == nil {
		err = errors.New("unknown startup failure")
	}
	b := strings.Builder{}
	b.WriteString("startup problem: ")
	b.WriteString(problemCode)
	b.WriteString("\nwhy: ")
	b.WriteString(strings.TrimSpace(err.Error()))
	cleanFixes := dedupeStrings(fixes)
	if len(cleanFixes) == 0 {
		cleanFixes = []string{"run `dnsvard doctor` for targeted diagnostics and fix steps"}
	}
	for _, fix := range cleanFixes {
		b.WriteString("\nfix: ")
		b.WriteString(strings.TrimSpace(fix))
	}
	return errors.New(b.String())
}

func startupBuildRoutesError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(lower, "workspace scope requires project/workspace labels"):
		return startupFailure("config_scope_labels", err,
			"fix: set `dnsvard.project` and `dnsvard.workspace` labels on ambiguous containers",
			"fix: or set explicit routing labels (`dnsvard.hosts`, `dnsvard.http_port`) and rerun",
		)
	case strings.Contains(lower, "suffix") || strings.Contains(lower, "host_pattern"):
		return startupFailure("config_suffix_or_host_pattern", err,
			"fix: resolve suffix/host_pattern conflict in config and rerun `dnsvard daemon start`",
		)
	case strings.Contains(lower, "docker discover failed"):
		return startupFailure("docker_discovery", err,
			"fix: start Docker, or set `docker_discovery_mode: optional` if Docker should be non-blocking",
		)
	default:
		return startupFailure("route_build", err,
			"fix: run `dnsvard doctor --probe-routing` for per-route diagnostics",
		)
	}
}

func resolverStatusFailureDetail(err error) string {
	if err == nil {
		return "resolver status unavailable"
	}
	errText := strings.TrimSpace(err.Error())
	lower := strings.ToLower(errText)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted") {
		return fmt.Sprintf("resolver permission check failed: %s; run `sudo dnsvard bootstrap --force`", errText)
	}
	if strings.Contains(lower, "resolver") {
		return fmt.Sprintf("resolver check failed: %s; run `sudo dnsvard bootstrap --force`", errText)
	}
	return errText
}

func runDaemonLogs(cfg config.Config, plat platform.Controller, clear bool) error {
	return runDaemonLogsTo(os.Stdout, cfg, plat, clear)
}

func runDaemonLogsTo(out io.Writer, cfg config.Config, plat platform.Controller, clear bool) error {
	if out == nil {
		out = os.Stdout
	}
	if backend, err := plat.DaemonLogBackend(); err == nil && backend.Source == platform.DaemonLogSourceJournald {
		unit := strings.TrimSpace(backend.JournalUnit)
		if unit != "" {
			if clear {
				fmt.Fprintf(out, "daemon logs clear is not supported for journald-managed logs; showing recent entries for %s\n", unit)
			}
			fmt.Fprintf(out, "daemon logs source: journald (%s)\n", unit)
			if err := printSystemdJournalLogsTo(out, unit, 120); err == nil {
				return nil
			}
		}
	}

	logPath := daemon.LogPath(cfg.StateDir)
	if clear {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return fmt.Errorf("create daemon log dir: %w", err)
		}
		if err := os.WriteFile(logPath, []byte{}, 0o644); err != nil {
			return fmt.Errorf("clear daemon log: %w", err)
		}
		fmt.Fprintf(out, "daemon logs cleared (%s)\n", userPath(logPath))
	}
	lines, err := tailFile(logPath, 120)
	if err != nil {
		return err
	}
	fmt.Fprint(out, lines)
	return nil
}

func printSystemdJournalLogs(unit string, lines int) error {
	return printSystemdJournalLogsTo(os.Stdout, unit, lines)
}

func printSystemdJournalLogsTo(out io.Writer, unit string, lines int) error {
	if out == nil {
		out = os.Stdout
	}
	if strings.TrimSpace(unit) == "" {
		return errors.New("missing systemd unit")
	}
	if lines <= 0 {
		lines = 120
	}
	cmd := exec.Command("journalctl", "--user", "-u", unit, "-n", strconv.Itoa(lines), "--no-pager")
	cmdOut, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(cmdOut))
		if trimmed != "" {
			return fmt.Errorf("journalctl --user -u %s failed: %s", unit, trimmed)
		}
		return fmt.Errorf("journalctl --user -u %s failed: %w", unit, err)
	}
	fmt.Fprint(out, string(cmdOut))
	return nil
}

func runDaemonForeground(logger *logx.Logger, cfg config.Config, configPath string) error {
	return runDaemonForegroundTo(os.Stdout, os.Stderr, logger, cfg, configPath)
}

func runDaemonForegroundTo(out io.Writer, errOut io.Writer, logger *logx.Logger, cfg config.Config, configPath string) error {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}

	lock, err := daemon.AcquireLock(cfg.StateDir, true)
	if err != nil {
		return fmt.Errorf("acquire daemon lock: %w", err)
	}
	defer func() {
		if unlockErr := daemon.ReleaseLock(lock); unlockErr != nil {
			logger.Warn("daemon lock release failed", "error", unlockErr)
		}
	}()

	workspace, err := identity.DeriveWorkspaceLabel(identity.WorkspaceInput{
		WorktreeBase: cfg.Workspace.WorktreeBase,
		Branch:       cfg.Workspace.Branch,
		CwdBase:      cfg.Workspace.CwdBase,
	})
	if err != nil {
		return startupFailure("workspace_identity", err,
			"fix: ensure this repo has a valid branch/worktree name, then rerun `dnsvard daemon start`",
			"fix: run `dnsvard doctor` to confirm workspace/project identity inputs",
		)
	}
	project, err := identity.DeriveProjectLabel(identity.ProjectInput{RemoteURL: cfg.Project.RemoteURL, RepoBase: cfg.Project.RepoBase})
	if err != nil {
		return startupFailure("project_identity", err,
			"fix: ensure git remote/repo metadata is valid, then rerun `dnsvard daemon start`",
			"fix: run `dnsvard doctor` to validate derived project labels",
		)
	}
	alloc, err := allocator.New(cfg.LoopbackCIDR, filepath.Join(cfg.StateDir, "allocator-state.json"))
	if err != nil {
		return startupFailure("config_loopback_cidr", err,
			"fix: set a valid non-overlapping `loopback_cidr` in config and rerun",
			"fix: run `dnsvard doctor` to inspect resolved runtime config",
		)
	}
	ip, err := alloc.Allocate(identity.WorkspaceID(cfg.Workspace.Path))
	if err != nil {
		return startupFailure("workspace_ip_allocate", err,
			"fix: verify allocator state in state_dir is writable and loopback CIDR has free addresses",
			"fix: run `dnsvard doctor` to confirm workspace/runtime state",
		)
	}

	hosts, err := identity.Hostnames(identity.HostnameInput{Domain: cfg.Domain, Project: project, Workspace: workspace, Pattern: cfg.HostPattern})
	if err != nil {
		return startupFailure("config_domain_or_host_pattern", err,
			"fix: ensure `suffix` and `host_pattern` are valid and compatible",
		)
	}

	table := dnsserver.NewTable(".")
	effectiveCfg := cfg
	table.SetManagedZones(effectiveCfg.Domains)

	provider := docker.New()
	plat := platform.New()
	runtimeProvider := runtimeprovider.New(cfg.StateDir)
	unreachableTargets := newUnreachableTargetTracker()
	routesResult, err := buildRoutes(buildRoutesInput{
		Cfg:             effectiveCfg,
		Project:         project,
		Workspace:       workspace,
		WorkspaceIP:     ip,
		Allocator:       alloc,
		Provider:        provider,
		RuntimeProvider: runtimeProvider,
		AvoidHTTPTarget: unreachableTargets.SnapshotActive(time.Now()),
		Now:             time.Now(),
	})
	if err != nil {
		return startupBuildRoutesError(err)
	}
	records := routesResult.Records
	httpRoutes := routesResult.HTTPRoutes
	tcpRoutes := routesResult.TCPRoutes
	warnings := routesResult.Warnings
	workspaceConfigFiles := routesResult.WorkspaceConfigFiles
	effectiveCfg.Domains = routesResult.ManagedDomains
	table.SetManagedZones(effectiveCfg.Domains)
	lastWorkspaceDomains := copyStringMap(routesResult.WorkspaceDomains)
	warningState := map[string]struct{}{}
	lastAppliedHTTPTargets := routeTargetsByHost(httpRoutes)
	httpRouteHealthCache := map[string]httpRouteHealthCacheEntry{}
	healing := newHealCoordinator()
	actionStates := map[healActionID]daemon.SelfHealActionState{}
	var selfHealMu sync.Mutex
	selfHealTrigger := ""
	selfHealAction := ""
	selfHealDetail := ""
	selfHealDetector := ""
	selfHealComponent := ""
	selfHealActionID := ""
	selfHealFailed := false
	selfHealFailureCount := 0
	markSelfHeal := func(spec healActionSpec, event healEvent) {
		selfHealMu.Lock()
		defer selfHealMu.Unlock()
		selfHealTrigger = strings.TrimSpace(spec.trigger)
		selfHealAction = strings.TrimSpace(spec.action)
		selfHealDetail = strings.TrimSpace(event.detail)
		selfHealDetector = strings.TrimSpace(string(event.detector))
		selfHealComponent = strings.TrimSpace(spec.component)
		selfHealActionID = strings.TrimSpace(string(event.actionID))
		selfHealFailed = false
		selfHealFailureCount = 0
	}
	markHealFailure := func(actionID healActionID, detail string) {
		now := time.Now()
		state := actionStates[actionID]
		spec, ok := daemonHealMatrix[actionID]
		if !ok {
			spec = healActionSpec{component: "unknown", trigger: "unknown", action: "unknown"}
		}
		state.Component = strings.TrimSpace(spec.component)
		state.Detector = strings.TrimSpace(string(spec.detector))
		state.Trigger = strings.TrimSpace(spec.trigger)
		state.Action = strings.TrimSpace(spec.action)
		state.FailureCount++
		state.LastFailureAt = now
		state.LastFailureDetail = truncateReconcileError(strings.TrimSpace(detail))
		if state.FailureCount >= 3 {
			state.BlockedUntil = now.Add(5 * time.Minute)
		}
		actionStates[actionID] = state

		selfHealMu.Lock()
		defer selfHealMu.Unlock()
		selfHealTrigger = state.Trigger
		selfHealAction = state.Action
		selfHealDetail = state.LastFailureDetail
		selfHealDetector = state.Detector
		selfHealComponent = state.Component
		selfHealActionID = strings.TrimSpace(string(actionID))
		selfHealFailed = true
		selfHealFailureCount = state.FailureCount
	}
	markHealSuccess := func(actionID healActionID) {
		delete(actionStates, actionID)
	}
	canAttemptHealAction := func(actionID healActionID) bool {
		state, ok := actionStates[actionID]
		if !ok || state.BlockedUntil.IsZero() {
			return true
		}
		if time.Now().After(state.BlockedUntil) {
			delete(actionStates, actionID)
			return true
		}
		selfHealMu.Lock()
		selfHealTrigger = state.Trigger
		selfHealAction = state.Action
		selfHealDetail = truncateReconcileError(fmt.Sprintf("action temporarily suppressed after repeated failures; next retry at %s", state.BlockedUntil.Format(time.RFC3339)))
		selfHealDetector = state.Detector
		selfHealComponent = state.Component
		selfHealActionID = strings.TrimSpace(string(actionID))
		selfHealFailed = true
		selfHealFailureCount = state.FailureCount
		selfHealMu.Unlock()
		return false
	}
	logDiscoveryWarnings(logger, warnings, warningState)
	if err := table.Set(records); err != nil {
		return err
	}

	httpProxyErrorTrigger := make(chan struct{}, 1)
	httpRouterResetTrigger := make(chan struct{}, 1)
	router := httprouter.New()
	var proxyErrorMu sync.Mutex
	lastProxyErrorTrigger := time.Time{}
	b := proxyUnreachableBurst{}
	currentHTTPRoutes := append([]httprouter.Route(nil), httpRoutes...)
	router.SetProxyErrorHandler(func(hostname string, target string, err error) {
		status, detail := classifyLocalNetworkProbeError(err)
		if status == localNetworkDenied {
			logger.Warn("http proxy upstream denied by local network permission", "host", hostname, "target", target, "error", err)
			return
		}
		if !isRouteUnreachableError(detail, err) {
			return
		}
		proxyErrorMu.Lock()
		now := time.Now()
		unreachableTargets.Record(hostname, target, now)
		if now.Sub(lastProxyErrorTrigger) < 2*time.Second {
			proxyErrorMu.Unlock()
			return
		}
		lastProxyErrorTrigger = now
		shouldResetHTTPRouter := detectPersistentProxyUnreachable(&b, now)
		proxyErrorMu.Unlock()
		healing.emit(healEvent{detector: healDetectorHTTPProxy, actionID: healProxyReconcile, detail: detail}, markSelfHeal)
		logger.Warn("http proxy upstream unreachable; scheduling immediate reconcile", "host", hostname, "target", target, "error", err)
		executeRouteReconcile(httpProxyErrorTrigger)
		if shouldResetHTTPRouter {
			logger.Warn("persistent upstream unreachable; scheduling http router reset", "host", hostname, "target", target)
			select {
			case httpRouterResetTrigger <- struct{}{}:
			default:
			}
		}
	})
	if err := router.SetRoutes(httpRoutes); err != nil {
		return err
	}
	httpListen := ":" + strconv.Itoa(effectiveCfg.HTTPPort)
	httpRouterEnabled := true
	if err := router.Start(httpListen); err != nil {
		httpRouterEnabled = false
		logger.Warn("http router disabled", "listen", httpListen, "error", err)
		fmt.Fprintf(out, "warning: failed to start HTTP router on %s\n", httpListen)
		fmt.Fprintf(out, "warning: %v\n", err)
		for _, hint := range portConflictHints(plat, effectiveCfg.HTTPPort, "http_port") {
			fmt.Fprintf(out, "warning: %s\n", hint)
		}
		fmt.Fprintln(out, "warning: DNS remains active; set http_port >= 1024 or run daemon with elevated privileges for no-port HTTP")
	}
	if httpRouterEnabled {
		defer func() { _ = router.Stop() }()
	}

	tcpProxy := tcpproxy.New()
	if err := tcpProxy.SetRoutes(tcpRoutes); err != nil {
		logger.Warn("tcp proxy routes failed", "error", err)
	} else {
		fmt.Fprintf(out, "- tcp proxy listeners: %d\n", len(tcpProxy.Snapshot()))
	}
	defer tcpProxy.Stop()

	srv := dnsserver.New(cfg.DNSListen, table, cfg.DNSTTL)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start dns server on %s: %w\n%s", cfg.DNSListen, err, strings.Join(listenerFailureAdvice(plat, cfg.DNSListen, "dns_listen"), "\n"))
	}
	defer func() { _ = srv.Stop() }()

	if err := daemon.WritePID(cfg.StateDir, os.Getpid()); err != nil {
		return err
	}
	defer func() { _ = daemon.RemovePID(cfg.StateDir) }()

	fmt.Fprintf(out, "daemon started on %s\n", cfg.DNSListen)
	fmt.Fprintf(out, "- project: %s\n", project)
	fmt.Fprintf(out, "- workspace: %s\n", workspace)
	fmt.Fprintf(out, "- workspace_path: %s\n", cfg.Workspace.Path)
	fmt.Fprintf(out, "- summary: %s\n", srv.Summary())
	fmt.Fprintf(out, "- %s -> %s\n", hosts.ProjectFQDN, ip)
	fmt.Fprintf(out, "- %s -> %s\n", hosts.WorkspaceFQDN, ip)

	dockerTrigger := make(chan struct{}, 8)
	globalConfigTrigger := make(chan struct{}, 4)
	workspaceConfigTrigger := make(chan struct{}, 8)
	sighupTrigger := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lastConfigReloadError := ""
	lastResolverSyncSignature := ""
	syncResolverState := func(nextCfg config.Config) {
		sig := resolverSyncSignature(nextCfg)
		if sig == lastResolverSyncSignature {
			return
		}
		if err := persistResolverSyncState(nextCfg); err != nil {
			logger.Warn("resolver sync state update failed", "error", err)
			return
		}
		lastResolverSyncSignature = sig
	}
	syncResolverState(effectiveCfg)
	startGlobalConfigWatcher(ctx, logger, globalConfigTrigger)
	workspaceWatcher := startWorkspaceConfigWatcher(ctx, logger, workspaceConfigTrigger)
	if workspaceWatcher != nil {
		workspaceWatcher.Update(workspaceConfigFiles)
	}
	if err := provider.Watch(ctx, dockerTrigger); err != nil {
		logger.Warn("docker watch not active", "error", err)
	}

	type routeSnapshot struct {
		dns  map[string]struct{}
		http map[string]struct{}
		tcp  map[string]struct{}
	}
	snapshotFrom := func(dnsRecords []dnsserver.Record, httpRoutes []httprouter.Route, tcpRoutes []tcpproxy.Route) routeSnapshot {
		s := routeSnapshot{dns: map[string]struct{}{}, http: map[string]struct{}{}, tcp: map[string]struct{}{}}
		for _, rec := range dnsRecords {
			key := strings.ToLower(strings.TrimSpace(rec.Hostname)) + "|" + rec.IP.String()
			s.dns[key] = struct{}{}
		}
		for _, route := range httpRoutes {
			key := strings.ToLower(strings.TrimSpace(route.Hostname)) + "|" + strings.TrimSpace(route.Target)
			s.http[key] = struct{}{}
		}
		for _, route := range tcpRoutes {
			key := route.ListenIP + "|" + strconv.Itoa(route.ListenPort) + "|" + route.TargetIP + "|" + strconv.Itoa(route.TargetPort)
			s.tcp[key] = struct{}{}
		}
		return s
	}
	setDelta := func(prev map[string]struct{}, next map[string]struct{}) (int, int) {
		added := 0
		removed := 0
		for k := range next {
			if _, ok := prev[k]; !ok {
				added++
			}
		}
		for k := range prev {
			if _, ok := next[k]; !ok {
				removed++
			}
		}
		return added, removed
	}

	lastSnapshot := snapshotFrom(records, httpRoutes, tcpRoutes)
	lastSuccessAt := time.Now()
	reconcileSequence := uint64(0)
	writeReconcileState := func(cause string, result string, deltas routeSnapshot, warnings int, duration time.Duration, errText string) {
		reconcileSequence++
		trackedConfigs := 0
		watchedDirs := 0
		watchStats := provider.WatchStats()
		selfHealMu.Lock()
		healTrigger := selfHealTrigger
		healAction := selfHealAction
		healDetail := selfHealDetail
		healDetector := selfHealDetector
		healComponent := selfHealComponent
		healActionID := selfHealActionID
		healFailed := selfHealFailed
		healFailureCount := selfHealFailureCount
		selfHealMu.Unlock()
		if workspaceWatcher != nil {
			trackedConfigs, watchedDirs = workspaceWatcher.Stats()
		}
		state := daemon.ReconcileState{
			PID:                     os.Getpid(),
			ConfigDomain:            effectiveCfg.Domain,
			ConfigHostPattern:       effectiveCfg.HostPattern,
			WorkspaceDomains:        copyStringMap(lastWorkspaceDomains),
			UpdatedAt:               time.Now(),
			Sequence:                reconcileSequence,
			IntervalSeconds:         15,
			Cause:                   cause,
			Result:                  result,
			LastSuccessAt:           lastSuccessAt,
			LastError:               truncateReconcileError(strings.TrimSpace(errText)),
			Warnings:                warnings,
			DurationMS:              duration.Milliseconds(),
			TrackedConfigs:          trackedConfigs,
			WatchedDirs:             watchedDirs,
			DockerWatchRunning:      watchStats.Running,
			DockerWatchRestartCount: watchStats.RestartCount,
			DockerWatchLastStartAt:  watchStats.LastStartAt,
			DockerWatchLastEventAt:  watchStats.LastEventAt,
			DockerWatchLastError:    truncateReconcileError(strings.TrimSpace(watchStats.LastError)),
			SelfHealTrigger:         healTrigger,
			SelfHealAction:          healAction,
			SelfHealDetail:          truncateReconcileError(healDetail),
			SelfHealDetector:        healDetector,
			SelfHealComponent:       healComponent,
			SelfHealActionID:        healActionID,
			SelfHealFailed:          healFailed,
			SelfHealFailureCount:    healFailureCount,
			SelfHealActions:         snapshotSelfHealActionStates(actionStates),
		}
		if result == "success" {
			dnsAdded, dnsRemoved := setDelta(lastSnapshot.dns, deltas.dns)
			httpAdded, httpRemoved := setDelta(lastSnapshot.http, deltas.http)
			tcpAdded, tcpRemoved := setDelta(lastSnapshot.tcp, deltas.tcp)
			state.DNSAdded = dnsAdded
			state.DNSRemoved = dnsRemoved
			state.HTTPAdded = httpAdded
			state.HTTPRemoved = httpRemoved
			state.TCPAdded = tcpAdded
			state.TCPRemoved = tcpRemoved
		}
		if err := daemon.WriteReconcileState(cfg.StateDir, state); err != nil {
			logger.Warn("reconcile state write failed", "error", err)
		}
	}
	writeReconcileState("startup", "success", lastSnapshot, len(warnings), 0, "")
	lastDockerWatchRestartCount := 0
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		failureStreak := 0
		reconcile := func(cause string) {
			if strings.TrimSpace(cause) == "" {
				cause = "unknown"
			}
			startedAt := time.Now()
			if reloaded, reloadErr := config.Load(config.LoadOptions{CWD: effectiveCfg.Workspace.Path, ExplicitPath: configPath}); reloadErr != nil {
				logger.Warn("config reload failed; keeping previous config", "error", reloadErr)
				errText := reloadErr.Error()
				if errText != lastConfigReloadError {
					fmt.Fprintf(errOut, "warning: config reload failed; keeping previous config: %v\n", reloadErr)
					lastConfigReloadError = errText
				}
			} else {
				lastConfigReloadError = ""
				if reloaded.Domain != effectiveCfg.Domain {
					logger.Info("applied config reload", "suffix", reloaded.Domain)
				}
				if reloaded.DNSListen != effectiveCfg.DNSListen {
					logger.Warn("dns_listen changed in config; restart daemon to apply listener change", "old", effectiveCfg.DNSListen, "new", reloaded.DNSListen)
				}
				if reloaded.HTTPPort != effectiveCfg.HTTPPort {
					logger.Warn("http_port changed in config; restart daemon to apply listener change", "old", effectiveCfg.HTTPPort, "new", reloaded.HTTPPort)
				}
				effectiveCfg = reloaded
			}
			watchStats := provider.WatchStats()
			if detectDockerWatchRestart(lastDockerWatchRestartCount, watchStats.RestartCount) {
				lastDockerWatchRestartCount = watchStats.RestartCount
				healing.emit(healEvent{detector: healDetectorDockerWatch, actionID: healDockerWatchRestart, detail: watchStats.LastError}, markSelfHeal)
			}

			if cause == "timer" {
				if canAttemptHealAction(healPlatformDaemonRepair) {
					if repaired, repairErr := executePlatformDaemonRepair(cfg, plat, configPath); repairErr != nil {
						markHealFailure(healPlatformDaemonRepair, repairErr.Error())
						logger.Warn("platform daemon auto-repair failed", "error", repairErr)
					} else if repaired {
						markHealSuccess(healPlatformDaemonRepair)
						healing.emit(healEvent{detector: healDetectorTimer, actionID: healPlatformDaemonRepair, detail: "daemon manager degraded state repaired"}, markSelfHeal)
					}
				}
				if canAttemptHealAction(healResolverDriftReconcile) {
					if changed, driftErr := executeResolverDriftReconcile(effectiveCfg, plat); driftErr != nil {
						markHealFailure(healResolverDriftReconcile, driftErr.Error())
						logger.Warn("resolver drift auto-heal failed", "error", driftErr)
					} else if changed {
						markHealSuccess(healResolverDriftReconcile)
						healing.emit(healEvent{detector: healDetectorTimer, actionID: healResolverDriftReconcile, detail: "resolver files reconciled"}, markSelfHeal)
					}
				}
			}

			nextResult, recErr := buildRoutes(buildRoutesInput{
				Cfg:             effectiveCfg,
				Project:         project,
				Workspace:       workspace,
				WorkspaceIP:     ip,
				Allocator:       alloc,
				Provider:        provider,
				RuntimeProvider: runtimeProvider,
				AvoidHTTPTarget: unreachableTargets.SnapshotActive(time.Now()),
				Now:             time.Now(),
			})
			if recErr != nil {
				logger.Warn("reconcile failed", "cause", cause, "error", recErr)
				writeReconcileState(cause, "failure", lastSnapshot, 0, time.Since(startedAt), recErr.Error())
				failureStreak++
				if failureStreak >= 3 {
					sleep := time.Duration(failureStreak) * time.Second
					if sleep > 10*time.Second {
						sleep = 10 * time.Second
					}
					time.Sleep(sleep)
				}
				return
			}
			failureStreak = 0
			if workspaceWatcher != nil {
				workspaceWatcher.Update(nextResult.WorkspaceConfigFiles)
			}
			effectiveCfg.Domains = nextResult.ManagedDomains
			lastWorkspaceDomains = copyStringMap(nextResult.WorkspaceDomains)
			syncResolverState(effectiveCfg)
			table.SetManagedZones(effectiveCfg.Domains)
			logDiscoveryWarnings(logger, nextResult.Warnings, warningState)
			if setErr := table.Set(nextResult.Records); setErr != nil {
				logger.Warn("dns table update failed", "error", setErr)
				return
			}
			healthFallbacks := 0
			nextHTTPRoutes := append([]httprouter.Route(nil), nextResult.HTTPRoutes...)
			nextHTTPRoutes, healthFallbacks = preserveLastHealthyHTTPRoutes(nextHTTPRoutes, lastAppliedHTTPTargets, httpRouteHealthCache, time.Now())
			nextWarnings := append([]string(nil), nextResult.Warnings...)
			if healthFallbacks > 0 {
				nextWarnings = append(nextWarnings, fmt.Sprintf("kept %d previous HTTP targets until new targets are healthy", healthFallbacks))
				healing.emit(healEvent{detector: healDetectorTimer, actionID: healHTTPRoutePromotion, detail: fmt.Sprintf("fallbacks=%d", healthFallbacks)}, markSelfHeal)
			}
			if httpRouterEnabled {
				if setErr := router.SetRoutes(nextHTTPRoutes); setErr != nil {
					logger.Warn("http router update failed", "error", setErr)
					return
				}
				currentHTTPRoutes = append(currentHTTPRoutes[:0], nextHTTPRoutes...)
				lastAppliedHTTPTargets = routeTargetsByHost(nextHTTPRoutes)
			}
			if setErr := tcpProxy.SetRoutes(nextResult.TCPRoutes); setErr != nil {
				logger.Warn("tcp proxy update failed", "error", setErr)
				return
			}
			nextSnapshot := snapshotFrom(nextResult.Records, nextHTTPRoutes, nextResult.TCPRoutes)
			dnsAdded, dnsRemoved := setDelta(lastSnapshot.dns, nextSnapshot.dns)
			httpAdded, httpRemoved := setDelta(lastSnapshot.http, nextSnapshot.http)
			tcpAdded, tcpRemoved := setDelta(lastSnapshot.tcp, nextSnapshot.tcp)
			if shouldLogReconcileApplied(reconcileAppliedSummary{
				DNSAdded:    dnsAdded,
				DNSRemoved:  dnsRemoved,
				HTTPAdded:   httpAdded,
				HTTPRemoved: httpRemoved,
				TCPAdded:    tcpAdded,
				TCPRemoved:  tcpRemoved,
				Warnings:    len(nextWarnings),
			}) {
				logger.Info(
					"reconcile applied",
					"cause", cause,
					"dns_added", dnsAdded,
					"dns_removed", dnsRemoved,
					"http_added", httpAdded,
					"http_removed", httpRemoved,
					"tcp_added", tcpAdded,
					"tcp_removed", tcpRemoved,
					"warnings", len(nextWarnings),
					"duration_ms", time.Since(startedAt).Milliseconds(),
				)
			}
			lastSuccessAt = time.Now()
			writeReconcileState(cause, "success", nextSnapshot, len(nextWarnings), time.Since(startedAt), "")
			lastSnapshot = nextSnapshot
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcile("timer")
			case <-dockerTrigger:
				reconcile("docker_event")
			case <-httpProxyErrorTrigger:
				reconcile("http_proxy_error")
			case <-httpRouterResetTrigger:
				if !httpRouterEnabled {
					continue
				}
				if !canAttemptHealAction(healHTTPRouterReset) {
					continue
				}
				if !healing.emit(healEvent{detector: healDetectorHTTPProxy, actionID: healHTTPRouterReset, detail: "persistent upstream unreachable"}, markSelfHeal) {
					continue
				}
				if err := executeHTTPRouterReset(router, ":"+strconv.Itoa(effectiveCfg.HTTPPort), currentHTTPRoutes); err != nil {
					markHealFailure(healHTTPRouterReset, err.Error())
					logger.Warn("http router reset failed", "error", err)
					continue
				}
				markHealSuccess(healHTTPRouterReset)
				logger.Warn("http router reset applied", "reason", "persistent upstream unreachable")
			case <-globalConfigTrigger:
				reconcile("global_config_change")
			case <-workspaceConfigTrigger:
				reconcile("workspace_config_change")
			case <-sighupTrigger:
				reconcile("sighup")
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for {
		sig := <-sigCh
		switch sig {
		case syscall.SIGHUP:
			select {
			case sighupTrigger <- struct{}{}:
			default:
			}
			continue
		default:
			cancel()
			fmt.Fprintln(out, "daemon stopping")
			return nil
		}
	}
}

type workspaceConfigWatcher struct {
	mu         sync.Mutex
	watcher    *fsnotify.Watcher
	tracked    map[string]string
	watchedDir map[string]struct{}
}

func startWorkspaceConfigWatcher(ctx context.Context, logger *logx.Logger, trigger chan<- struct{}) *workspaceConfigWatcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("workspace config watcher disabled", "error", err)
		return nil
	}
	w := &workspaceConfigWatcher{
		watcher:    watcher,
		tracked:    map[string]string{},
		watchedDir: map[string]struct{}{},
	}
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !(event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
					continue
				}
				cfgPath := filepath.Clean(event.Name)
				w.mu.Lock()
				prev, tracked := w.tracked[cfgPath]
				if !tracked {
					w.mu.Unlock()
					continue
				}
				next := readFileFingerprint(cfgPath)
				if next == prev {
					w.mu.Unlock()
					continue
				}
				w.tracked[cfgPath] = next
				w.mu.Unlock()
				logger.Info("workspace config changed; scheduling reconcile", "path", cfgPath)
				select {
				case trigger <- struct{}{}:
				default:
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn("workspace config watcher error", "error", err)
			}
		}
	}()
	return w
}

func (w *workspaceConfigWatcher) Update(configFiles []string) {
	if w == nil {
		return
	}
	next := map[string]string{}
	for _, p := range configFiles {
		cfgPath := filepath.Clean(strings.TrimSpace(p))
		if cfgPath == "" {
			continue
		}
		dir := filepath.Dir(cfgPath)
		w.mu.Lock()
		_, watched := w.watchedDir[dir]
		w.mu.Unlock()
		if !watched {
			if err := w.watcher.Add(dir); err == nil {
				w.mu.Lock()
				w.watchedDir[dir] = struct{}{}
				w.mu.Unlock()
			}
		}
		next[cfgPath] = readFileFingerprint(cfgPath)
	}
	w.mu.Lock()
	w.tracked = next
	w.mu.Unlock()
}

func (w *workspaceConfigWatcher) Stats() (int, int) {
	if w == nil {
		return 0, 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tracked), len(w.watchedDir)
}

func startGlobalConfigWatcher(ctx context.Context, logger *logx.Logger, trigger chan<- struct{}) {
	globalPath := config.GlobalConfigPath()
	watchDir := filepath.Dir(globalPath)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("global config watcher disabled", "error", err)
		return
	}
	if err := watcher.Add(watchDir); err != nil {
		logger.Warn("global config watcher disabled", "path", watchDir, "error", err)
		_ = watcher.Close()
		return
	}

	fingerprint := readFileFingerprint(globalPath)
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Clean(event.Name) != filepath.Clean(globalPath) {
					continue
				}
				if !(event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
					continue
				}
				next := readFileFingerprint(globalPath)
				if next == fingerprint {
					continue
				}
				fingerprint = next
				logger.Info("global config changed; scheduling reconcile", "path", globalPath)
				select {
				case trigger <- struct{}{}:
				default:
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn("global config watcher error", "error", err)
			}
		}
	}()
}

func readFileFingerprint(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

func formatReconcileStateSummary(state daemon.ReconcileState, runningPID int) (string, bool, string) {
	tm := state.UpdatedAt.Format(time.RFC3339)
	if state.UpdatedAt.IsZero() {
		tm = "unknown"
	}
	cause := strings.TrimSpace(state.Cause)
	if cause == "" {
		cause = "unknown"
	}
	result := strings.TrimSpace(state.Result)
	if result == "" {
		result = "unknown"
	}
	summary := fmt.Sprintf(
		"time=%s cause=%s result=%s dns(+%d/-%d) http(+%d/-%d) tcp(+%d/-%d) warnings=%d duration_ms=%d tracked=%d watched=%d docker_watch(running=%t,restarts=%d)",
		tm,
		cause,
		result,
		state.DNSAdded,
		state.DNSRemoved,
		state.HTTPAdded,
		state.HTTPRemoved,
		state.TCPAdded,
		state.TCPRemoved,
		state.Warnings,
		state.DurationMS,
		state.TrackedConfigs,
		state.WatchedDirs,
		state.DockerWatchRunning,
		state.DockerWatchRestartCount,
	)
	if result == "failure" {
		errText := strings.TrimSpace(state.LastError)
		if errText == "" {
			errText = "unknown"
		}
		summary += fmt.Sprintf(" error=%s", errText)
	}
	if v := strings.TrimSpace(state.SelfHealTrigger); v != "" {
		summary += fmt.Sprintf(" self_heal(trigger=%s", v)
		if detector := strings.TrimSpace(state.SelfHealDetector); detector != "" {
			summary += fmt.Sprintf(",detector=%s", detector)
		}
		if component := strings.TrimSpace(state.SelfHealComponent); component != "" {
			summary += fmt.Sprintf(",component=%s", component)
		}
		if actionID := strings.TrimSpace(state.SelfHealActionID); actionID != "" {
			summary += fmt.Sprintf(",action_id=%s", actionID)
		}
		if action := strings.TrimSpace(state.SelfHealAction); action != "" {
			summary += fmt.Sprintf(",action=%s", action)
		}
		if detail := strings.TrimSpace(state.SelfHealDetail); detail != "" {
			summary += fmt.Sprintf(",detail=%s", detail)
		}
		if state.SelfHealFailed {
			summary += fmt.Sprintf(",failed=true,failure_count=%d", state.SelfHealFailureCount)
		}
		summary += ")"
	}

	if runningPID <= 0 {
		return summary, true, "daemon_not_running"
	}
	if state.PID <= 0 || state.PID != runningPID {
		return summary, true, "pid_mismatch"
	}
	if !state.UpdatedAt.IsZero() {
		interval := time.Duration(state.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 15 * time.Second
		}
		maxAge := 2*interval + 5*time.Second
		if age := time.Since(state.UpdatedAt); age > maxAge {
			return summary, true, fmt.Sprintf("age=%s", age.Round(time.Second))
		}
	}
	return summary, false, ""
}

func truncateReconcileError(errText string) string {
	errText = strings.TrimSpace(errText)
	const max = 400
	if len(errText) <= max {
		return errText
	}
	return errText[:max] + "..."
}

func resolverSyncSignature(cfg config.Config) string {
	suffixes := append([]string{}, cfg.Domains...)
	for i := range suffixes {
		suffixes[i] = strings.ToLower(strings.Trim(strings.TrimSpace(suffixes[i]), "."))
	}
	sort.Strings(suffixes)
	return strings.Join(suffixes, ",") + "|" + strings.TrimSpace(cfg.DNSListen)
}

type reconcileAppliedSummary struct {
	DNSAdded    int
	DNSRemoved  int
	HTTPAdded   int
	HTTPRemoved int
	TCPAdded    int
	TCPRemoved  int
	Warnings    int
}

func shouldLogReconcileApplied(summary reconcileAppliedSummary) bool {
	if summary.Warnings > 0 {
		return true
	}
	return summary.DNSAdded != 0 || summary.DNSRemoved != 0 || summary.HTTPAdded != 0 || summary.HTTPRemoved != 0 || summary.TCPAdded != 0 || summary.TCPRemoved != 0
}

func isRouteUnreachableError(detail string, err error) bool {
	parts := []string{detail}
	if err != nil {
		parts = append(parts, err.Error())
	}
	lower := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(lower, "no route to host") ||
		strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "host is down")
}

func routeTargetsByHost(routes []httprouter.Route) map[string]string {
	out := make(map[string]string, len(routes))
	for _, route := range routes {
		host := strings.ToLower(strings.TrimSpace(route.Hostname))
		target := strings.TrimSpace(route.Target)
		if host == "" || target == "" {
			continue
		}
		out[host] = target
	}
	return out
}

type httpRouteHealthCacheEntry struct {
	CheckedAt time.Time
	Healthy   bool
}

const (
	httpRouteHealthyCacheTTL   = 45 * time.Second
	httpRouteUnhealthyCacheTTL = 3 * time.Second
	httpRouteHealthCacheMaxAge = 3 * time.Minute
)

func preserveLastHealthyHTTPRoutes(next []httprouter.Route, previous map[string]string, cache map[string]httpRouteHealthCacheEntry, now time.Time) ([]httprouter.Route, int) {
	if len(previous) == 0 || len(next) == 0 {
		return next, 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	pruneHTTPRouteHealthCache(cache, now)

	isHealthy := func(target string) bool {
		return isHTTPRouteHealthyCached(target, cache, now)
	}

	out := make([]httprouter.Route, 0, len(next))
	fallbacks := 0
	for _, route := range next {
		host := strings.ToLower(strings.TrimSpace(route.Hostname))
		target := strings.TrimSpace(route.Target)
		if host == "" || target == "" {
			out = append(out, route)
			continue
		}
		if isHealthy(target) {
			out = append(out, route)
			continue
		}
		if prevTarget, ok := previous[host]; ok && prevTarget != "" && prevTarget != target {
			if isHealthy(prevTarget) {
				route.Target = prevTarget
				fallbacks++
			}
		}
		out = append(out, route)
	}
	return out, fallbacks
}

func isHTTPRouteHealthyCached(target string, cache map[string]httpRouteHealthCacheEntry, now time.Time) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if cache != nil {
		if entry, ok := cache[target]; ok {
			ttl := httpRouteUnhealthyCacheTTL
			if entry.Healthy {
				ttl = httpRouteHealthyCacheTTL
			}
			if now.Sub(entry.CheckedAt) < ttl {
				return entry.Healthy
			}
		}
	}
	healthy := isHTTPRouteHealthy(target)
	if cache != nil {
		cache[target] = httpRouteHealthCacheEntry{CheckedAt: now, Healthy: healthy}
	}
	return healthy
}

func pruneHTTPRouteHealthCache(cache map[string]httpRouteHealthCacheEntry, now time.Time) {
	if cache == nil {
		return
	}
	for target, entry := range cache {
		if now.Sub(entry.CheckedAt) > httpRouteHealthCacheMaxAge {
			delete(cache, target)
		}
	}
}

func isHTTPRouteHealthy(target string) bool {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.TrimSpace(u.Host)
	if host == "" {
		return false
	}
	dialer := &net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := dialer.Dial("tcp", host)
	if err != nil {
		return false
	}
	_ = conn.Close()

	client := &http.Client{Timeout: 300 * time.Millisecond}
	req, reqErr := http.NewRequest(http.MethodHead, target, nil)
	if reqErr != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

func autoHealResolverDrift(cfg config.Config, plat platform.Controller) (bool, error) {
	if !plat.DaemonCanAutoHealResolvers() {
		return false, nil
	}
	changed := false
	for _, domain := range cfg.Domains {
		d := strings.TrimSpace(domain)
		if d == "" {
			continue
		}
		spec, err := resolverSpecFromListen(cfg, d)
		if err != nil {
			return changed, err
		}
		match, err := plat.ResolverMatches(spec)
		if err != nil {
			return changed, err
		}
		if match {
			continue
		}
		if err := plat.EnsureResolver(spec); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

func autoRepairLaunchdDegradedState(cfg config.Config, plat platform.Controller, configPath string) (bool, error) {

	if isGoRunExecutable(os.Args[0]) {
		return false, nil
	}
	return plat.AutoRepairDaemon(platform.DaemonSpec{
		BinaryPath: os.Args[0],
		ConfigPath: configPath,
		StateDir:   cfg.StateDir,
		WorkingDir: cfg.Workspace.Path,
	})
}

type proxyUnreachableBurst struct {
	windowStart    time.Time
	count          int
	lastEscalation time.Time
}

type unreachableTargetTracker struct {
	mu      sync.Mutex
	entries map[string]unreachableTargetState
}

type unreachableTargetState struct {
	windowStart  time.Time
	failureCount int
	quarantined  time.Time
}

func newUnreachableTargetTracker() *unreachableTargetTracker {
	return &unreachableTargetTracker{entries: map[string]unreachableTargetState{}}
}

func (t *unreachableTargetTracker) Record(hostname string, target string, now time.Time) {
	if t == nil {
		return
	}
	host := strings.ToLower(strings.TrimSpace(hostname))
	target = strings.TrimSpace(target)
	if host == "" || target == "" {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	key := host + "|" + target
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.entries[key]
	if state.windowStart.IsZero() || now.Sub(state.windowStart) > 20*time.Second {
		state.windowStart = now
		state.failureCount = 0
	}
	state.failureCount++
	if state.failureCount >= 3 {
		state.quarantined = now.Add(45 * time.Second)
		state.failureCount = 0
		state.windowStart = now
	}
	t.entries[key] = state
}

func (t *unreachableTargetTracker) SnapshotActive(now time.Time) map[string]time.Time {
	if t == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]time.Time{}
	for key, state := range t.entries {
		if state.quarantined.IsZero() || !now.Before(state.quarantined) {
			delete(t.entries, key)
			continue
		}
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			delete(t.entries, key)
			continue
		}
		target := strings.TrimSpace(parts[1])
		if target == "" {
			continue
		}
		expiresAt, ok := out[target]
		if !ok || expiresAt.Before(state.quarantined) {
			out[target] = state.quarantined
		}
	}
	return out
}

func detectPersistentProxyUnreachable(state *proxyUnreachableBurst, now time.Time) bool {
	if state == nil {
		return false
	}
	if state.windowStart.IsZero() || now.Sub(state.windowStart) > 30*time.Second {
		state.windowStart = now
		state.count = 0
	}
	state.count++
	if state.count < 6 {
		return false
	}
	if !state.lastEscalation.IsZero() && now.Sub(state.lastEscalation) <= 2*time.Minute {
		return false
	}
	state.lastEscalation = now
	state.count = 0
	return true
}

func detectDockerWatchRestart(previous int, current int) bool {
	return current > previous
}

func executeRouteReconcile(trigger chan<- struct{}) {
	select {
	case trigger <- struct{}{}:
	default:
	}
}

func executeHTTPRouterReset(router *httprouter.Router, listen string, routes []httprouter.Route) error {
	return resetHTTPRouter(router, listen, routes)
}

func executePlatformDaemonRepair(cfg config.Config, plat platform.Controller, configPath string) (bool, error) {
	return autoRepairLaunchdDegradedState(cfg, plat, configPath)
}

func executeResolverDriftReconcile(cfg config.Config, plat platform.Controller) (bool, error) {
	return autoHealResolverDrift(cfg, plat)
}

func resetHTTPRouter(router *httprouter.Router, listen string, routes []httprouter.Route) error {
	if err := router.Stop(); err != nil {
		return fmt.Errorf("stop http router: %w", err)
	}
	if err := router.SetRoutes(routes); err != nil {
		return fmt.Errorf("set http routes: %w", err)
	}
	var startErr error
	for range 20 {
		if err := router.Start(listen); err == nil {
			return nil
		} else {
			startErr = err
			if !isAddressInUseError(err) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return fmt.Errorf("start http router: %w", startErr)
}

func isAddressInUseError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "address already in use") || strings.Contains(lower, "eaddrinuse")
}

func listenerFailureAdvice(plat platform.Controller, listen string, key string) []string {
	port, ok := netutil.ParseListenPort(listen)
	if !ok {
		return []string{fmt.Sprintf("fix: check %s and ensure no process is already bound", key)}
	}
	return portConflictHints(plat, port, key)
}

func portConflictHints(plat platform.Controller, port int, key string) []string {
	hints := []string{}
	listeners, err := plat.DescribeTCPPortListeners(port)
	if err == nil {
		for _, item := range listeners {
			hints = append(hints, "listener: "+strings.TrimSpace(item))
		}
	}
	hints = append(hints, fmt.Sprintf("fix: stop the process listening on %d, or set %s to a different port", port, key))
	if port < 1024 {
		hints = append(hints, fmt.Sprintf("fix: port %d is privileged; use an unprivileged port or run `dnsvard bootstrap -f` for privileged setup", port))
	}
	return hints
}

type selfHealProblem struct {
	ActionID     string
	Code         string
	Component    string
	Message      string
	FailureCount int
	BlockedUntil time.Time
	Fixes        []string
}

func selfHealProblemLines(problem selfHealProblem) []string {
	blockedUntil := "none"
	if !problem.BlockedUntil.IsZero() {
		blockedUntil = problem.BlockedUntil.Format(time.RFC3339)
	}
	lines := []string{
		fmt.Sprintf("- self_heal_problem: action_id=%s component=%s code=%s failure_count=%d blocked_until=%s", problem.ActionID, problem.Component, problem.Code, problem.FailureCount, blockedUntil),
		fmt.Sprintf("- self_heal_problem_detail: %s", problem.Message),
	}
	for _, hint := range problem.Fixes {
		lines = append(lines, fmt.Sprintf("- self_heal_fix: %s", hint))
	}
	return lines
}

func snapshotSelfHealActionStates(actionStates map[healActionID]daemon.SelfHealActionState) map[string]daemon.SelfHealActionState {
	if len(actionStates) == 0 {
		return nil
	}
	out := map[string]daemon.SelfHealActionState{}
	for actionID, state := range actionStates {
		if state.FailureCount <= 0 && state.BlockedUntil.IsZero() {
			continue
		}
		state.LastFailureDetail = truncateReconcileError(strings.TrimSpace(state.LastFailureDetail))
		out[strings.TrimSpace(string(actionID))] = state
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func selfHealProblemsFromState(state daemon.ReconcileState, cfg config.Config, plat platform.Controller) []selfHealProblem {
	now := time.Now()
	actionIDs := make([]string, 0, len(state.SelfHealActions))
	for actionID := range state.SelfHealActions {
		actionIDs = append(actionIDs, strings.TrimSpace(actionID))
	}
	sort.Strings(actionIDs)

	problems := make([]selfHealProblem, 0, len(actionIDs))
	for _, actionID := range actionIDs {
		actionState := state.SelfHealActions[actionID]
		if actionState.FailureCount <= 0 && (actionState.BlockedUntil.IsZero() || now.After(actionState.BlockedUntil)) {
			continue
		}
		detail := strings.TrimSpace(actionState.LastFailureDetail)
		if detail == "" {
			detail = fmt.Sprintf("self-heal action %s is degraded", actionID)
		}
		code, fixes := selfHealResolutionByAction(actionID, detail, cfg, plat)
		if !actionState.BlockedUntil.IsZero() && now.Before(actionState.BlockedUntil) {
			fixes = append(fixes, fmt.Sprintf("automatic retries resume at %s; after fixing root cause, run `dnsvard daemon restart` to retry immediately", actionState.BlockedUntil.Format(time.RFC3339)))
		}
		component := strings.TrimSpace(actionState.Component)
		if component == "" {
			if spec, ok := daemonHealMatrix[healActionID(actionID)]; ok {
				component = strings.TrimSpace(spec.component)
			}
		}
		if component == "" {
			component = "self_heal"
		}
		problems = append(problems, selfHealProblem{
			ActionID:     actionID,
			Code:         code,
			Component:    component,
			Message:      detail,
			FailureCount: actionState.FailureCount,
			BlockedUntil: actionState.BlockedUntil,
			Fixes:        dedupeStrings(fixes),
		})
	}

	if len(problems) == 0 && state.SelfHealFailed {
		actionID := strings.TrimSpace(state.SelfHealActionID)
		detail := strings.TrimSpace(state.SelfHealDetail)
		if detail == "" {
			detail = "self-heal action is degraded"
		}
		code, fixes := selfHealResolutionByAction(actionID, detail, cfg, plat)
		component := strings.TrimSpace(state.SelfHealComponent)
		if component == "" {
			component = "self_heal"
		}
		problems = append(problems, selfHealProblem{
			ActionID:     actionID,
			Code:         code,
			Component:    component,
			Message:      detail,
			FailureCount: state.SelfHealFailureCount,
			Fixes:        dedupeStrings(fixes),
		})
	}

	return problems
}

func selfHealResolution(state daemon.ReconcileState, cfg config.Config, plat platform.Controller) (string, []string) {
	return selfHealResolutionByAction(state.SelfHealActionID, state.SelfHealDetail, cfg, plat)
}

func selfHealResolutionByAction(actionID string, detail string, cfg config.Config, plat platform.Controller) (string, []string) {
	actionID = strings.TrimSpace(actionID)
	detail = strings.TrimSpace(detail)
	code := "self_heal_unknown"
	hints := []string{}
	switch actionID {
	case string(healProxyReconcile):
		code = "self_heal_proxy_reconcile_failed"
		hints = append(hints, "run `dnsvard doctor --probe-routing` to inspect route health and host reachability")
	case string(healHTTPRouterReset):
		code = "self_heal_http_router_reset_failed"
		hints = append(hints, portConflictHints(plat, cfg.HTTPPort, "http_port")...)
		hints = append(hints, "verify the target service is running and listening on the configured container/host port")
	case string(healDockerWatchRestart):
		code = "self_heal_docker_watch_failed"
		hints = append(hints, "ensure Docker daemon is healthy (`docker ps` should succeed)")
		hints = append(hints, "if Docker is running but watch keeps failing, restart Docker Desktop/engine")
	case string(healPlatformDaemonRepair):
		code = "self_heal_platform_daemon_repair_failed"
		hints = append(hints, "run `dnsvard bootstrap -f` to reinstall platform daemon manager integration")
		hints = append(hints, "if bootstrap is blocked by permissions, rerun with sudo")
	case string(healResolverDriftReconcile):
		code = "self_heal_resolver_reconcile_failed"
		hints = append(hints, "run `dnsvard bootstrap -f` to reconcile resolver permissions and managed resolver files")
	case string(healHTTPRoutePromotion):
		code = "self_heal_http_route_promotion_failed"
		hints = append(hints, "check upstream container health and keep at least one healthy target for each routed hostname")
	default:
		hints = append(hints, "run `dnsvard doctor --probe-routing` and inspect self-heal metadata for targeted repair")
	}
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "no route to host") {
		hints = append(hints, "check container/service network reachability and restart the affected service if needed")
	}
	if strings.Contains(lower, "permission denied") {
		hints = append(hints, "grant required OS permissions and rerun `dnsvard bootstrap -f`")
	}
	if len(hints) == 0 {
		hints = append(hints, "inspect daemon logs (`dnsvard daemon logs`) for exact failing component details")
	}
	return code, dedupeStrings(hints)
}

func selfHealResolutionHints(state daemon.ReconcileState, cfg config.Config, plat platform.Controller) []string {
	_, hints := selfHealResolution(state, cfg, plat)
	return hints
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func runDaemonBackground(cfg config.Config, configPath string, quiet bool) error {
	return runDaemonBackgroundTo(os.Stdout, cfg, configPath, quiet)
}

func runDaemonBackgroundTo(out io.Writer, cfg config.Config, configPath string, quiet bool) error {
	if out == nil {
		out = os.Stdout
	}
	if pid, err := daemon.ReadPID(cfg.StateDir); err == nil && daemon.ProcessRunning(pid) {
		return fmt.Errorf("daemon already running with pid %d", pid)
	}
	lock, lockErr := daemon.AcquireLock(cfg.StateDir, false)
	if lockErr != nil {
		if errors.Is(lockErr, daemon.ErrDaemonLockHeld) {
			return errors.New("daemon already running (lock held)")
		}
		return lockErr
	}
	if releaseErr := daemon.ReleaseLock(lock); releaseErr != nil {
		return releaseErr
	}

	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return err
	}
	logPath := daemon.LogPath(cfg.StateDir)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	args := make([]string, 0, 6)
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "-c", configPath)
	}
	args = append(args, "daemon", "start", "--foreground")
	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon process: %w", err)
	}

	for range 10 {
		time.Sleep(100 * time.Millisecond)
		if daemon.ProcessRunning(cmd.Process.Pid) {
			break
		}
	}

	if !quiet {
		fmt.Fprintf(out, "daemon started in background (pid=%d)\n", cmd.Process.Pid)
		fmt.Fprintf(out, "log file: %s\n", userPath(logPath))
	}
	return nil
}
