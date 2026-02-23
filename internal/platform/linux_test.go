//go:build linux

package platform

import (
	"strings"
	"testing"
)

func TestLinuxPlatformCapabilities(t *testing.T) {
	t.Parallel()

	c := linuxController{}
	if c.SupportsLocalNetworkProbe() {
		t.Fatal("linux should not require local network probe")
	}
	if !c.BootstrapRequiresRootTargetUser() {
		t.Fatal("linux should require target user for root bootstrap")
	}
	if !c.BootstrapSupportsPrivilegedPortCapability() {
		t.Fatal("linux should support privileged port capability")
	}
	if !c.BootstrapHasRootResolverOnlyPass() {
		t.Fatal("linux should support root resolver-only bootstrap pass")
	}
	if c.ResolverAutoHealDelegated() {
		t.Fatal("linux resolver auto-heal should not be delegated")
	}
	if !c.DaemonCanAutoHealResolvers() {
		t.Fatal("linux daemon should auto-heal resolvers")
	}
}

func TestLinuxBootstrapVerboseNote(t *testing.T) {
	t.Parallel()

	c := linuxController{}
	if got := c.BootstrapVerboseNote(false, ""); !strings.Contains(got, "non-root") {
		t.Fatalf("unexpected non-root note: %q", got)
	}
	if got := c.BootstrapVerboseNote(true, "steeve"); !strings.Contains(got, "steeve") {
		t.Fatalf("unexpected root note: %q", got)
	}
}

func TestLinuxStatusDetails(t *testing.T) {
	t.Parallel()

	c := linuxController{}
	details := c.DaemonStatusDetails("/tmp/state", "active (running)\nextra", false)
	joined := strings.Join(details, "\n")
	if !strings.Contains(joined, "systemd user unit: dnsvard.service") {
		t.Fatalf("missing daemon details: %v", details)
	}
	if !strings.Contains(joined, "daemon summary: active (running)") {
		t.Fatalf("missing daemon summary: %v", details)
	}

	loopDetails := c.LoopbackStatusDetails("/tmp/state", "inline")
	loopJoined := strings.Join(loopDetails, "\n")
	if !strings.Contains(loopJoined, "/tmp/state/resolver-sync-state.json") {
		t.Fatalf("missing resolver sync path: %v", loopDetails)
	}
}
