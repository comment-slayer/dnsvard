package daemon

import (
	"errors"
	"testing"
)

func TestAcquireLockNonBlockingHeld(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	first, err := AcquireLock(stateDir, false)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	t.Cleanup(func() {
		_ = ReleaseLock(first)
	})

	second, err := AcquireLock(stateDir, false)
	if !errors.Is(err, ErrDaemonLockHeld) {
		t.Fatalf("expected ErrDaemonLockHeld, got %v", err)
	}
	if second != nil {
		t.Fatal("expected nil second lock handle")
	}
}
