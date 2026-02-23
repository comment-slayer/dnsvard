package daemon

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestWriteReadReconcileState(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	want := ReconcileState{
		PID:               1234,
		ConfigDomain:      "cs",
		ConfigHostPattern: "workspace-tld",
		WorkspaceDomains: map[string]string{
			"master@breadstick":     "master.bs",
			"master@comment-slayer": "master.cs",
		},
		UpdatedAt:       now,
		Sequence:        7,
		IntervalSeconds: 15,
		Cause:           "timer",
		Result:          "success",
		LastSuccessAt:   now,
		DNSAdded:        1,
		DNSRemoved:      2,
		HTTPAdded:       3,
		HTTPRemoved:     4,
		TCPAdded:        5,
		TCPRemoved:      6,
		Warnings:        2,
		SelfHealActions: map[string]SelfHealActionState{
			"resolver_drift_reconcile": {
				Component:         "resolver_drift",
				Detector:          "timer",
				Trigger:           "resolver_drift",
				Action:            "ensure_resolver",
				FailureCount:      3,
				LastFailureAt:     now,
				LastFailureDetail: "permission denied",
				BlockedUntil:      now.Add(5 * time.Minute),
			},
		},
	}

	if err := WriteReconcileState(stateDir, want); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}
	got, err := ReadReconcileState(stateDir)
	if err != nil {
		t.Fatalf("ReadReconcileState: %v", err)
	}
	if got.Version != ReconcileStateVersion {
		t.Fatalf("version = %d, want %d", got.Version, ReconcileStateVersion)
	}
	if got.PID != want.PID || got.Sequence != want.Sequence || got.Cause != want.Cause || got.Result != want.Result {
		t.Fatalf("unexpected basic fields: %+v", got)
	}
	if got.ConfigDomain != want.ConfigDomain || got.ConfigHostPattern != want.ConfigHostPattern {
		t.Fatalf("runtime config fields mismatch: %+v", got)
	}
	if len(got.WorkspaceDomains) != len(want.WorkspaceDomains) {
		t.Fatalf("workspace domains length mismatch: got=%d want=%d", len(got.WorkspaceDomains), len(want.WorkspaceDomains))
	}
	for key, value := range want.WorkspaceDomains {
		if got.WorkspaceDomains[key] != value {
			t.Fatalf("workspace domain mismatch for %q: got=%q want=%q", key, got.WorkspaceDomains[key], value)
		}
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("updated_at = %s, want %s", got.UpdatedAt, want.UpdatedAt)
	}
	if !got.LastSuccessAt.Equal(want.LastSuccessAt) {
		t.Fatalf("last_success_at = %s, want %s", got.LastSuccessAt, want.LastSuccessAt)
	}
	if got.DurationMS != want.DurationMS || got.TrackedConfigs != want.TrackedConfigs || got.WatchedDirs != want.WatchedDirs {
		t.Fatalf("extended fields mismatch: %+v", got)
	}
	gotResolver := got.SelfHealActions["resolver_drift_reconcile"]
	if gotResolver.FailureCount != 3 {
		t.Fatalf("failure_count = %d, want 3", gotResolver.FailureCount)
	}
	if !gotResolver.LastFailureAt.Equal(now) {
		t.Fatalf("last_failure_at = %s, want %s", gotResolver.LastFailureAt, now)
	}
	if !gotResolver.BlockedUntil.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("blocked_until = %s, want %s", gotResolver.BlockedUntil, now.Add(5*time.Minute))
	}
}

func TestWriteReconcileStateUsesAtomicRename(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := WriteReconcileState(stateDir, ReconcileState{PID: 1, Result: "success"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteReconcileState(stateDir, ReconcileState{PID: 2, Result: "failure"}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if _, err := os.Stat(ReconcileStatePath(stateDir) + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file should not exist after rename")
	}
}

func TestReadReconcileStateRejectsFutureVersion(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	content := []byte("{\n  \"version\": 999,\n  \"pid\": 1\n}\n")
	if err := os.WriteFile(ReconcileStatePath(stateDir), content, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	_, err := ReadReconcileState(stateDir)
	if err == nil {
		t.Fatal("expected error")
	}
	var versionErr UnsupportedReconcileStateVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReconcileStateSequenceMonotonicOnRewrite(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := WriteReconcileState(stateDir, ReconcileState{PID: 1, Sequence: 1, Result: "success"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteReconcileState(stateDir, ReconcileState{PID: 1, Sequence: 2, Result: "success"}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := ReadReconcileState(stateDir)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if got.Sequence < 2 {
		t.Fatalf("sequence = %d, want >= 2", got.Sequence)
	}
}
