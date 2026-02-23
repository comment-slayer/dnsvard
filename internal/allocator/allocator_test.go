package allocator

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAllocateIsStableAcrossReload(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "allocator.json")
	a, err := New("127.90.0.0/16", statePath)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ip1, err := a.Allocate("ws-a")
	if err != nil {
		t.Fatalf("Allocate 1 returned error: %v", err)
	}

	b, err := New("127.90.0.0/16", statePath)
	if err != nil {
		t.Fatalf("New 2 returned error: %v", err)
	}

	ip2, err := b.Allocate("ws-a")
	if err != nil {
		t.Fatalf("Allocate 2 returned error: %v", err)
	}

	if ip1 != ip2 {
		t.Fatalf("stable allocation mismatch: %s vs %s", ip1, ip2)
	}
}

func TestCollisionAvoidanceAndCooldown(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "allocator.json")
	a, err := New("127.91.0.0/30", statePath)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	now := time.Date(2026, time.February, 23, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	a.cooldown = 30 * time.Minute

	ip1, err := a.Allocate("ws-a")
	if err != nil {
		t.Fatalf("Allocate ws-a returned error: %v", err)
	}
	if err := a.Release("ws-a"); err != nil {
		t.Fatalf("Release ws-a returned error: %v", err)
	}

	ip2, err := a.Allocate("ws-b")
	if err != nil {
		t.Fatalf("Allocate ws-b returned error: %v", err)
	}

	if ip1 == ip2 {
		t.Fatalf("expected cooldown to avoid immediate IP reuse, got same IP %s", ip2)
	}

	now = now.Add(31 * time.Minute)
	ip3, err := a.Allocate("ws-c")
	if err != nil {
		t.Fatalf("Allocate ws-c returned error: %v", err)
	}

	if ip3 != ip1 {
		t.Fatalf("expected reuse after cooldown, got %s want %s", ip3, ip1)
	}
}
