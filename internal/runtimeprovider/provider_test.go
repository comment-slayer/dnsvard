package runtimeprovider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpsertAndRemoveLease(t *testing.T) {
	t.Parallel()

	p := New(filepath.Join(t.TempDir(), "state"))
	lease := Lease{ID: "abc", PID: 1, Hostnames: []string{"api.master.project.test"}}
	if err := p.Upsert(lease); err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}

	active, err := p.Active()
	if err != nil {
		t.Fatalf("Active returned error: %v", err)
	}
	if len(active) > 1 {
		t.Fatalf("unexpected active leases len: %d", len(active))
	}

	if err := p.Remove("abc"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
}

func TestUpsertReplacesExistingLeaseForSameHostname(t *testing.T) {
	t.Parallel()

	p := New(filepath.Join(t.TempDir(), "state"))
	pid := os.Getpid()
	if err := p.Upsert(Lease{ID: "old", PID: pid, Hostnames: []string{"www.dnsvard"}, HTTPPort: 11111}); err != nil {
		t.Fatalf("Upsert(old) returned error: %v", err)
	}
	if err := p.Upsert(Lease{ID: "new", PID: pid, Hostnames: []string{"www.dnsvard"}, HTTPPort: 22222}); err != nil {
		t.Fatalf("Upsert(new) returned error: %v", err)
	}

	active, err := p.Active()
	if err != nil {
		t.Fatalf("Active returned error: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active leases len = %d, want 1", len(active))
	}
	if active[0].ID != "new" {
		t.Fatalf("active lease ID = %q, want %q", active[0].ID, "new")
	}
	if active[0].HTTPPort != 22222 {
		t.Fatalf("active lease HTTPPort = %d, want 22222", active[0].HTTPPort)
	}
}
