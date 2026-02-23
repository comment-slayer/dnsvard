//go:build !darwin && !linux

package platform

import (
	"fmt"
	"runtime"

	"github.com/comment-slayer/dnsvard/internal/config"
)

func unsupportedPlatformError() error {
	return fmt.Errorf("unsupported platform %q: dnsvard currently supports only darwin and linux", runtime.GOOS)
}

func unsupportedFeatureError(feature string) error {
	if feature == "" {
		return unsupportedPlatformError()
	}
	return fmt.Errorf("%s: %w", feature, unsupportedPlatformError())
}

type unsupportedController struct{}

func New() Controller {
	return unsupportedController{}
}

func (unsupportedController) Name() string {
	return "unsupported"
}

func (unsupportedController) SupportsLocalNetworkProbe() bool {
	return false
}

func (unsupportedController) LocalNetworkDeniedAdvice() string {
	return ""
}

func (unsupportedController) LocalNetworkUnknownAdvice(string) string {
	return ""
}

func (unsupportedController) ResolverAutoHealDelegated() bool {
	return false
}

func (unsupportedController) BootstrapRequiresRootTargetUser() bool {
	return false
}

func (unsupportedController) BootstrapNeedsStateDirOwnershipPrep() bool {
	return false
}

func (unsupportedController) BootstrapVerboseNote(bool, string) string {
	return ""
}

func (unsupportedController) BootstrapSupportsPrivilegedPortCapability() bool {
	return false
}

func (unsupportedController) BootstrapHasRootResolverOnlyPass() bool {
	return false
}

func (unsupportedController) ValidateConfig(_ config.Config) error {
	return unsupportedPlatformError()
}

func (unsupportedController) BootstrapPrecheck() error {
	return unsupportedPlatformError()
}

func (unsupportedController) Diagnostics() []string {
	return []string{"platform: unsupported"}
}

func (unsupportedController) DoctorHints() []string {
	return nil
}

func (unsupportedController) DoctorRecommendation() string {
	return "dnsvard currently supports only darwin and linux"
}

func (unsupportedController) DoctorFixPreflight() ([]string, error) {
	return nil, unsupportedPlatformError()
}

func (unsupportedController) EnsureResolver(ResolverSpec) error {
	return unsupportedFeatureError("resolver bootstrap")
}

func (unsupportedController) ResolverMatches(ResolverSpec) (bool, error) {
	return false, unsupportedFeatureError("resolver checks")
}

func (unsupportedController) RemoveResolver(ResolverSpec) error {
	return unsupportedFeatureError("resolver removal")
}

func (unsupportedController) ListManagedResolvers() ([]string, error) {
	return nil, unsupportedFeatureError("managed resolver listing")
}

func (unsupportedController) InstallDaemon(DaemonSpec) (string, error) {
	return "", unsupportedFeatureError("daemon installer")
}

func (unsupportedController) InstallLoopbackAgent(LoopbackAgentSpec) (string, error) {
	return "", unsupportedFeatureError("loopback agent installer")
}

func (unsupportedController) StopDaemon() error {
	return unsupportedFeatureError("daemon stop")
}

func (unsupportedController) UninstallDaemon() error {
	return unsupportedFeatureError("daemon uninstall")
}

func (unsupportedController) UninstallLoopbackAgent() error {
	return unsupportedFeatureError("loopback agent uninstall")
}

func (unsupportedController) DaemonStatus() (string, error) {
	return "", unsupportedFeatureError("daemon status")
}

func (unsupportedController) DaemonStatusDegraded(string) bool {
	return false
}

func (unsupportedController) DaemonStatusDetails(string, string, bool) []string {
	return nil
}

func (unsupportedController) DaemonLogBackend() (DaemonLogBackend, error) {
	return DaemonLogBackend{Source: DaemonLogSourceStateFile}, nil
}

func (unsupportedController) LoopbackStatus() (string, error) {
	return "", unsupportedFeatureError("loopback status")
}

func (unsupportedController) LoopbackStatusDetails(string, string) []string {
	return nil
}

func (unsupportedController) FlushDNSCache() error {
	return unsupportedFeatureError("dns cache flush")
}

func (unsupportedController) DescribeTCPPortListeners(int) ([]string, error) {
	return nil, nil
}

func (unsupportedController) AutoRepairDaemon(DaemonSpec) (bool, error) {
	return false, nil
}

func (unsupportedController) DaemonCanAutoHealResolvers() bool {
	return false
}
