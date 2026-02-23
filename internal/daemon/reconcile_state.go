package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const ReconcileStateVersion = 1

type ReconcileState struct {
	Version                 int                            `json:"version"`
	PID                     int                            `json:"pid"`
	ConfigDomain            string                         `json:"config_domain,omitempty"`
	ConfigHostPattern       string                         `json:"config_host_pattern,omitempty"`
	WorkspaceDomains        map[string]string              `json:"workspace_domains,omitempty"`
	UpdatedAt               time.Time                      `json:"updated_at"`
	Sequence                uint64                         `json:"sequence"`
	IntervalSeconds         int                            `json:"interval_seconds"`
	Cause                   string                         `json:"cause"`
	Result                  string                         `json:"result"`
	LastSuccessAt           time.Time                      `json:"last_success_at,omitempty"`
	LastError               string                         `json:"last_error,omitempty"`
	DNSAdded                int                            `json:"dns_added"`
	DNSRemoved              int                            `json:"dns_removed"`
	HTTPAdded               int                            `json:"http_added"`
	HTTPRemoved             int                            `json:"http_removed"`
	TCPAdded                int                            `json:"tcp_added"`
	TCPRemoved              int                            `json:"tcp_removed"`
	Warnings                int                            `json:"warnings"`
	DurationMS              int64                          `json:"duration_ms"`
	TrackedConfigs          int                            `json:"tracked_configs"`
	WatchedDirs             int                            `json:"watched_dirs"`
	DockerWatchRunning      bool                           `json:"docker_watch_running,omitempty"`
	DockerWatchRestartCount int                            `json:"docker_watch_restart_count,omitempty"`
	DockerWatchLastStartAt  time.Time                      `json:"docker_watch_last_start_at,omitempty"`
	DockerWatchLastEventAt  time.Time                      `json:"docker_watch_last_event_at,omitempty"`
	DockerWatchLastError    string                         `json:"docker_watch_last_error,omitempty"`
	SelfHealTrigger         string                         `json:"self_heal_trigger,omitempty"`
	SelfHealAction          string                         `json:"self_heal_action,omitempty"`
	SelfHealDetail          string                         `json:"self_heal_detail,omitempty"`
	SelfHealDetector        string                         `json:"self_heal_detector,omitempty"`
	SelfHealComponent       string                         `json:"self_heal_component,omitempty"`
	SelfHealActionID        string                         `json:"self_heal_action_id,omitempty"`
	SelfHealFailed          bool                           `json:"self_heal_failed,omitempty"`
	SelfHealFailureCount    int                            `json:"self_heal_failure_count,omitempty"`
	SelfHealActions         map[string]SelfHealActionState `json:"self_heal_actions,omitempty"`
}

type SelfHealActionState struct {
	Component         string    `json:"component,omitempty"`
	Detector          string    `json:"detector,omitempty"`
	Trigger           string    `json:"trigger,omitempty"`
	Action            string    `json:"action,omitempty"`
	FailureCount      int       `json:"failure_count,omitempty"`
	LastFailureAt     time.Time `json:"last_failure_at,omitempty"`
	LastFailureDetail string    `json:"last_failure_detail,omitempty"`
	BlockedUntil      time.Time `json:"blocked_until,omitempty"`
}

type UnsupportedReconcileStateVersionError struct {
	Version int
}

func (e UnsupportedReconcileStateVersionError) Error() string {
	return fmt.Sprintf("unsupported reconcile state version %d (supported=%d)", e.Version, ReconcileStateVersion)
}

func ReconcileStatePath(stateDir string) string {
	return filepath.Join(stateDir, "reconcile-state.json")
}

func WriteReconcileState(stateDir string, state ReconcileState) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	state.Version = ReconcileStateVersion
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reconcile state: %w", err)
	}
	b = append(b, '\n')

	path := ReconcileStatePath(stateDir)
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open reconcile state tmp: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write reconcile state tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync reconcile state tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close reconcile state tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename reconcile state: %w", err)
	}
	dir, err := os.Open(stateDir)
	if err != nil {
		return fmt.Errorf("open reconcile state dir: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("sync reconcile state dir: %w", err)
	}
	return nil
}

func ReadReconcileState(stateDir string) (ReconcileState, error) {
	b, err := os.ReadFile(ReconcileStatePath(stateDir))
	if err != nil {
		return ReconcileState{}, err
	}
	out := ReconcileState{}
	if err := json.Unmarshal(b, &out); err != nil {
		return ReconcileState{}, fmt.Errorf("parse reconcile state: %w", err)
	}
	if out.Version <= 0 {
		return ReconcileState{}, fmt.Errorf("invalid reconcile state version %d", out.Version)
	}
	if out.Version > ReconcileStateVersion {
		return ReconcileState{}, UnsupportedReconcileStateVersionError{Version: out.Version}
	}
	return out, nil
}
