//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/macos"
	"github.com/comment-slayer/dnsvard/internal/textutil"
)

type darwinController struct{}

func New() Controller {
	return darwinController{}
}

func (darwinController) Name() string {
	return "darwin"
}

func (darwinController) SupportsLocalNetworkProbe() bool {
	return true
}

func (darwinController) LocalNetworkDeniedAdvice() string {
	return "open System Settings -> Privacy & Security -> Local Network and allow dnsvard (or your terminal app), then retry"
}

func (darwinController) LocalNetworkUnknownAdvice(domain string) string {
	return fmt.Sprintf("if requests to *.%s fail, check System Settings -> Privacy & Security -> Local Network and allow dnsvard (or your terminal app)", domain)
}

func (darwinController) ResolverAutoHealDelegated() bool {
	return true
}

func (darwinController) BootstrapRequiresRootTargetUser() bool {
	return false
}

func (darwinController) BootstrapNeedsStateDirOwnershipPrep() bool {
	return true
}

func (darwinController) BootstrapVerboseNote(_ bool, _ string) string {
	return "macOS may show a Local Network access prompt for dnsvard; click Allow."
}

func (darwinController) BootstrapSupportsPrivilegedPortCapability() bool {
	return false
}

func (darwinController) BootstrapHasRootResolverOnlyPass() bool {
	return false
}

func (darwinController) ValidateConfig(_ config.Config) error {
	return nil
}

func (darwinController) BootstrapPrecheck() error {
	return nil
}

func (darwinController) Diagnostics() []string {
	return []string{"platform: darwin"}
}

func (darwinController) DoctorHints() []string {
	return nil
}

func (darwinController) DoctorRecommendation() string {
	return ""
}

func (darwinController) DoctorFixPreflight() ([]string, error) {
	return nil, nil
}

func (darwinController) EnsureResolver(spec ResolverSpec) error {
	return macos.EnsureResolver(macos.ResolverSpec(spec))
}

func (darwinController) ResolverMatches(spec ResolverSpec) (bool, error) {
	return macos.ResolverMatches(macos.ResolverSpec(spec))
}

func (darwinController) RemoveResolver(spec ResolverSpec) error {
	return macos.RemoveResolver(macos.ResolverSpec(spec))
}

func (darwinController) ListManagedResolvers() ([]string, error) {
	return macos.ListManagedResolvers()
}

func (darwinController) InstallDaemon(spec DaemonSpec) (string, error) {
	return macos.InstallOrUpdateLaunchAgent(macos.LaunchAgentSpec{
		BinaryPath: spec.BinaryPath,
		ConfigPath: spec.ConfigPath,
		StateDir:   spec.StateDir,
		WorkingDir: spec.WorkingDir,
	})
}

func (darwinController) InstallLoopbackAgent(spec LoopbackAgentSpec) (string, error) {
	return macos.InstallOrUpdateLoopbackAgent(macos.LoopbackAgentSpec{
		BinaryPath:        spec.BinaryPath,
		StateFile:         spec.StateFile,
		ResolverStateFile: spec.ResolverStateFile,
		CIDR:              spec.CIDR,
	})
}

func (darwinController) StopDaemon() error {
	return macos.StopLaunchAgent()
}

func (darwinController) UninstallDaemon() error {
	return macos.UninstallLaunchAgent()
}

func (darwinController) UninstallLoopbackAgent() error {
	return macos.UninstallLoopbackAgent()
}

func (darwinController) DaemonStatus() (string, error) {
	return macos.LaunchAgentStatus()
}

func (darwinController) DaemonStatusDegraded(status string) bool {
	lower := strings.ToLower(status)
	return strings.Contains(lower, "state = spawn scheduled") || strings.Contains(lower, "last exit code = 1")
}

func (darwinController) DaemonStatusDetails(_ string, status string, degraded bool) []string {
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = "~"
	}
	details := []string{
		"launchd label: dev.dnsvard.daemon",
		"launch agent plist: " + filepath.Join(home, "Library", "LaunchAgents", "dev.dnsvard.daemon.plist"),
		"launch daemon summary: " + textutil.FirstLine(status),
	}
	if degraded {
		details = append(details, "launch daemon note: launchd shows restart retries or recent failures; likely unmanaged port conflict")
	}
	return details
}

func (darwinController) DaemonLogBackend() (DaemonLogBackend, error) {
	return DaemonLogBackend{Source: DaemonLogSourceStateFile}, nil
}

func (darwinController) LoopbackStatus() (string, error) {
	return macos.LoopbackAgentStatus()
}

func (darwinController) LoopbackStatusDetails(stateDir string, status string) []string {
	return []string{
		"loopback label: dev.dnsvard.loopback",
		"loopback plist: /Library/LaunchDaemons/dev.dnsvard.loopback.plist",
		"resolver sync state file: " + filepath.Join(stateDir, "resolver-sync-state.json"),
		"loopback summary: " + textutil.FirstLine(status),
	}
}

func (darwinController) FlushDNSCache() error {
	if err := exec.Command("dscacheutil", "-flushcache").Run(); err != nil {
		return fmt.Errorf("flush dscacheutil: %w", err)
	}
	if err := exec.Command("killall", "-HUP", "mDNSResponder").Run(); err != nil {
		return fmt.Errorf("reload mDNSResponder: %w", err)
	}
	return nil
}

func (darwinController) DescribeTCPPortListeners(port int) ([]string, error) {
	if port <= 0 {
		return nil, nil
	}
	cmd := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpcn")
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return nil, nil
	}
	listeners := []string{}
	pid := ""
	cmdName := ""
	name := ""
	flush := func() {
		if pid == "" && cmdName == "" && name == "" {
			return
		}
		listeners = append(listeners, strings.TrimSpace(fmt.Sprintf("pid=%s command=%s endpoint=%s", pid, cmdName, name)))
		pid = ""
		cmdName = ""
		name = ""
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			flush()
			pid = strings.TrimSpace(line[1:])
		case 'c':
			cmdName = strings.TrimSpace(line[1:])
		case 'n':
			name = strings.TrimSpace(line[1:])
		}
	}
	flush()
	return listeners, nil
}

func (darwinController) AutoRepairDaemon(spec DaemonSpec) (bool, error) {
	status, err := macos.LaunchAgentStatus()
	if err != nil {
		return false, nil
	}
	lower := strings.ToLower(status)
	if !strings.Contains(lower, "state = spawn scheduled") && !strings.Contains(lower, "last exit code = 1") {
		return false, nil
	}
	if err := macos.StopLaunchAgent(); err != nil {
		return false, err
	}
	if _, err := macos.InstallOrUpdateLaunchAgent(macos.LaunchAgentSpec{
		BinaryPath: spec.BinaryPath,
		ConfigPath: spec.ConfigPath,
		StateDir:   spec.StateDir,
		WorkingDir: spec.WorkingDir,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (darwinController) DaemonCanAutoHealResolvers() bool {
	return false
}
