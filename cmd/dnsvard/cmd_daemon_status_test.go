package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/daemon"
)

func TestFormatReconcileStateSummaryStaleByPIDMismatch(t *testing.T) {
	t.Parallel()

	summary, stale, reason := formatReconcileStateSummary(daemon.ReconcileState{
		UpdatedAt:       time.Now(),
		IntervalSeconds: 15,
		PID:             100,
		Cause:           "timer",
		Result:          "success",
	}, 200)
	if !stale {
		t.Fatal("expected stale")
	}
	if reason != "pid_mismatch" {
		t.Fatalf("reason = %q", reason)
	}
	if !strings.Contains(summary, "cause=timer") {
		t.Fatalf("missing cause in summary: %s", summary)
	}
}

func TestFormatReconcileStateSummaryStaleByAge(t *testing.T) {
	t.Parallel()

	summary, stale, reason := formatReconcileStateSummary(daemon.ReconcileState{
		UpdatedAt:       time.Now().Add(-40 * time.Second),
		IntervalSeconds: 15,
		PID:             100,
		Cause:           "timer",
		Result:          "success",
	}, 100)
	if !stale {
		t.Fatal("expected stale")
	}
	if !strings.HasPrefix(reason, "age=") {
		t.Fatalf("reason = %q", reason)
	}
	if !strings.Contains(summary, "result=success") {
		t.Fatalf("missing result in summary: %s", summary)
	}
}

func TestTruncateReconcileError(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("x", 500)
	out := truncateReconcileError(input)
	if len(out) > 403 {
		t.Fatalf("length=%d", len(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("missing suffix: %q", out)
	}
}

func TestShouldLogReconcileApplied(t *testing.T) {
	t.Parallel()

	if shouldLogReconcileApplied(reconcileAppliedSummary{}) {
		t.Fatal("expected no log for zero deltas and zero warnings")
	}
	if !shouldLogReconcileApplied(reconcileAppliedSummary{DNSAdded: 1}) {
		t.Fatal("expected log when dns delta exists")
	}
	if !shouldLogReconcileApplied(reconcileAppliedSummary{Warnings: 1}) {
		t.Fatal("expected log when warnings exist")
	}
}

func TestStartupBuildRoutesErrorIncludesActionableFix(t *testing.T) {
	t.Parallel()

	err := startupBuildRoutesError(errors.New("workspace scope requires project/workspace labels"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "startup problem: config_scope_labels") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "fix:") {
		t.Fatalf("expected fix lines in error: %v", err)
	}
}

func TestResolverStatusFailureDetailPermissionDeniedIncludesRepair(t *testing.T) {
	t.Parallel()

	detail := resolverStatusFailureDetail(errors.New("permission denied while reading resolver state"))
	if !strings.Contains(detail, "bootstrap") {
		t.Fatalf("detail should include bootstrap repair instruction: %q", detail)
	}
}
