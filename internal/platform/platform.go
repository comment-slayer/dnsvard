package platform

import (
	"github.com/comment-slayer/dnsvard/internal/config"
)

type ResolverSpec struct {
	Domain     string
	Nameserver string
	Port       string
}

type DaemonSpec struct {
	BinaryPath string
	ConfigPath string
	StateDir   string
	WorkingDir string
}

type LoopbackAgentSpec struct {
	BinaryPath        string
	StateFile         string
	ResolverStateFile string
	CIDR              string
}

type DaemonLogSource string

const (
	DaemonLogSourceStateFile DaemonLogSource = "state_file"
	DaemonLogSourceJournald  DaemonLogSource = "journald"
)

type DaemonLogBackend struct {
	Source      DaemonLogSource
	JournalUnit string
}

type Controller interface {
	Name() string
	SupportsLocalNetworkProbe() bool
	LocalNetworkDeniedAdvice() string
	LocalNetworkUnknownAdvice(domain string) string
	ResolverAutoHealDelegated() bool
	BootstrapRequiresRootTargetUser() bool
	BootstrapNeedsStateDirOwnershipPrep() bool
	BootstrapVerboseNote(isRoot bool, targetUser string) string
	BootstrapSupportsPrivilegedPortCapability() bool
	BootstrapHasRootResolverOnlyPass() bool
	ValidateConfig(cfg config.Config) error
	BootstrapPrecheck() error
	Diagnostics() []string
	DoctorHints() []string
	DoctorRecommendation() string
	DoctorFixPreflight() ([]string, error)
	EnsureResolver(spec ResolverSpec) error
	ResolverMatches(spec ResolverSpec) (bool, error)
	RemoveResolver(spec ResolverSpec) error
	ListManagedResolvers() ([]string, error)
	InstallDaemon(spec DaemonSpec) (string, error)
	InstallLoopbackAgent(spec LoopbackAgentSpec) (string, error)
	StopDaemon() error
	UninstallDaemon() error
	UninstallLoopbackAgent() error
	DaemonStatus() (string, error)
	DaemonStatusDegraded(status string) bool
	DaemonStatusDetails(stateDir string, status string, degraded bool) []string
	DaemonLogBackend() (DaemonLogBackend, error)
	LoopbackStatus() (string, error)
	LoopbackStatusDetails(stateDir string, status string) []string
	FlushDNSCache() error
	DescribeTCPPortListeners(port int) ([]string, error)
	AutoRepairDaemon(spec DaemonSpec) (bool, error)
	DaemonCanAutoHealResolvers() bool
}
