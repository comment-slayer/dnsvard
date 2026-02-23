//go:build linux

package platform

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/linux"
	"github.com/comment-slayer/dnsvard/internal/textutil"
)

type linuxController struct {
	cap      linux.Capabilities
	resolver linux.ResolverBackend
}

var systemServiceIsActive = func(name string) error {
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("service %s is not active (%s)", name, strings.TrimSpace(string(out)))
	}
	return nil
}

func New() Controller {
	cap := linux.DetectCapabilities()
	return linuxController{cap: cap, resolver: linux.SelectResolverBackend(cap)}
}

func (linuxController) Name() string {
	return "linux"
}

func (linuxController) SupportsLocalNetworkProbe() bool {
	return false
}

func (linuxController) LocalNetworkDeniedAdvice() string {
	return ""
}

func (linuxController) LocalNetworkUnknownAdvice(string) string {
	return ""
}

func (linuxController) ResolverAutoHealDelegated() bool {
	return false
}

func (linuxController) BootstrapRequiresRootTargetUser() bool {
	return true
}

func (linuxController) BootstrapNeedsStateDirOwnershipPrep() bool {
	return false
}

func (linuxController) BootstrapVerboseNote(isRoot bool, targetUser string) string {
	if isRoot {
		if strings.TrimSpace(targetUser) == "" {
			return "running as root; resolver setup now, user daemon setup as your user next."
		}
		return "running as root; resolver setup now, user daemon setup as " + targetUser + " next."
	}
	return "running as non-root; user daemon setup now."
}

func (linuxController) BootstrapSupportsPrivilegedPortCapability() bool {
	return true
}

func (linuxController) BootstrapHasRootResolverOnlyPass() bool {
	return true
}

func (c linuxController) ValidateConfig(cfg config.Config) error {
	if strings.TrimSpace(cfg.DNSListen) == "" {
		return fmt.Errorf("dns_listen is required")
	}
	return nil
}

func (c linuxController) EnsureResolver(spec ResolverSpec) error {
	switch c.resolver.Kind {
	case linux.ResolverBackendSystemdResolved:
		return linux.EnsureSystemdResolvedResolver(linux.ResolverSpec(spec))
	case linux.ResolverBackendDnsmasq:
		return linux.EnsureDnsmasqResolver(linux.ResolverSpec(spec))
	default:
		return fmt.Errorf("linux resolver integration unavailable: %s", c.resolver.Reason)
	}
}

func (c linuxController) ResolverMatches(spec ResolverSpec) (bool, error) {
	switch c.resolver.Kind {
	case linux.ResolverBackendSystemdResolved:
		return linux.SystemdResolvedResolverMatches(linux.ResolverSpec(spec))
	case linux.ResolverBackendDnsmasq:
		return linux.DnsmasqResolverMatches(linux.ResolverSpec(spec))
	default:
		return false, fmt.Errorf("linux resolver checks unavailable: %s", c.resolver.Reason)
	}
}

func (c linuxController) RemoveResolver(spec ResolverSpec) error {
	switch c.resolver.Kind {
	case linux.ResolverBackendSystemdResolved:
		return linux.RemoveSystemdResolvedResolver(linux.ResolverSpec(spec))
	case linux.ResolverBackendDnsmasq:
		return linux.RemoveDnsmasqResolver(linux.ResolverSpec(spec))
	default:
		return fmt.Errorf("linux resolver removal unavailable: %s", c.resolver.Reason)
	}
}

func (c linuxController) ListManagedResolvers() ([]string, error) {
	switch c.resolver.Kind {
	case linux.ResolverBackendSystemdResolved:
		return linux.ListManagedSystemdResolvedResolvers()
	case linux.ResolverBackendDnsmasq:
		return linux.ListManagedDnsmasqResolvers()
	default:
		return nil, nil
	}
}

func (c linuxController) InstallDaemon(spec DaemonSpec) (string, error) {
	if !c.cap.Systemctl {
		return "", fmt.Errorf("linux daemon install requires systemd user services (`systemctl --user`)")
	}
	return linux.InstallOrUpdateUserDaemon(spec.BinaryPath, spec.ConfigPath, spec.StateDir, spec.WorkingDir)
}

func (linuxController) InstallLoopbackAgent(_ LoopbackAgentSpec) (string, error) {
	return "linux-inline-loopback", nil
}

func (c linuxController) StopDaemon() error {
	if !c.cap.Systemctl {
		return fmt.Errorf("linux daemon stop requires `systemctl --user`")
	}
	return linux.StopUserDaemon()
}

func (c linuxController) UninstallDaemon() error {
	if !c.cap.Systemctl {
		return nil
	}
	return linux.UninstallUserDaemon()
}

func (linuxController) UninstallLoopbackAgent() error {
	return nil
}

func (c linuxController) DaemonStatus() (string, error) {
	if !c.cap.Systemctl {
		return "", fmt.Errorf("systemctl not available")
	}
	return linux.UserDaemonStatus()
}

func (linuxController) DaemonStatusDegraded(string) bool {
	return false
}

func (linuxController) DaemonStatusDetails(_ string, status string, _ bool) []string {
	return []string{"systemd user unit: dnsvard.service", "daemon summary: " + textutil.FirstLine(status)}
}

func (c linuxController) DaemonLogBackend() (DaemonLogBackend, error) {
	if !c.cap.Systemctl {
		return DaemonLogBackend{Source: DaemonLogSourceStateFile}, nil
	}
	unit, err := linux.ActiveUserDaemonUnit()
	if err != nil {
		return DaemonLogBackend{Source: DaemonLogSourceStateFile}, nil
	}
	return DaemonLogBackend{Source: DaemonLogSourceJournald, JournalUnit: unit}, nil
}

func (linuxController) LoopbackStatus() (string, error) {
	return "linux-inline --resolver-state-file", nil
}

func (linuxController) LoopbackStatusDetails(stateDir string, status string) []string {
	return []string{"loopback mode: inline", "resolver sync state file: " + filepath.Join(stateDir, "resolver-sync-state.json"), "loopback summary: " + textutil.FirstLine(status)}
}

func (c linuxController) Diagnostics() []string {
	resolver := string(c.resolver.Kind)
	if c.resolver.Kind == linux.ResolverBackendNone && c.resolver.Reason != "" {
		resolver = resolver + " (" + c.resolver.Reason + ")"
	}
	return []string{
		"platform: linux",
		"resolver_backend: " + resolver,
		fmt.Sprintf("capabilities: systemctl=%t resolvectl=%t dnsmasq=%t ip=%t", c.cap.Systemctl, c.cap.SystemdResolved, c.cap.Dnsmasq, c.cap.IPTool),
	}
}

func (c linuxController) DoctorHints() []string {
	return linux.ResolverBackendFixHints(c.cap, c.resolver)
}

func (c linuxController) DoctorRecommendation() string {
	base := "run bootstrap with sudo once from your user shell: `sudo dnsvard bootstrap`"
	switch c.resolver.Kind {
	case linux.ResolverBackendSystemdResolved:
		return "recommended setup: systemd-resolved backend; " + base
	case linux.ResolverBackendDnsmasq:
		return "recommended setup: dnsmasq backend (supports non-53 forwarding when configured); " + base
	default:
		return "recommended setup: install/enable dnsmasq or systemd-resolved backend, then " + base
	}
}

func (c linuxController) DoctorFixPreflight() ([]string, error) {
	checks := []string{"linux capability preflight complete"}

	switch c.resolver.Kind {
	case linux.ResolverBackendSystemdResolved:
		if err := systemServiceIsActive("systemd-resolved"); err != nil {
			return nil, fmt.Errorf("linux preflight failed: %v\nfix: sudo systemctl enable --now systemd-resolved", err)
		}
		checks = append(checks, "resolver backend service active: systemd-resolved")
		return checks, nil
	case linux.ResolverBackendDnsmasq:
		if err := systemServiceIsActive("dnsmasq"); err == nil {
			checks = append(checks, "resolver backend service active: dnsmasq")
			return checks, nil
		}
		if err := systemServiceIsActive("NetworkManager"); err == nil {
			checks = append(checks, "resolver backend service active: NetworkManager")
			return checks, nil
		}
		return nil, fmt.Errorf("linux preflight failed: neither dnsmasq nor NetworkManager appears active\nfix: sudo systemctl enable --now dnsmasq\nor: sudo systemctl enable --now NetworkManager")
	default:
		return nil, fmt.Errorf("linux preflight failed: resolver backend unavailable (%s)\nfix: install and enable dnsmasq or systemd-resolved", c.resolver.Reason)
	}
}

func (c linuxController) BootstrapPrecheck() error {
	if c.resolver.Kind == linux.ResolverBackendNone {
		return fmt.Errorf("BOOTSTRAP ABORTED: no supported resolver backend is available\nACTION:\n  install dnsmasq or systemd-resolved\n  enable one backend via systemctl\nThen re-run:\n  sudo dnsvard bootstrap")
	}
	return nil
}

func (c linuxController) FlushDNSCache() error {
	if c.cap.SystemdResolved {
		if out, err := exec.Command("resolvectl", "flush-caches").CombinedOutput(); err != nil {
			return fmt.Errorf("flush resolvectl cache: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (linuxController) DescribeTCPPortListeners(port int) ([]string, error) {
	if port <= 0 {
		return nil, nil
	}
	cmd := exec.Command("ss", "-lntp", "sport", "=", ":"+fmt.Sprintf("%d", port))
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil, nil
	}
	listeners := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "State") {
			continue
		}
		listeners = append(listeners, trimmed)
	}
	return listeners, nil
}

func (linuxController) AutoRepairDaemon(_ DaemonSpec) (bool, error) {
	return false, nil
}

func (linuxController) DaemonCanAutoHealResolvers() bool {
	return true
}
