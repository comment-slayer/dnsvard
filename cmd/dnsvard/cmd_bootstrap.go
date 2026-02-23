package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/logx"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

func bootstrapHammerReason(cfg config.Config, plat platform.Controller) (string, error) {
	version, ok, err := readBootstrapStateVersion(cfg.StateDir)
	if err != nil {
		return "bootstrap state unreadable", nil
	}
	if !ok {
		return "bootstrap state missing", nil
	}
	if version < bootstrapStateVersion {
		return fmt.Sprintf("bootstrap state version %d -> %d", version, bootstrapStateVersion), nil
	}

	loopbackStatus, loopbackErr := plat.LoopbackStatus()
	if loopbackErr != nil {
		return "loopback helper is not loaded", nil
	}
	if !loopbackHasResolverSync(loopbackStatus) {
		return "loopback helper missing resolver sync support", nil
	}

	if _, err := plat.DaemonStatus(); err != nil {
		return "user daemon launch agent is not loaded", nil
	}

	pid, err := daemon.ReadPID(cfg.StateDir)
	if err != nil || !daemon.ProcessRunning(pid) {
		return "daemon pid is not running", nil
	}

	return "", nil
}

type bootstrapRunOptions struct {
	Logger     *logx.Logger
	Cfg        config.Config
	Platform   platform.Controller
	ConfigPath string
	Force      bool
	Quick      bool
	AsUser     string
	Verbose    bool
}

func runBootstrap(options bootstrapRunOptions) error {
	return runBootstrapTo(os.Stdout, os.Stderr, options)
}

func runBootstrapTo(out io.Writer, errOut io.Writer, options bootstrapRunOptions) error {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}

	logger := options.Logger
	cfg := options.Cfg
	plat := options.Platform
	configPath := options.ConfigPath
	force := options.Force
	quick := options.Quick
	asUser := options.AsUser
	verbose := options.Verbose

	isLinux := plat.BootstrapRequiresRootTargetUser()
	isRoot := os.Geteuid() == 0
	if err := plat.BootstrapPrecheck(); err != nil {
		return err
	}
	targetUser := strings.TrimSpace(asUser)
	if isLinux && isRoot && targetUser == "" {
		targetUser = strings.TrimSpace(os.Getenv("SUDO_USER"))
	}
	if isLinux && isRoot && targetUser == "" {
		return errors.New("bootstrap run as root on this platform requires a target user\nfix: rerun with: sudo dnsvard bootstrap --as-user <your-user>\nif you intentionally run daemon as root: --as-user root")
	}
	if plat.BootstrapNeedsStateDirOwnershipPrep() && isRoot {
		sudoUser := strings.TrimSpace(os.Getenv("SUDO_USER"))
		if sudoUser != "" {
			if err := ensurePathOwnedByUser(logger, cfg.StateDir, sudoUser); err != nil {
				return fmt.Errorf("prepare state directory ownership for %s: %w", sudoUser, err)
			}
		}
	}

	if !force {
		reason, reasonErr := bootstrapHammerReason(cfg, plat)
		if reasonErr != nil {
			return reasonErr
		}
		if reason == "" {
			if verbose {
				fmt.Fprintf(out, "Bootstrap fast path: reconciling state without reinstall.\n")
			}
			if err := persistResolverSyncState(cfg); err != nil {
				return err
			}
			resolved, removed, err := reconcileResolvers(cfg, plat)
			if err != nil {
				return err
			}
			notifyDaemonReconcile(cfg.StateDir)
			fmt.Fprintln(out, "bootstrap complete")
			if verbose && len(resolved) > 0 {
				fmt.Fprintf(out, "- resolvers installed: %s\n", strings.Join(resolved, ","))
			}
			if verbose && len(removed) > 0 {
				fmt.Fprintf(out, "- resolvers removed: %s\n", strings.Join(removed, ","))
			}
			if verbose {
				return runDoctor(doctorRunOptions{Cfg: cfg, Platform: plat})
			}
			fmt.Fprintln(out, "next: run `dnsvard doctor`")
			return nil
		}
		if quick {
			fmt.Fprintf(out, "Bootstrap quick mode requires full bootstrap: %s\n", reason)
		} else {
			if reason == "bootstrap state missing" {
				fmt.Fprintln(out, "First-time setup detected.")
			} else {
				fmt.Fprintf(out, "Bootstrap requires full setup: %s\n", reason)
			}
		}
	}
	if err := ensureDNSListenAvailable(cfg.DNSListen); err != nil {
		fmt.Fprintf(out, "Detected existing listener on %s; attempting clean restart...\n", cfg.DNSListen)
		if stopErr := stopExistingDaemon(cfg, plat); stopErr != nil {
			logger.Warn("bootstrap stop-existing daemon warning", "error", stopErr)
		}
		if waitErr := waitForDNSListenAvailable(cfg.DNSListen, 3*time.Second); waitErr != nil {
			return waitErr
		}
	}
	start := time.Now()
	fmt.Fprintln(out, "bootstrapping dnsvard...")
	fmt.Fprintln(out, "- expected time: ~1-2s when already configured, ~3-15s on first-time/full setup")
	fmt.Fprintln(out, "- local-only changes: resolver files, launch agents, and dnsvard state")
	if verbose {
		if note := strings.TrimSpace(plat.BootstrapVerboseNote(isRoot, targetUser)); note != "" {
			fmt.Fprintf(out, "note: %s\n", note)
		}
	}
	if verbose {
		fmt.Fprintf(out, "1/3 Reconciling resolvers for managed suffixes: %s\n", strings.Join(cfg.Domains, ","))
	} else {
		fmt.Fprintln(out, "- step 1/3: reconciling local DNS resolver settings...")
	}
	resolved, removed, err := reconcileResolvers(cfg, plat)
	resolverPermissionDenied := false
	if err != nil {
		if isLinux && !isRoot && strings.Contains(strings.ToLower(err.Error()), "requires elevated permissions") {
			resolverPermissionDenied = true
			fmt.Fprintln(out, "warning: resolver bootstrap requires root on Linux")
			fmt.Fprintln(out, "warning: run once with sudo to install resolver config:")
			fmt.Fprintln(out, "warning:   sudo dnsvard bootstrap")
		} else {
			return err
		}
	}

	if verbose {
		fmt.Fprintln(out, "2/3 Installing and starting background daemon")
	} else {
		fmt.Fprintln(out, "- step 2/3: installing/updating background agents...")
	}
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if isGoRunExecutable(binPath) {
		return fmt.Errorf("bootstrap cannot run from `go run` ephemeral binary (%s)\nfix: install or build dnsvard first (example: `go build -o ~/.local/bin/dnsvard ./cmd/dnsvard`), then rerun bootstrap", userPath(binPath))
	}
	if plat.BootstrapSupportsPrivilegedPortCapability() {
		if err := ensurePrivilegedHTTPBindCapability(logger, cfg, binPath, isRoot); err != nil {
			return err
		}
	}

	if plat.BootstrapHasRootResolverOnlyPass() && isRoot {
		if verbose {
			fmt.Fprintln(out, "2/3 Linux root pass complete (resolver bootstrap).")
		}
		if targetUser != "" {
			if err := ensurePathOwnedByUser(logger, cfg.StateDir, targetUser); err != nil {
				return fmt.Errorf("prepare state directory ownership: %w", err)
			}
			if verbose {
				fmt.Fprintf(out, "3/3 Installing user daemon as %s ...\n", targetUser)
			}
			if err := runLinuxUserDaemonPass(configPath, targetUser, binPath, out, errOut); err != nil {
				return fmt.Errorf("user daemon pass failed for %s: %w\nfix: run manually: sudo -iu %s dnsvard daemon restart", targetUser, err, targetUser)
			}
			fmt.Fprintln(out, "bootstrap complete")
			fmt.Fprintln(out, "next: run `dnsvard doctor`")
			return nil
		} else {
			fmt.Fprintln(out, "next: run `dnsvard bootstrap` as your normal (non-root) user to install/start daemon")
			fmt.Fprintln(out, "tip: or provide --as-user <name> when running bootstrap as root")
		}
		return nil
	}

	auditPrivilegedOperation(logger, "install_platform_daemon", "binary_path", binPath, "state_dir", cfg.StateDir)
	plistPath, err := plat.InstallDaemon(platform.DaemonSpec{
		BinaryPath: binPath,
		ConfigPath: configPath,
		StateDir:   cfg.StateDir,
		WorkingDir: cfg.Workspace.Path,
	})
	if err != nil {
		return err
	}
	auditPrivilegedOperation(logger, "install_loopback_agent", "binary_path", binPath, "state_dir", cfg.StateDir)
	loopbackPath, err := plat.InstallLoopbackAgent(platform.LoopbackAgentSpec{
		BinaryPath:        binPath,
		StateFile:         filepath.Join(cfg.StateDir, "allocator-state.json"),
		ResolverStateFile: resolverSyncStatePath(cfg.StateDir),
		CIDR:              cfg.LoopbackCIDR,
	})
	if err != nil {
		return err
	}

	if verbose {
		fmt.Fprintln(out, "3/3 Running health checks")
	} else {
		fmt.Fprintln(out, "- step 3/3: waiting for daemon health check...")
	}
	if verbose && os.Geteuid() == 0 && plat.SupportsLocalNetworkProbe() {
		fmt.Fprintln(out, "- local network permission check skipped under sudo")
		fmt.Fprintf(out, "- only run `dnsvard doctor --check-local-network` as your user if requests to *.%s fail\n", cfg.Domain)
	}
	waitForDaemonPID(cfg.StateDir, 2*time.Second)
	fmt.Fprintf(out, "bootstrap complete in %s\n", time.Since(start).Round(100*time.Millisecond))
	if verbose && resolverPermissionDenied {
		fmt.Fprintln(out, "- resolvers installed: pending (requires sudo bootstrap pass)")
	} else if verbose {
		fmt.Fprintf(out, "- resolvers installed: %s\n", strings.Join(resolved, ","))
	}
	if verbose && len(removed) > 0 {
		fmt.Fprintf(out, "- resolvers removed: %s\n", strings.Join(removed, ","))
	}
	if verbose {
		fmt.Fprintf(out, "- launch agent: %s\n", plistPath)
		fmt.Fprintf(out, "- loopback agent: %s\n", loopbackPath)
	}
	if err := writeBootstrapStateVersion(cfg.StateDir, bootstrapStateVersion); err != nil {
		logger.Warn("write bootstrap state failed", "error", err)
	}

	if verbose {
		return runDoctor(doctorRunOptions{Cfg: cfg, Platform: plat})
	}
	fmt.Fprintln(out, "next: run `dnsvard doctor`")
	return nil
}

func runLinuxUserDaemonPass(configPath string, targetUser string, binPath string, out io.Writer, errOut io.Writer) error {
	stopArgs := []string{}
	if strings.TrimSpace(configPath) != "" {
		stopArgs = append(stopArgs, "-c", configPath)
	}
	stopArgs = append(stopArgs, "daemon", "stop")
	stopCmd := exec.Command("sudo", append([]string{"-iu", targetUser, binPath}, stopArgs...)...)
	stopCmd.Stdout = nil
	stopCmd.Stderr = nil
	_ = stopCmd.Run()

	userArgs := []string{}
	if strings.TrimSpace(configPath) != "" {
		userArgs = append(userArgs, "-c", configPath)
	}
	userArgs = append(userArgs, "daemon", "restart")
	userCmd := exec.Command("sudo", append([]string{"-iu", targetUser, binPath}, userArgs...)...)
	userCmd.Stdin = os.Stdin
	userCmd.Stdout = out
	userCmd.Stderr = errOut
	return userCmd.Run()
}

func ensurePathOwnedByUser(logger *logx.Logger, path string, username string) error {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(username) == "" {
		return nil
	}
	if os.Geteuid() != 0 {
		return nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(strings.TrimSpace(u.Uid))
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(strings.TrimSpace(u.Gid))
	if err != nil {
		return err
	}
	paths, err := ownershipPathsNoSymlink(path)
	if err != nil {
		return err
	}
	auditPrivilegedOperation(logger, "chown_state_dir_tree_start", "path", path, "target_user", username, "entry_count", len(paths))
	for _, p := range paths {
		if err := os.Chown(p, uid, gid); err != nil {
			return err
		}
	}
	auditPrivilegedOperation(logger, "chown_state_dir_tree_complete", "path", path, "target_user", username, "entry_count", len(paths))
	return nil
}

func ownershipPathsNoSymlink(path string) ([]string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing ownership prep on symlinked root path %s", path)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("refusing ownership prep on non-directory path %s", path)
	}

	paths := []string{}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func stopExistingDaemon(cfg config.Config, plat platform.Controller) error {
	if err := plat.StopDaemon(); err != nil {
		// Best effort. We still try pid-based stop below.
	}
	if pid, err := daemon.ReadPID(cfg.StateDir); err == nil && daemon.ProcessRunning(pid) {
		if err := killPID(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop existing daemon process %d: %w", pid, err)
		}
		for range 30 {
			if !daemon.ProcessRunning(pid) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}

func runUninstall(logger *logx.Logger, cfg config.Config, plat platform.Controller, removeBinary bool) error {
	return runUninstallTo(os.Stdout, logger, cfg, plat, removeBinary)
}

func runUninstallTo(out io.Writer, logger *logx.Logger, cfg config.Config, plat platform.Controller, removeBinary bool) error {
	if out == nil {
		out = os.Stdout
	}

	if err := stopExistingDaemon(cfg, plat); err != nil {
		logger.Warn("daemon stop during uninstall failed", "error", err)
	}
	if err := stopAnyForegroundDaemons(); err != nil {
		logger.Warn("foreground daemon stop sweep failed", "error", err)
	}
	auditPrivilegedOperation(logger, "uninstall_platform_daemon", "state_dir", cfg.StateDir)
	if err := plat.UninstallDaemon(); err != nil {
		return err
	}
	auditPrivilegedOperation(logger, "uninstall_loopback_agent", "state_dir", cfg.StateDir)
	if err := plat.UninstallLoopbackAgent(); err != nil {
		logger.Warn("loopback agent uninstall failed", "error", err)
	}
	managed, err := plat.ListManagedResolvers()
	if err != nil {
		logger.Warn("managed resolver list failed during uninstall", "error", err)
	} else {
		for _, domain := range managed {
			spec, specErr := resolverSpecFromListen(cfg, domain)
			if specErr != nil {
				logger.Warn("skip resolver removal due to invalid spec", "domain", domain, "error", specErr)
				continue
			}
			if removeErr := plat.RemoveResolver(spec); removeErr != nil {
				logger.Warn("resolver removal during uninstall failed", "domain", domain, "error", removeErr)
			}
		}
	}
	if err := daemon.RemovePID(cfg.StateDir); err != nil {
		logger.Warn("failed removing pid file", "error", err)
	}
	if err := os.Remove(bootstrapStatePath(cfg.StateDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("bootstrap state cleanup failed", "error", err)
	}
	if err := maybeRevokePrivilegedHTTPBindCapability(logger, cfg.StateDir); err != nil {
		logger.Warn("http privileged bind capability cleanup failed", "error", err)
	}
	safeStateDir, safeErr := config.SafeStateDirPath(cfg.StateDir)
	if safeErr != nil {
		logger.Warn("state dir cleanup skipped: unsafe state_dir", "path", cfg.StateDir, "error", safeErr)
		fmt.Fprintf(out, "- skipped unsafe state directory removal: %s\n", cfg.StateDir)
		fmt.Fprintln(out, "- fix: set `state_dir` under your home directory ending with /dnsvard, then rerun uninstall")
	} else if err := os.RemoveAll(safeStateDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("state dir cleanup failed", "path", safeStateDir, "error", err)
	} else {
		auditPrivilegedOperation(logger, "uninstall_remove_state_dir", "path", safeStateDir)
	}
	fmt.Fprintln(out, "uninstall complete")
	fmt.Fprintln(out, "- removed dnsvard-managed resolvers")
	fmt.Fprintln(out, "- removed platform daemon registration")
	if safeErr == nil {
		fmt.Fprintf(out, "- removed state directory: %s\n", safeStateDir)
	}
	globalConfigPath := config.GlobalConfigPath()
	if info, statErr := os.Stat(globalConfigPath); statErr == nil {
		if !info.IsDir() {
			fmt.Fprintf(out, "- preserved global config: %s\n", globalConfigPath)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		logger.Warn("global config stat failed during uninstall", "path", globalConfigPath, "error", statErr)
	}

	exePath, exeErr := os.Executable()
	if exeErr != nil {
		logger.Warn("could not resolve executable path during uninstall", "error", exeErr)
		return nil
	}
	exePath = filepath.Clean(exePath)

	if !removeBinary {
		fmt.Fprintf(out, "- binary was not removed: %s\n", exePath)
		fmt.Fprintf(out, "- remove manually: rm %q\n", exePath)
		if isBrewManagedInstall(exePath) {
			fmt.Fprintln(out, "- Homebrew-managed install detected; remove with: brew uninstall --cask comment-slayer/tap/dnsvard")
		}
		return nil
	}

	if isBrewManagedInstall(exePath) {
		fmt.Fprintf(out, "- binary removal skipped (Homebrew-managed path): %s\n", exePath)
		fmt.Fprintln(out, "- remove with: brew uninstall --cask comment-slayer/tap/dnsvard")
		return nil
	}

	if err := os.Remove(exePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(out, "- binary already absent: %s\n", exePath)
			return nil
		}
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintf(out, "- failed to remove binary (permission denied): %s\n", exePath)
			fmt.Fprintf(out, "- try: sudo rm %q\n", exePath)
			return nil
		}
		fmt.Fprintf(out, "- failed to remove binary: %s (%v)\n", exePath, err)
		fmt.Fprintf(out, "- remove manually: rm %q\n", exePath)
		return nil
	}
	auditPrivilegedOperation(logger, "uninstall_remove_binary", "path", exePath)

	fmt.Fprintf(out, "- removed binary: %s\n", exePath)
	return nil
}

func stopAnyForegroundDaemons() error {
	processes, err := listProcessSnapshots()
	if err != nil {
		return err
	}
	identity, err := currentExecutableIdentity()
	if err != nil {
		return err
	}
	for _, pid := range selectOwnedForegroundDaemonPIDs(processes, os.Getpid(), os.Getuid(), identity) {
		_ = killPID(pid, syscall.SIGTERM)
	}
	return nil
}

type processSnapshot struct {
	PID     int
	UID     int
	Command string
}

type executableIdentity struct {
	BaseName string
	Device   uint64
	Inode    uint64
}

func listProcessSnapshots() ([]processSnapshot, error) {
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	processes := make([]processSnapshot, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		if pidErr != nil || uidErr != nil || pid <= 0 {
			continue
		}
		processes = append(processes, processSnapshot{
			PID:     pid,
			UID:     uid,
			Command: strings.Join(fields[2:], " "),
		})
	}
	return processes, nil
}

func currentExecutableIdentity() (executableIdentity, error) {
	exePath, err := os.Executable()
	if err != nil {
		return executableIdentity{}, fmt.Errorf("resolve executable for process cleanup: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return executableIdentity{}, fmt.Errorf("resolve executable symlink for process cleanup: %w", err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return executableIdentity{}, fmt.Errorf("stat executable for process cleanup: %w", err)
	}
	dev, inode, ok := fileDeviceInode(info)
	if !ok {
		return executableIdentity{}, errors.New("read executable identity for process cleanup")
	}
	base := filepath.Base(realPath)
	if base == "" {
		return executableIdentity{}, errors.New("resolve executable basename for process cleanup")
	}
	return executableIdentity{BaseName: base, Device: dev, Inode: inode}, nil
}

func selectOwnedForegroundDaemonPIDs(processes []processSnapshot, selfPID int, uid int, identity executableIdentity) []int {
	pids := make([]int, 0, len(processes))
	for _, p := range processes {
		if !isOwnedForegroundDaemonProcess(p, selfPID, uid, identity) {
			continue
		}
		pids = append(pids, p.PID)
	}
	sort.Ints(pids)
	return pids
}

func isOwnedForegroundDaemonProcess(p processSnapshot, selfPID int, uid int, identity executableIdentity) bool {
	if p.PID <= 0 || p.PID == selfPID || p.UID != uid {
		return false
	}
	args := strings.Fields(strings.TrimSpace(p.Command))
	if len(args) < 4 {
		return false
	}
	argv0 := trimOuterQuotes(args[0])
	if filepath.Base(argv0) != identity.BaseName {
		return false
	}
	if filepath.IsAbs(argv0) {
		info, err := os.Stat(argv0)
		if err != nil {
			return false
		}
		dev, inode, ok := fileDeviceInode(info)
		if !ok || dev != identity.Device || inode != identity.Inode {
			return false
		}
	}
	return hasForegroundDaemonSubcommand(args[1:])
}

func trimOuterQuotes(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 2 {
		return v
	}
	if (strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"")) || (strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'")) {
		return v[1 : len(v)-1]
	}
	return v
}

func hasForegroundDaemonSubcommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] != "daemon" {
			continue
		}
		if i+1 >= len(args) || args[i+1] != "start" {
			continue
		}
		for _, arg := range args[i+2:] {
			if arg == "--foreground" {
				return true
			}
		}
		return false
	}
	return false
}

func ensurePrivilegedHTTPBindCapability(logger *logx.Logger, cfg config.Config, binaryPath string, isRoot bool) error {
	if cfg.HTTPPort <= 0 || cfg.HTTPPort >= 1024 {
		return nil
	}
	if hasNetBindCapability(binaryPath) {
		return nil
	}
	if !isRoot {
		return fmt.Errorf("http_port=%d requires privileged bind capability on Linux\nfix: run `sudo dnsvard bootstrap` once", cfg.HTTPPort)
	}
	if _, err := exec.LookPath("setcap"); err != nil {
		return fmt.Errorf("http_port=%d requires `setcap` to grant bind capability\nfix: install libcap tools (for example `libcap2-bin`) and rerun bootstrap", cfg.HTTPPort)
	}
	auditPrivilegedOperation(logger, "setcap_grant_http_bind_start", "binary_path", binaryPath, "http_port", cfg.HTTPPort)
	cmd := exec.Command("setcap", "cap_net_bind_service=+ep", binaryPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("grant net bind capability on %s: %w (%s)", binaryPath, err, strings.TrimSpace(string(out)))
	}
	if !hasNetBindCapability(binaryPath) {
		return fmt.Errorf("failed to verify privileged bind capability on %s", binaryPath)
	}
	if err := writePrivilegedBindMarker(cfg.StateDir, binaryPath); err != nil {
		return err
	}
	auditPrivilegedOperation(logger, "setcap_grant_http_bind_complete", "binary_path", binaryPath, "http_port", cfg.HTTPPort)
	return nil
}

func hasNetBindCapability(binaryPath string) bool {
	getcapPath, err := exec.LookPath("getcap")
	if err != nil {
		return false
	}
	out, err := exec.Command(getcapPath, binaryPath).CombinedOutput()
	if err != nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(v, "cap_net_bind_service")
}

func privilegedBindMarkerPath(stateDir string) string {
	return filepath.Join(stateDir, "http-net-bind-capability.json")
}

type privilegedBindMarker struct {
	Version      int    `json:"version"`
	BinaryPath   string `json:"binary_path"`
	RealPath     string `json:"real_path"`
	Device       uint64 `json:"device"`
	Inode        uint64 `json:"inode"`
	DigestSHA256 string `json:"digest_sha256,omitempty"`
}

const privilegedBindMarkerVersion = 1

var (
	errPrivilegedBindMarkerStale      = errors.New("capability marker stale")
	errPrivilegedBindMarkerValidation = errors.New("capability marker validation failed")
)

func writePrivilegedBindMarker(stateDir string, binaryPath string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	marker, err := buildPrivilegedBindMarker(binaryPath)
	if err != nil {
		return err
	}
	b, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("marshal capability marker: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(privilegedBindMarkerPath(stateDir), b, 0o644); err != nil {
		return fmt.Errorf("write capability marker: %w", err)
	}
	return nil
}

func buildPrivilegedBindMarker(binaryPath string) (privilegedBindMarker, error) {
	binaryPath = filepath.Clean(strings.TrimSpace(binaryPath))
	if binaryPath == "" {
		return privilegedBindMarker{}, fmt.Errorf("build capability marker: %w", errPrivilegedBindMarkerValidation)
	}
	realPath, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return privilegedBindMarker{}, fmt.Errorf("build capability marker: resolve %s: %w", binaryPath, err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return privilegedBindMarker{}, fmt.Errorf("build capability marker: stat %s: %w", realPath, err)
	}
	dev, inode, ok := fileDeviceInode(info)
	if !ok {
		return privilegedBindMarker{}, fmt.Errorf("build capability marker: read inode metadata for %s", realPath)
	}
	digest, err := fileSHA256(realPath)
	if err != nil {
		return privilegedBindMarker{}, fmt.Errorf("build capability marker: digest %s: %w", realPath, err)
	}
	return privilegedBindMarker{
		Version:      privilegedBindMarkerVersion,
		BinaryPath:   binaryPath,
		RealPath:     realPath,
		Device:       dev,
		Inode:        inode,
		DigestSHA256: digest,
	}, nil
}

func maybeRevokePrivilegedHTTPBindCapability(logger *logx.Logger, stateDir string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	binaryPath, ok, err := resolvePrivilegedBindRevocationTarget(stateDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := exec.LookPath("setcap"); err != nil {
		return nil
	}
	auditPrivilegedOperation(logger, "setcap_revoke_http_bind_start", "binary_path", binaryPath)
	cmd := exec.Command("setcap", "-r", binaryPath)
	out, err := cmd.CombinedOutput()
	outText := strings.TrimSpace(string(out))
	if err != nil {
		if isNoCapabilityToRemoveError(outText) {
			return nil
		}
		return fmt.Errorf("revoke net bind capability on %s: %w (%s)", binaryPath, err, outText)
	}
	auditPrivilegedOperation(logger, "setcap_revoke_http_bind_complete", "binary_path", binaryPath)
	return nil
}

func resolvePrivilegedBindRevocationTarget(stateDir string) (string, bool, error) {
	markerPath := privilegedBindMarkerPath(stateDir)
	b, err := os.ReadFile(markerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	marker, err := parsePrivilegedBindMarker(b)
	if err != nil {
		return "", false, err
	}
	target, err := validatePrivilegedBindMarker(marker)
	if err != nil {
		if errors.Is(err, errPrivilegedBindMarkerStale) {
			_ = os.Remove(markerPath)
			return "", false, nil
		}
		return "", false, err
	}
	return target, true, nil
}

func parsePrivilegedBindMarker(b []byte) (privilegedBindMarker, error) {
	v := strings.TrimSpace(string(b))
	if v == "" {
		return privilegedBindMarker{}, fmt.Errorf("parse capability marker: %w", errPrivilegedBindMarkerValidation)
	}
	if !strings.HasPrefix(v, "{") {
		return privilegedBindMarker{BinaryPath: v}, nil
	}
	var marker privilegedBindMarker
	if err := json.Unmarshal([]byte(v), &marker); err != nil {
		return privilegedBindMarker{}, fmt.Errorf("parse capability marker: %w", err)
	}
	return marker, nil
}

func validatePrivilegedBindMarker(marker privilegedBindMarker) (string, error) {
	binaryPath := filepath.Clean(strings.TrimSpace(marker.BinaryPath))
	if binaryPath == "" {
		return "", fmt.Errorf("%w: missing binary path", errPrivilegedBindMarkerValidation)
	}
	realPath := filepath.Clean(strings.TrimSpace(marker.RealPath))
	if realPath == "" || marker.Device == 0 || marker.Inode == 0 {
		return "", fmt.Errorf("%w: missing inode/device metadata", errPrivilegedBindMarkerValidation)
	}
	resolvedPath, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errPrivilegedBindMarkerStale
		}
		return "", fmt.Errorf("%w: resolve binary path: %v", errPrivilegedBindMarkerValidation, err)
	}
	resolvedPath = filepath.Clean(resolvedPath)
	if resolvedPath != realPath {
		return "", fmt.Errorf("%w: realpath mismatch", errPrivilegedBindMarkerValidation)
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errPrivilegedBindMarkerStale
		}
		return "", fmt.Errorf("%w: stat target: %v", errPrivilegedBindMarkerValidation, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: target is not a regular file", errPrivilegedBindMarkerValidation)
	}
	dev, inode, ok := fileDeviceInode(info)
	if !ok {
		return "", fmt.Errorf("%w: read inode metadata", errPrivilegedBindMarkerValidation)
	}
	if dev != marker.Device || inode != marker.Inode {
		return "", fmt.Errorf("%w: inode/device mismatch", errPrivilegedBindMarkerValidation)
	}
	if strings.TrimSpace(marker.DigestSHA256) != "" {
		digest, digestErr := fileSHA256(resolvedPath)
		if digestErr != nil {
			return "", fmt.Errorf("%w: digest read failed", errPrivilegedBindMarkerValidation)
		}
		if !strings.EqualFold(strings.TrimSpace(marker.DigestSHA256), digest) {
			return "", fmt.Errorf("%w: digest mismatch", errPrivilegedBindMarkerValidation)
		}
	}
	return resolvedPath, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func auditPrivilegedOperation(logger *logx.Logger, operation string, keyvals ...any) {
	if logger == nil || strings.TrimSpace(operation) == "" {
		return
	}
	fields := make([]any, 0, len(keyvals)+2)
	fields = append(fields, "operation", operation)
	fields = append(fields, keyvals...)
	logger.Debug("audit privileged operation", fields...)
}

func isNoCapabilityToRemoveError(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return false
	}
	return strings.Contains(v, "no capab") && strings.Contains(v, "remove")
}
