package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/identity"
	"github.com/comment-slayer/dnsvard/internal/logx"
	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
)

func runRun(_ context.Context, logger *logx.Logger, cfg config.Config, args []string) error {
	_ = logger

	runArgs, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	if runArgs.Adapter == "" {
		if detected, reason := detectRunAdapter(runArgs.Command, cfg.Workspace.Path); detected != "" {
			runArgs.Adapter = detected
			fmt.Printf("- adapter auto-detected: %s (%s)\n", detected, reason)
		} else if likelyFrontendRunCommand(runArgs.Command) {
			return usageError(fmt.Sprintf("run: could not auto-detect adapter for command `%s`\nfix: pass one adapter explicitly with --adapter <%s> (or --vite/--next/...)\nexample: dnsvard run --adapter vite -- bun dev", strings.Join(runArgs.Command, " "), strings.Join(listRunAdapterNames(), "|")))
		}
	}
	adapter := selectRunAdapter(runArgs.Adapter)
	service := runArgs.Service
	cmd := runArgs.Command
	if !dnsvardDaemonRunning(cfg.StateDir) {
		fmt.Printf("- dnsvard daemon is not running; running command directly without dnsvard routing\n")
		fmt.Printf("- run `dnsvard bootstrap` to enable *.%s hostnames\n", cfg.Domain)
		direct := exec.Command(cmd[0], cmd[1:]...)
		direct.Stdin = os.Stdin
		direct.Stdout = os.Stdout
		direct.Stderr = os.Stderr
		direct.Env = os.Environ()
		return direct.Run()
	}

	workspace, err := identity.DeriveWorkspaceLabel(identity.WorkspaceInput{
		WorktreeBase: cfg.Workspace.WorktreeBase,
		Branch:       cfg.Workspace.Branch,
		CwdBase:      cfg.Workspace.CwdBase,
	})
	if err != nil {
		return err
	}

	project, err := identity.DeriveProjectLabel(identity.ProjectInput{
		RemoteURL: cfg.Project.RemoteURL,
		RepoBase:  cfg.Project.RepoBase,
	})
	if err != nil {
		return err
	}

	h, err := identity.Hostnames(identity.HostnameInput{
		Domain:    cfg.Domain,
		Project:   project,
		Workspace: workspace,
		Service:   service,
		Pattern:   cfg.HostPattern,
	})
	if err != nil {
		return err
	}

	hostnames := []string{h.WorkspaceFQDN}
	if h.ServiceFQDN != "" {
		hostnames = append(hostnames, h.ServiceFQDN)
	}
	publicHost := ""
	if len(hostnames) > 0 {
		publicHost = hostnames[0]
	}

	rp := runtimeprovider.New(cfg.StateDir)

	runtimePort, injectedPort, err := resolveRuntimeHTTPPort()
	if err != nil {
		return err
	}
	if adapter != nil && adapter.AdjustCmd != nil {
		cmd = adapter.AdjustCmd(cmd, runtimePort)
	}

	command := exec.Command(cmd[0], cmd[1:]...)
	command.Stdin = os.Stdin
	configureChildProcess(command)

	childEnv := os.Environ()
	if publicHost != "" {
		childEnv = append(childEnv,
			fmt.Sprintf("DNSVARD_HOST=%s", publicHost),
			fmt.Sprintf("DNSVARD_URL=http://%s", publicHost),
		)
	}
	if adapter != nil && adapter.ExtendEnv != nil && publicHost != "" {
		childEnv = adapter.ExtendEnv(childEnv, publicHost)
	}
	if injectedPort {
		childEnv = append(childEnv,
			fmt.Sprintf("PORT=%d", runtimePort),
			fmt.Sprintf("DNSVARD_HTTP_PORT=%d", runtimePort),
		)
	}
	command.Env = childEnv

	if adapter != nil && adapter.RewriteLine != nil && publicHost != "" {
		publicURL := "http://" + publicHost
		stdoutRewrite := newLineRewriteWriter(os.Stdout, func(line string) string {
			return adapter.RewriteLine(line, runtimePort, publicURL)
		})
		stderrRewrite := newLineRewriteWriter(os.Stderr, func(line string) string {
			return adapter.RewriteLine(line, runtimePort, publicURL)
		})
		defer func() { _ = stdoutRewrite.Flush() }()
		defer func() { _ = stderrRewrite.Flush() }()
		command.Stdout = stdoutRewrite
		command.Stderr = stderrRewrite
	} else {
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
	}

	if err := command.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- command.Wait()
	}()

	randomID, err := randomHex(6)
	if err != nil {
		_ = killProcessGroup(command.Process.Pid, syscall.SIGKILL)
		return err
	}
	leaseID := fmt.Sprintf("run-%s", randomID)
	lease := runtimeprovider.Lease{
		ID:        leaseID,
		PID:       command.Process.Pid,
		Hostnames: hostnames,
		Domain:    cfg.Domain,
		HTTPPort:  runtimePort,
	}
	if err := rp.Upsert(lease); err != nil {
		_ = killProcessGroup(command.Process.Pid, syscall.SIGKILL)
		return err
	}
	notifyDaemonReconcile(cfg.StateDir)
	fmt.Printf("- waiting for DNS propagation...\n")
	_ = waitForDNSRecords(hostnames, cfg.DNSListen, 2*time.Second)
	_ = waitForSystemDNS(hostnames, 3*time.Second)
	defer func() { _ = rp.Remove(leaseID) }()
	defer notifyDaemonReconcile(cfg.StateDir)

	fmt.Printf("runtime routes:\n")
	if injectedPort {
		fmt.Printf("- assigned PORT=%d for child process\n", runtimePort)
	}
	if adapter != nil {
		fmt.Printf("- adapter: %s\n", adapter.Name)
	}
	for _, host := range hostnames {
		fmt.Printf("- %s\n", host)
	}
	if len(hostnames) > 0 {
		fmt.Printf("- public url: http://%s\n", hostnames[0])
	}
	if lease.HTTPPort > 0 {
		fmt.Printf("- http proxy target: http://localhost:%d\n", lease.HTTPPort)
	}

	const gracefulShutdownWindow = 2 * time.Second
	const terminationWindow = 2 * time.Second

	var shutdownPhase int
	var escalationTimer *time.Timer
	defer func() {
		if escalationTimer != nil {
			escalationTimer.Stop()
		}
	}()

	startOrResetEscalationTimer := func(d time.Duration) {
		if escalationTimer == nil {
			escalationTimer = time.NewTimer(d)
			return
		}
		if !escalationTimer.Stop() {
			select {
			case <-escalationTimer.C:
			default:
			}
		}
		escalationTimer.Reset(d)
	}

	advanceShutdown := func(sig syscall.Signal) {
		switch shutdownPhase {
		case 0:
			_ = killProcessGroup(command.Process.Pid, sig)
			shutdownPhase = 1
			startOrResetEscalationTimer(gracefulShutdownWindow)
		case 1:
			_ = killProcessGroup(command.Process.Pid, syscall.SIGTERM)
			shutdownPhase = 2
			startOrResetEscalationTimer(terminationWindow)
		case 2:
			_ = killProcessGroup(command.Process.Pid, syscall.SIGKILL)
			shutdownPhase = 3
			if escalationTimer != nil {
				if !escalationTimer.Stop() {
					select {
					case <-escalationTimer.C:
					default:
					}
				}
			}
		}
	}

	for {
		var timerCh <-chan time.Time
		if escalationTimer != nil {
			timerCh = escalationTimer.C
		}

		select {
		case err := <-waitCh:
			if err != nil {
				return err
			}
			return nil
		case sig := <-sigCh:
			s, ok := sig.(syscall.Signal)
			if !ok {
				continue
			}
			advanceShutdown(s)
		case <-timerCh:
			if shutdownPhase == 1 {
				advanceShutdown(syscall.SIGTERM)
				continue
			}
			if shutdownPhase == 2 {
				advanceShutdown(syscall.SIGKILL)
			}
		}
	}
}

type runInvocation struct {
	Service string
	Adapter string
	Command []string
}

func parseRunArgs(args []string) (runInvocation, error) {
	if len(args) == 0 {
		return runInvocation{}, usageError("run expects command separator `--`\nusage: dnsvard run [service] [--vite|--next|--nuxt|--astro|--svelte|--webpack|--adapter <name>] -- <cmd...>\nexample: dnsvard run --vite -- bun dev")
	}

	separator := -1
	for i, a := range args {
		if a == "--" {
			separator = i
			break
		}
	}
	if separator == -1 {
		return runInvocation{}, usageError("run expects command separator `--`\nusage: dnsvard run [service] [--vite|--next|--nuxt|--astro|--svelte|--webpack|--adapter <name>] -- <cmd...>\nexample: dnsvard run --vite -- bun dev")
	}

	pre := args[:separator]
	cmd := args[separator+1:]
	if len(cmd) == 0 {
		return runInvocation{}, usageError("run expects a command after `--`\nusage: dnsvard run [service] [--adapter <name>] -- <cmd...>\nexample: dnsvard run api --adapter vite -- bun dev")
	}

	out := runInvocation{Command: cmd}
	for i := 0; i < len(pre); i++ {
		token := pre[i]
		if adapterName, ok := adapterNameForRunFlag(token); ok {
			if err := setRunAdapter(&out, adapterName); err != nil {
				return runInvocation{}, err
			}
			continue
		}

		switch token {
		case "--adapter":
			if i+1 >= len(pre) {
				return runInvocation{}, usageError(fmt.Sprintf("run: --adapter expects a value\nusage: --adapter <%s>", strings.Join(listRunAdapterNames(), "|")))
			}
			i++
			if err := setRunAdapter(&out, pre[i]); err != nil {
				return runInvocation{}, err
			}
		case "":
			continue
		default:
			if strings.HasPrefix(token, "--adapter=") {
				if err := setRunAdapter(&out, strings.TrimPrefix(token, "--adapter=")); err != nil {
					return runInvocation{}, err
				}
				continue
			}
			if strings.HasPrefix(token, "-") {
				return runInvocation{}, usageError(fmt.Sprintf("run: unknown option %q\nknown options: --vite --next --nuxt --astro --svelte --webpack --adapter <name>\nusage: dnsvard run [service] [options] -- <cmd...>", token))
			}
			if out.Service != "" {
				return runInvocation{}, usageError(fmt.Sprintf("run accepts at most one optional service before `--` (received %q and %q)\nusage: dnsvard run [service] [options] -- <cmd...>", out.Service, token))
			}
			out.Service = token
		}
	}

	return out, nil
}

func setRunAdapter(out *runInvocation, name string) error {
	v := strings.ToLower(strings.TrimSpace(name))
	if v == "" {
		return usageError(fmt.Sprintf("run: adapter name cannot be empty\nusage: --adapter <%s>", strings.Join(listRunAdapterNames(), "|")))
	}
	if !isKnownRunAdapter(v) {
		return unknownAdapterError(v)
	}
	if out.Adapter != "" && out.Adapter != v {
		return usageError(fmt.Sprintf("run: multiple adapters specified (%q and %q)\nchoose exactly one adapter per command", out.Adapter, v))
	}
	out.Adapter = v
	return nil
}

func killProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	err := killProcessGroupImpl(pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func dnsvardDaemonRunning(stateDir string) bool {
	pid, err := daemon.ReadPID(stateDir)
	if err != nil {
		return false
	}
	return daemon.ProcessRunning(pid)
}
