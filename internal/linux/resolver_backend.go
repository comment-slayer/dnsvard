package linux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var serviceIsActive = func(name string) bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	return cmd.Run() == nil
}

type ResolverBackendKind string

const (
	ResolverBackendNone            ResolverBackendKind = "none"
	ResolverBackendSystemdResolved ResolverBackendKind = "systemd-resolved"
	ResolverBackendDnsmasq         ResolverBackendKind = "dnsmasq"
)

type ResolverBackend struct {
	Kind   ResolverBackendKind
	Reason string
}

func SelectResolverBackend(cap Capabilities) ResolverBackend {
	if forced := strings.ToLower(strings.TrimSpace(os.Getenv("DNSVARD_LINUX_RESOLVER_BACKEND"))); forced != "" {
		switch forced {
		case string(ResolverBackendSystemdResolved):
			return ResolverBackend{Kind: ResolverBackendSystemdResolved, Reason: "forced by DNSVARD_LINUX_RESOLVER_BACKEND"}
		case string(ResolverBackendDnsmasq):
			return ResolverBackend{Kind: ResolverBackendDnsmasq, Reason: "forced by DNSVARD_LINUX_RESOLVER_BACKEND"}
		case string(ResolverBackendNone):
			return ResolverBackend{Kind: ResolverBackendNone, Reason: "forced by DNSVARD_LINUX_RESOLVER_BACKEND"}
		default:
			return ResolverBackend{Kind: ResolverBackendNone, Reason: fmt.Sprintf("invalid forced resolver backend %q", forced)}
		}
	}

	if cap.Dnsmasq && cap.Systemctl && (serviceIsActive("dnsmasq") || serviceIsActive("NetworkManager")) {
		return ResolverBackend{Kind: ResolverBackendDnsmasq}
	}
	if cap.SystemdResolved && cap.Systemctl {
		return ResolverBackend{Kind: ResolverBackendSystemdResolved}
	}

	missing := []string{}
	if !cap.Systemctl {
		missing = append(missing, "systemctl")
	}
	if !cap.Dnsmasq {
		missing = append(missing, "dnsmasq")
	}
	if !cap.SystemdResolved {
		missing = append(missing, "resolvectl")
	}

	reason := "no supported resolver backend detected"
	if len(missing) > 0 {
		reason = fmt.Sprintf("missing required tools: %v", missing)
	}

	return ResolverBackend{
		Kind:   ResolverBackendNone,
		Reason: reason,
	}
}

func ResolverBackendFixHints(cap Capabilities, backend ResolverBackend) []string {
	hints := []string{}
	switch backend.Kind {
	case ResolverBackendSystemdResolved:
		hints = append(hints, "resolver backend systemd-resolved selected")
		hints = append(hints, "if bootstrap fails, ensure systemd-resolved service is active: sudo systemctl enable --now systemd-resolved")
		hints = append(hints, "if dnsmasq cannot bind port 53 on your host, this backend is often the better default")
	case ResolverBackendDnsmasq:
		hints = append(hints, "resolver backend dnsmasq selected")
		hints = append(hints, "dnsmasq backend supports non-53 dns_listen forwarding")
		hints = append(hints, "ensure dnsmasq (or NetworkManager dnsmasq integration) is active before bootstrap")
	default:
		hints = append(hints, "no Linux resolver backend is ready")
		if !cap.Systemctl {
			hints = append(hints, "install/enable systemd user services (`systemctl --user`) support")
		}
		if !cap.SystemdResolved {
			hints = append(hints, "install systemd-resolved (`resolvectl`) or use a dnsmasq-based setup")
		}
		if !cap.Dnsmasq {
			hints = append(hints, "install dnsmasq and start it: sudo systemctl enable --now dnsmasq")
		}
	}
	return hints
}
