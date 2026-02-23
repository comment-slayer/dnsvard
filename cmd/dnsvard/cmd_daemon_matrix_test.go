package main

import (
	"strings"
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

func TestDaemonHealMatrixIntegrityTableDriven(t *testing.T) {
	t.Parallel()

	expected := map[healActionID]healActionSpec{
		healProxyReconcile: {
			component: "route_reconcile",
			detector:  healDetectorHTTPProxy,
			trigger:   "http_proxy_error",
			action:    "reconcile",
			cooldown:  0,
		},
		healHTTPRouterReset: {
			component: "http_router",
			detector:  healDetectorHTTPProxy,
			trigger:   "http_proxy_error",
			action:    "http_router_reset",
			cooldown:  2 * time.Minute,
		},
		healDockerWatchRestart: {
			component: "docker_watch",
			detector:  healDetectorDockerWatch,
			trigger:   "docker_watch",
			action:    "restart",
			cooldown:  5 * time.Second,
		},
		healPlatformDaemonRepair: {
			component: "platform_daemon",
			detector:  healDetectorTimer,
			trigger:   "platform_daemon",
			action:    "auto_repair",
			cooldown:  2 * time.Minute,
		},
		healResolverDriftReconcile: {
			component: "resolver_drift",
			detector:  healDetectorTimer,
			trigger:   "resolver_drift",
			action:    "ensure_resolver",
			cooldown:  30 * time.Second,
		},
		healHTTPRoutePromotion: {
			component: "http_router",
			detector:  healDetectorTimer,
			trigger:   "http_route_promotion",
			action:    "fallback_previous_target",
			cooldown:  0,
		},
	}

	if len(daemonHealMatrix) != len(expected) {
		t.Fatalf("matrix size = %d, want %d", len(daemonHealMatrix), len(expected))
	}

	for actionID, want := range expected {
		got, ok := daemonHealMatrix[actionID]
		if !ok {
			t.Fatalf("missing matrix action %s", actionID)
		}
		if got.component != want.component || got.detector != want.detector || got.trigger != want.trigger || got.action != want.action || got.cooldown != want.cooldown {
			t.Fatalf("matrix[%s] = %+v, want %+v", actionID, got, want)
		}
		if strings.TrimSpace(got.component) == "" || strings.TrimSpace(got.trigger) == "" || strings.TrimSpace(got.action) == "" || strings.TrimSpace(string(got.detector)) == "" {
			t.Fatalf("matrix[%s] has empty required fields: %+v", actionID, got)
		}
		if got.cooldown < 0 {
			t.Fatalf("matrix[%s] has invalid cooldown %s", actionID, got.cooldown)
		}
	}
}

func TestEachMatrixActionHasFixCodeAndTelemetryProblem(t *testing.T) {
	t.Parallel()

	for actionID, spec := range daemonHealMatrix {
		actionID := actionID
		spec := spec
		t.Run(string(actionID), func(t *testing.T) {
			t.Parallel()

			detail := "simulated self-heal failure"
			code, fixes := selfHealResolutionByAction(string(actionID), detail, config.Default(), platform.New())
			if strings.TrimSpace(code) == "" || code == "self_heal_unknown" {
				t.Fatalf("action %s produced non-deterministic code %q", actionID, code)
			}
			if len(fixes) == 0 {
				t.Fatalf("action %s produced no fixes", actionID)
			}

			state := daemon.ReconcileState{
				SelfHealActions: map[string]daemon.SelfHealActionState{
					string(actionID): {
						FailureCount:      1,
						LastFailureDetail: detail,
					},
				},
			}
			problems := selfHealProblemsFromState(state, config.Default(), platform.New())
			if len(problems) != 1 {
				t.Fatalf("len(problems) = %d, want 1", len(problems))
			}
			p := problems[0]
			if p.ActionID != string(actionID) {
				t.Fatalf("action_id = %q, want %q", p.ActionID, actionID)
			}
			if p.Code != code {
				t.Fatalf("problem code = %q, want %q", p.Code, code)
			}
			if p.Component != spec.component {
				t.Fatalf("component = %q, want %q", p.Component, spec.component)
			}
			if p.Message != detail {
				t.Fatalf("message = %q, want %q", p.Message, detail)
			}
			if p.FailureCount != 1 {
				t.Fatalf("failure_count = %d, want 1", p.FailureCount)
			}
			if len(p.Fixes) == 0 {
				t.Fatalf("action %s surfaced no fixes", actionID)
			}
		})
	}
}

func TestEverySurfacedSelfHealProblemHasActionableFixText(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := daemon.ReconcileState{SelfHealActions: map[string]daemon.SelfHealActionState{}}
	for actionID := range daemonHealMatrix {
		state.SelfHealActions[string(actionID)] = daemon.SelfHealActionState{
			FailureCount:      2,
			LastFailureDetail: "permission denied",
			BlockedUntil:      now.Add(5 * time.Minute),
		}
	}

	problems := selfHealProblemsFromState(state, config.Default(), platform.New())
	if len(problems) != len(daemonHealMatrix) {
		t.Fatalf("len(problems) = %d, want %d", len(problems), len(daemonHealMatrix))
	}
	for _, p := range problems {
		if len(p.Fixes) == 0 {
			t.Fatalf("problem %s has no fixes", p.ActionID)
		}
		for _, fix := range p.Fixes {
			if !isActionableFixText(fix) {
				t.Fatalf("problem %s has non-actionable fix text: %q", p.ActionID, fix)
			}
		}
	}
}

func TestPlainAndJSONSelfHealProblemAlignment(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	problem := selfHealProblem{
		ActionID:     string(healResolverDriftReconcile),
		Code:         "self_heal_resolver_reconcile_failed",
		Component:    "resolver_drift",
		Message:      "permission denied",
		FailureCount: 3,
		BlockedUntil: now,
		Fixes:        []string{"run `sudo dnsvard bootstrap --force`"},
	}

	lines := selfHealProblemLines(problem)
	if len(lines) < 3 {
		t.Fatalf("plain lines len = %d, want >= 3", len(lines))
	}
	if !strings.Contains(lines[0], "action_id="+problem.ActionID) || !strings.Contains(lines[0], "component="+problem.Component) || !strings.Contains(lines[0], "code="+problem.Code) {
		t.Fatalf("plain headline missing aligned fields: %q", lines[0])
	}

	jsonProblem := doctorProblemFromSelfHeal(problem)
	if jsonProblem.ActionID != problem.ActionID || jsonProblem.Component != problem.Component || jsonProblem.Code != problem.Code {
		t.Fatalf("json problem not aligned: %+v vs %+v", jsonProblem, problem)
	}
	if len(jsonProblem.Fixes) != len(problem.Fixes) || jsonProblem.Fixes[0] != problem.Fixes[0] {
		t.Fatalf("json fixes not aligned: %+v vs %+v", jsonProblem.Fixes, problem.Fixes)
	}
}

func isActionableFixText(fix string) bool {
	v := strings.ToLower(strings.TrimSpace(fix))
	if v == "" {
		return false
	}
	v = strings.TrimSpace(strings.TrimPrefix(v, "fix:"))
	actionPrefixes := []string{"run ", "set ", "check ", "ensure ", "verify ", "grant ", "inspect ", "stop ", "install ", "attach ", "use ", "automatic retries ", "if "}
	for _, prefix := range actionPrefixes {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return strings.Contains(v, "rerun") || strings.Contains(v, "dnsvard") || strings.Contains(v, "docker")
}
