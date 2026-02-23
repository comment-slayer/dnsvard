package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/httprouter"
)

func TestHealCoordinatorDetectorMismatch(t *testing.T) {
	t.Parallel()

	h := newHealCoordinator()
	called := 0
	ok := h.emit(healEvent{
		detector: healDetectorTimer,
		actionID: healHTTPRouterReset,
		detail:   "mismatch",
	}, func(healActionSpec, healEvent) {
		called++
	})
	if ok {
		t.Fatal("expected detector mismatch to reject event")
	}
	if called != 0 {
		t.Fatalf("markSelfHeal called %d times, want 0", called)
	}
}

func TestHealCoordinatorCooldown(t *testing.T) {
	t.Parallel()

	h := newHealCoordinator()
	called := 0
	event := healEvent{
		detector: healDetectorHTTPProxy,
		actionID: healHTTPRouterReset,
		detail:   "unreachable",
	}
	if ok := h.emit(event, func(healActionSpec, healEvent) { called++ }); !ok {
		t.Fatal("expected first event to be accepted")
	}
	if ok := h.emit(event, func(healActionSpec, healEvent) { called++ }); ok {
		t.Fatal("expected second event to be suppressed by cooldown")
	}
	if called != 1 {
		t.Fatalf("markSelfHeal called %d times, want 1", called)
	}
}

func TestDetectPersistentProxyUnreachable(t *testing.T) {
	t.Parallel()

	state := &proxyUnreachableBurst{}
	base := time.Now()
	for i := 0; i < 5; i++ {
		if detectPersistentProxyUnreachable(state, base.Add(time.Duration(i)*time.Second)) {
			t.Fatal("unexpected escalation before threshold")
		}
	}
	if !detectPersistentProxyUnreachable(state, base.Add(6*time.Second)) {
		t.Fatal("expected escalation at threshold")
	}
	if detectPersistentProxyUnreachable(state, base.Add(7*time.Second)) {
		t.Fatal("expected cooldown suppression")
	}
}

func TestExecuteHTTPRouterResetRestoresHealthyRoute(t *testing.T) {
	t.Parallel()

	r := httprouter.New()
	if err := r.SetRoutes([]httprouter.Route{{Hostname: "feat-1.cs", Target: "http://127.0.0.1:65534"}}); err != nil {
		t.Fatalf("SetRoutes initial failed: %v", err)
	}
	addr := startRouterOnEphemeralPort(t, r)
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "feat-1.cs"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("initial request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	_ = resp.Body.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	if err := executeHTTPRouterReset(r, addr, []httprouter.Route{{Hostname: "feat-1.cs", Target: backend.URL}}); err != nil {
		t.Fatalf("executeHTTPRouterReset failed: %v", err)
	}
	req2, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req2.Host = "feat-1.cs"
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("request after reset failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestIncidentPersistentUnreachableUsesTargetedResetAndRecovers(t *testing.T) {
	t.Parallel()

	r := httprouter.New()
	if err := r.SetRoutes([]httprouter.Route{{Hostname: "feat-incident.cs", Target: "http://127.0.0.1:65534"}}); err != nil {
		t.Fatalf("SetRoutes initial failed: %v", err)
	}
	addr := startRouterOnEphemeralPort(t, r)
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "feat-incident.cs"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("initial request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	_ = resp.Body.Close()

	healing := newHealCoordinator()
	b := proxyUnreachableBurst{}
	reconcileTrigger := make(chan struct{}, 8)
	httpResetTrigger := make(chan struct{}, 8)
	actionCounts := map[healActionID]int{}
	mark := func(_ healActionSpec, event healEvent) {
		actionCounts[event.actionID]++
	}

	emitIncident := func(now time.Time) {
		err := errors.New("dial tcp 10.0.0.4:80: connect: no route to host")
		if !isRouteUnreachableError("", err) {
			t.Fatal("expected no-route error to be considered unreachable")
		}
		shouldReset := detectPersistentProxyUnreachable(&b, now)
		healing.emit(healEvent{detector: healDetectorHTTPProxy, actionID: healProxyReconcile, detail: err.Error()}, mark)
		executeRouteReconcile(reconcileTrigger)
		if shouldReset {
			select {
			case httpResetTrigger <- struct{}{}:
			default:
			}
		}
	}

	base := time.Now()
	for i := 0; i < 6; i++ {
		emitIncident(base.Add(time.Duration(i) * time.Second))
	}
	if len(reconcileTrigger) == 0 {
		t.Fatal("expected at least one immediate reconcile trigger")
	}
	if len(httpResetTrigger) != 1 {
		t.Fatalf("expected one targeted router reset trigger, got %d", len(httpResetTrigger))
	}

	emitIncident(base.Add(7 * time.Second))
	if len(httpResetTrigger) != 1 {
		t.Fatal("expected cooldown behavior to suppress additional router reset triggers")
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	if !healing.emit(healEvent{detector: healDetectorHTTPProxy, actionID: healHTTPRouterReset, detail: "persistent upstream unreachable"}, mark) {
		t.Fatal("expected first targeted router reset action to be accepted")
	}
	if healing.emit(healEvent{detector: healDetectorHTTPProxy, actionID: healHTTPRouterReset, detail: "persistent upstream unreachable"}, mark) {
		t.Fatal("expected targeted router reset action cooldown suppression")
	}

	if err := executeHTTPRouterReset(r, addr, []httprouter.Route{{Hostname: "feat-incident.cs", Target: backend.URL}}); err != nil {
		t.Fatalf("executeHTTPRouterReset failed: %v", err)
	}
	checkReq, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	checkReq.Host = "feat-incident.cs"
	checkResp, err := client.Do(checkReq)
	if err != nil {
		t.Fatalf("request after incident reset failed: %v", err)
	}
	defer checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", checkResp.StatusCode, http.StatusOK)
	}

	if actionCounts[healPlatformDaemonRepair] != 0 {
		t.Fatalf("unexpected daemon-wide repair action count: %d", actionCounts[healPlatformDaemonRepair])
	}
	if actionCounts[healHTTPRouterReset] != 1 {
		t.Fatalf("expected exactly one targeted router reset action, got %d", actionCounts[healHTTPRouterReset])
	}
}

func startRouterOnEphemeralPort(t *testing.T, r *httprouter.Router) string {
	t.Helper()
	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen failed: %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		if err := r.Start(addr); err == nil {
			return addr
		} else if !isAddressInUseError(err) {
			t.Fatalf("router start failed: %v", err)
		}
	}
	t.Fatal("failed to start router on ephemeral port after retries")
	return ""
}
