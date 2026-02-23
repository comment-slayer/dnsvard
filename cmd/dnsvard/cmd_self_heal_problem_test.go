package main

import (
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

func TestSelfHealProblemsFromStateActionMap(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	state := daemon.ReconcileState{
		SelfHealActions: map[string]daemon.SelfHealActionState{
			string(healPlatformDaemonRepair): {
				Component:         "platform_daemon",
				FailureCount:      3,
				LastFailureDetail: "daemon manager install failed",
				BlockedUntil:      now.Add(10 * time.Minute),
			},
			string(healResolverDriftReconcile): {
				Component:         "resolver_drift",
				FailureCount:      1,
				LastFailureDetail: "permission denied",
			},
		},
	}

	problems := selfHealProblemsFromState(state, config.Default(), platform.New())
	if len(problems) != 2 {
		t.Fatalf("len(problems) = %d, want 2", len(problems))
	}
	if problems[0].ActionID != string(healPlatformDaemonRepair) {
		t.Fatalf("problems[0].action_id = %q", problems[0].ActionID)
	}
	if problems[0].Code != "self_heal_platform_daemon_repair_failed" {
		t.Fatalf("problems[0].code = %q", problems[0].Code)
	}
	if problems[0].BlockedUntil.IsZero() {
		t.Fatal("problems[0].blocked_until should be set")
	}
	if len(problems[0].Fixes) == 0 {
		t.Fatal("problems[0] should include fixes")
	}
	if problems[1].ActionID != string(healResolverDriftReconcile) {
		t.Fatalf("problems[1].action_id = %q", problems[1].ActionID)
	}
	if problems[1].Code != "self_heal_resolver_reconcile_failed" {
		t.Fatalf("problems[1].code = %q", problems[1].Code)
	}
	if len(problems[1].Fixes) == 0 {
		t.Fatal("problems[1] should include fixes")
	}
}

func TestSelfHealProblemsFromStateLegacyFallback(t *testing.T) {
	t.Parallel()

	state := daemon.ReconcileState{
		SelfHealFailed:       true,
		SelfHealActionID:     string(healResolverDriftReconcile),
		SelfHealComponent:    "resolver_drift",
		SelfHealDetail:       "permission denied",
		SelfHealFailureCount: 2,
	}

	problems := selfHealProblemsFromState(state, config.Default(), platform.New())
	if len(problems) != 1 {
		t.Fatalf("len(problems) = %d, want 1", len(problems))
	}
	if problems[0].Code != "self_heal_resolver_reconcile_failed" {
		t.Fatalf("code = %q", problems[0].Code)
	}
	if len(problems[0].Fixes) == 0 {
		t.Fatal("expected at least one fix")
	}
}

func TestSelfHealResolutionByActionDeterministicCodeAndFixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		actionID string
		wantCode string
	}{
		{actionID: string(healProxyReconcile), wantCode: "self_heal_proxy_reconcile_failed"},
		{actionID: string(healHTTPRouterReset), wantCode: "self_heal_http_router_reset_failed"},
		{actionID: string(healDockerWatchRestart), wantCode: "self_heal_docker_watch_failed"},
		{actionID: string(healPlatformDaemonRepair), wantCode: "self_heal_platform_daemon_repair_failed"},
		{actionID: string(healResolverDriftReconcile), wantCode: "self_heal_resolver_reconcile_failed"},
		{actionID: string(healHTTPRoutePromotion), wantCode: "self_heal_http_route_promotion_failed"},
		{actionID: "unknown_action", wantCode: "self_heal_unknown"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.actionID, func(t *testing.T) {
			t.Parallel()
			code, fixes := selfHealResolutionByAction(tc.actionID, "permission denied", config.Default(), platform.New())
			if code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
			if len(fixes) == 0 {
				t.Fatal("expected at least one fix")
			}
		})
	}
}
