//go:build darwin

package platform

import (
	"strings"
	"testing"
)

func TestDarwinDaemonStatusDegraded(t *testing.T) {
	t.Parallel()

	c := darwinController{}
	if !c.DaemonStatusDegraded("state = spawn scheduled") {
		t.Fatal("expected degraded for spawn scheduled")
	}
	if !c.DaemonStatusDegraded("last exit code = 1") {
		t.Fatal("expected degraded for last exit code")
	}
	if c.DaemonStatusDegraded("state = running") {
		t.Fatal("unexpected degraded for healthy state")
	}
}

func TestDarwinStatusDetails(t *testing.T) {
	t.Parallel()

	c := darwinController{}
	details := c.DaemonStatusDetails("/tmp/state", "service summary\nextra", true)
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "launchd label: dev.dnsvard.daemon") {
		t.Fatalf("missing launchd label details: %v", details)
	}
	if !strings.Contains(joined, "launch daemon note") {
		t.Fatalf("missing degraded note: %v", details)
	}

	loopDetails := c.LoopbackStatusDetails("/tmp/state", "ok")
	loopJoined := strings.Join(loopDetails, "\n")
	if !strings.Contains(loopJoined, "/tmp/state/resolver-sync-state.json") {
		t.Fatalf("missing resolver sync state path: %v", loopDetails)
	}
}
