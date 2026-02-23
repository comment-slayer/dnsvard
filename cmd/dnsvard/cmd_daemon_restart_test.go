package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

type testDaemonInstaller struct {
	installErr error
	called     bool
	spec       platform.DaemonSpec
}

func (t *testDaemonInstaller) InstallDaemon(spec platform.DaemonSpec) (string, error) {
	t.called = true
	t.spec = spec
	if t.installErr != nil {
		return "", t.installErr
	}
	return "/tmp/agent.plist", nil
}

func TestTryManagedDaemonRestartSuccess(t *testing.T) {
	t.Parallel()

	installer := &testDaemonInstaller{}
	waited := false
	restarted, err := tryManagedDaemonRestart(managedDaemonRestartRequest{
		Cfg:            config.Config{StateDir: "/tmp/state", Workspace: config.WorkspaceInfo{Path: "/tmp/work"}},
		Installer:      installer,
		ConfigPath:     "/tmp/dnsvard.yaml",
		Quiet:          true,
		ExecutablePath: func() (string, error) { return "/tmp/dnsvard", nil },
		WaitForPID: func(stateDir string, _ time.Duration) bool {
			if stateDir != "/tmp/state" {
				t.Fatalf("stateDir = %q", stateDir)
			}
			waited = true
			return true
		},
	})
	if err != nil {
		t.Fatalf("tryManagedDaemonRestart error: %v", err)
	}
	if !restarted {
		t.Fatal("expected restarted=true")
	}
	if !installer.called {
		t.Fatal("expected InstallDaemon call")
	}
	if !waited {
		t.Fatal("expected wait function call")
	}
	if installer.spec.WorkingDir != "/tmp/work" {
		t.Fatalf("working dir = %q", installer.spec.WorkingDir)
	}
}

func TestTryManagedDaemonRestartInstallError(t *testing.T) {
	t.Parallel()

	installer := &testDaemonInstaller{installErr: errors.New("boom")}
	restarted, err := tryManagedDaemonRestart(managedDaemonRestartRequest{
		Cfg:            config.Config{StateDir: "/tmp/state", Workspace: config.WorkspaceInfo{Path: "/tmp/work"}},
		Installer:      installer,
		ConfigPath:     "/tmp/dnsvard.yaml",
		Quiet:          true,
		ExecutablePath: func() (string, error) { return "/tmp/dnsvard", nil },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if restarted {
		t.Fatal("expected restarted=false")
	}
}

func TestTryManagedDaemonRestartSkipsGoRunExecutable(t *testing.T) {
	t.Parallel()

	installer := &testDaemonInstaller{}
	restarted, err := tryManagedDaemonRestart(managedDaemonRestartRequest{
		Cfg:            config.Config{StateDir: "/tmp/state", Workspace: config.WorkspaceInfo{Path: "/tmp/work"}},
		Installer:      installer,
		ConfigPath:     "/tmp/dnsvard.yaml",
		Quiet:          true,
		ExecutablePath: func() (string, error) { return "/tmp/go-build123/b001/exe/dnsvard", nil },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if restarted {
		t.Fatal("expected restarted=false")
	}
	if installer.called {
		t.Fatal("installer should not be called for go-run executable")
	}
}

func TestTryManagedDaemonRestartFailsWhenDaemonNeverComesUp(t *testing.T) {
	t.Parallel()

	installer := &testDaemonInstaller{}
	restarted, err := tryManagedDaemonRestart(managedDaemonRestartRequest{
		Cfg:            config.Config{StateDir: "/tmp/state", Workspace: config.WorkspaceInfo{Path: "/tmp/work"}},
		Installer:      installer,
		ConfigPath:     "/tmp/dnsvard.yaml",
		Quiet:          true,
		ExecutablePath: func() (string, error) { return "/tmp/dnsvard", nil },
		WaitForPID:     func(string, time.Duration) bool { return false },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if restarted {
		t.Fatal("expected restarted=false")
	}
	if !installer.called {
		t.Fatal("expected InstallDaemon call")
	}
}

func TestRunDaemonRestartWithDepsRequiresRunningDaemonAtEnd(t *testing.T) {
	t.Parallel()

	readCalls := 0
	err := runDaemonRestartWithDeps(daemonRestartRequest{
		Cfg:        config.Config{StateDir: "/tmp/state", Workspace: config.WorkspaceInfo{Path: "/tmp/work"}},
		ConfigPath: "/tmp/dnsvard.yaml",
		Quiet:      true,
		Deps: daemonRestartDeps{
			readPID: func(string) (int, error) {
				readCalls++
				return 0, errors.New("missing")
			},
			processRunning: func(int) bool { return false },
			killPID:        func(int) error { return nil },
			sleep:          func(time.Duration) {},
			runBackground:  func(config.Config, string, bool) error { return nil },
			waitForPID:     func(string, time.Duration) bool { return false },
		},
		ManagedRestart: func() (bool, error) { return false, nil },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "did not result in a running daemon") {
		t.Fatalf("error = %q", err)
	}
	if readCalls < 2 {
		t.Fatalf("readPID calls = %d, want >= 2", readCalls)
	}
}

func TestRunDaemonRestartWithDepsDoesNotKillFreshManagedDaemon(t *testing.T) {
	t.Parallel()

	states := []daemonProcessState{{pid: 101, running: true}, {pid: 202, running: true}}
	stateCall := 0
	readPID := func(string) (int, error) {
		idx := stateCall
		if idx >= len(states) {
			idx = len(states) - 1
		}
		stateCall++
		return states[idx].pid, nil
	}

	killed := []int{}
	startedBackground := false
	err := runDaemonRestartWithDeps(daemonRestartRequest{
		Cfg:        config.Config{StateDir: "/tmp/state", Workspace: config.WorkspaceInfo{Path: "/tmp/work"}},
		ConfigPath: "/tmp/dnsvard.yaml",
		Quiet:      true,
		Deps: daemonRestartDeps{
			readPID: readPID,
			processRunning: func(pid int) bool {
				for _, st := range states {
					if st.pid == pid {
						return st.running
					}
				}
				return false
			},
			killPID: func(pid int) error {
				killed = append(killed, pid)
				return nil
			},
			sleep: func(time.Duration) {},
			runBackground: func(config.Config, string, bool) error {
				startedBackground = true
				return nil
			},
			waitForPID: func(string, time.Duration) bool { return true },
		},
		ManagedRestart: func() (bool, error) { return false, fmt.Errorf("install returned transient error") },
	})
	if err != nil {
		t.Fatalf("runDaemonRestartWithDeps error: %v", err)
	}
	if len(killed) != 0 {
		t.Fatalf("expected no kill, got %v", killed)
	}
	if startedBackground {
		t.Fatal("expected no direct background start when fresh daemon is already running")
	}
}

func TestStopStaleDaemonProcessesWithTargetsForegroundCleanupOnly(t *testing.T) {
	t.Parallel()

	called := 0
	err := stopStaleDaemonProcessesWith(func() error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("stopStaleDaemonProcessesWith error: %v", err)
	}
	if called != 1 {
		t.Fatalf("cleanup calls = %d, want 1", called)
	}
}
