package linux

import (
	"strings"
	"testing"
)

func TestSelectResolverBackend(t *testing.T) {
	prev := serviceIsActive
	serviceIsActive = func(_ string) bool { return true }
	t.Cleanup(func() { serviceIsActive = prev })

	t.Run("systemd resolved selected when dnsmasq missing", func(t *testing.T) {
		got := SelectResolverBackend(Capabilities{Systemctl: true, SystemdResolved: true})
		if got.Kind != ResolverBackendSystemdResolved {
			t.Fatalf("backend kind = %s, want %s", got.Kind, ResolverBackendSystemdResolved)
		}
	})

	t.Run("dnsmasq selected when available", func(t *testing.T) {
		got := SelectResolverBackend(Capabilities{Systemctl: true, SystemdResolved: true, Dnsmasq: true})
		if got.Kind != ResolverBackendDnsmasq {
			t.Fatalf("backend kind = %s, want %s", got.Kind, ResolverBackendDnsmasq)
		}
	})

	t.Run("fallback to systemd when dnsmasq inactive", func(t *testing.T) {
		prev := serviceIsActive
		serviceIsActive = func(name string) bool {
			return name == "systemd-resolved"
		}
		t.Cleanup(func() { serviceIsActive = prev })

		got := SelectResolverBackend(Capabilities{Systemctl: true, SystemdResolved: true, Dnsmasq: true})
		if got.Kind != ResolverBackendSystemdResolved {
			t.Fatalf("backend kind = %s, want %s", got.Kind, ResolverBackendSystemdResolved)
		}
	})

	t.Run("dnsmasq selected", func(t *testing.T) {
		got := SelectResolverBackend(Capabilities{Systemctl: true, Dnsmasq: true})
		if got.Kind != ResolverBackendDnsmasq {
			t.Fatalf("backend kind = %s, want %s", got.Kind, ResolverBackendDnsmasq)
		}
	})

	t.Run("none when missing tools", func(t *testing.T) {
		got := SelectResolverBackend(Capabilities{Systemctl: false, SystemdResolved: false})
		if got.Kind != ResolverBackendNone {
			t.Fatalf("backend kind = %s, want %s", got.Kind, ResolverBackendNone)
		}
		if got.Reason == "" {
			t.Fatal("expected non-empty reason")
		}
	})
}

func TestSelectResolverBackendForced(t *testing.T) {
	prev := serviceIsActive
	serviceIsActive = func(_ string) bool { return false }
	t.Cleanup(func() { serviceIsActive = prev })
	t.Setenv("DNSVARD_LINUX_RESOLVER_BACKEND", "dnsmasq")
	got := SelectResolverBackend(Capabilities{Systemctl: true, SystemdResolved: true, Dnsmasq: false})
	if got.Kind != ResolverBackendDnsmasq {
		t.Fatalf("forced backend kind = %s, want %s", got.Kind, ResolverBackendDnsmasq)
	}
}

func TestSelectResolverBackendForcedSystemd(t *testing.T) {
	prev := serviceIsActive
	serviceIsActive = func(_ string) bool { return false }
	t.Cleanup(func() { serviceIsActive = prev })
	t.Setenv("DNSVARD_LINUX_RESOLVER_BACKEND", "systemd-resolved")
	got := SelectResolverBackend(Capabilities{Systemctl: true, SystemdResolved: true, Dnsmasq: true})
	if got.Kind != ResolverBackendSystemdResolved {
		t.Fatalf("forced backend kind = %s, want %s", got.Kind, ResolverBackendSystemdResolved)
	}
}

func TestResolverBackendFixHints(t *testing.T) {
	t.Parallel()

	t.Run("none backend includes actionable hints", func(t *testing.T) {
		t.Parallel()
		hints := ResolverBackendFixHints(
			Capabilities{Systemctl: false, SystemdResolved: false, Dnsmasq: false},
			ResolverBackend{Kind: ResolverBackendNone, Reason: "missing"},
		)
		if len(hints) < 2 {
			t.Fatalf("expected multiple hints, got %v", hints)
		}
	})

	t.Run("dnsmasq backend includes status hint", func(t *testing.T) {
		t.Parallel()
		hints := ResolverBackendFixHints(
			Capabilities{Systemctl: true, Dnsmasq: true},
			ResolverBackend{Kind: ResolverBackendDnsmasq},
		)
		if len(hints) == 0 {
			t.Fatal("expected non-empty hints")
		}
		joined := strings.Join(hints, "\n")
		if !strings.Contains(joined, "dnsmasq") {
			t.Fatalf("expected dns_listen hint, got: %v", hints)
		}
	})
}
