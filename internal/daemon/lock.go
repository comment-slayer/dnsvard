//go:build darwin || linux

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrDaemonLockHeld = errors.New("daemon lock held")

func LockPath(stateDir string) string {
	return filepath.Join(stateDir, "daemon.lock")
}

func AcquireLock(stateDir string, wait bool) (*os.File, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	lockPath := LockPath(stateDir)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock file: %w", err)
	}
	flags := syscall.LOCK_EX
	if !wait {
		flags |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(f.Fd()), flags)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			if !wait {
				_ = f.Close()
				return nil, ErrDaemonLockHeld
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_ = f.Close()
		return nil, fmt.Errorf("acquire daemon lock: %w", err)
	}
}

func ReleaseLock(f *os.File) error {
	if f == nil {
		return nil
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		_ = f.Close()
		return fmt.Errorf("release daemon lock: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close daemon lock file: %w", err)
	}
	return nil
}
