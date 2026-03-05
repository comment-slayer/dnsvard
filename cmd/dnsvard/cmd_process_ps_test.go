package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
)

func TestWorkspaceDisplayDomainUsesHostPattern(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "cs"
	cfg.HostPattern = "workspace-tld"

	got := workspaceDisplayDomain(cfg, "comment-slayer", "feat-1")
	if got != "feat-1.cs" {
		t.Fatalf("workspaceDisplayDomain = %q, want %q", got, "feat-1.cs")
	}
}

func TestPrintManagedStateIncludesDomainWithoutOwnershipField(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "cs"
	cfg.HostPattern = "workspace-tld"
	state := managedState{
		Containers: []dockerContainer{
			{ID: "abc123", Name: "frontend-1", Service: "frontend", Project: "comment-slayer", Workspace: "feat-1", Running: true},
		},
	}

	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, cfg, state, nil, false, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "workspace/comment-slayer/feat-1 domain=feat-1.cs") {
		t.Fatalf("output missing workspace domain:\n%s", out.String())
	}
	if strings.Contains(out.String(), "ownership=") {
		t.Fatalf("output should not include ownership field:\n%s", out.String())
	}
}

func TestPrintManagedStateShowsContainerCountsForMultiWorkspace(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "cs"
	cfg.HostPattern = "workspace-tld"
	state := managedState{
		Containers: []dockerContainer{
			{ID: "a", Name: "frontend-1", Service: "frontend", Project: "comment-slayer", Workspace: "feat-1", Running: true},
			{ID: "b", Name: "db-1", Service: "db", Project: "comment-slayer", Workspace: "feat-1", Running: true},
			{ID: "c", Name: "frontend-2", Service: "frontend", Project: "comment-slayer", Workspace: "feat-2", Running: true},
		},
	}

	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, cfg, state, nil, false, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "project/comment-slayer containers=3") {
		t.Fatalf("output missing project container count:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "workspace/comment-slayer/feat-1 domain=feat-1.cs containers=2") {
		t.Fatalf("output missing workspace feat-1 container count:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "workspace/comment-slayer/feat-2 domain=feat-2.cs containers=1") {
		t.Fatalf("output missing workspace feat-2 container count:\n%s", out.String())
	}
}

func TestManagedStateDisplayConfigUsesDaemonReconcileConfig(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.Domain = "test"
	cfg.HostPattern = "service-workspace-project-tld"

	pid := os.Getpid()
	if err := daemon.WritePID(stateDir, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
		PID:               pid,
		Result:            "success",
		ConfigDomain:      "cs",
		ConfigHostPattern: "workspace-tld",
	}); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}

	displayCfg := managedStateDisplayConfig(cfg)
	if displayCfg.Domain != "cs" {
		t.Fatalf("display domain = %q, want %q", displayCfg.Domain, "cs")
	}
	if displayCfg.HostPattern != "workspace-tld" {
		t.Fatalf("display host_pattern = %q, want %q", displayCfg.HostPattern, "workspace-tld")
	}

	state := managedState{Containers: []dockerContainer{{
		ID: "abc123", Name: "frontend-1", Service: "frontend", Project: "comment-slayer", Workspace: "feat-1", Running: true,
	}}}
	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, displayCfg, state, nil, false, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "workspace/comment-slayer/feat-1 domain=feat-1.cs") {
		t.Fatalf("output missing daemon-reconcile domain:\n%s", out.String())
	}
}

func TestManagedStateDisplayConfigSkipsMismatchedReconcilePID(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.Domain = "test"
	cfg.HostPattern = "service-workspace-project-tld"

	pid := os.Getpid()
	if err := daemon.WritePID(stateDir, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
		PID:               pid + 1,
		Result:            "success",
		ConfigDomain:      "cs",
		ConfigHostPattern: "workspace-tld",
	}); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}

	displayCfg := managedStateDisplayConfig(cfg)
	if displayCfg.Domain != "test" {
		t.Fatalf("display domain = %q, want %q", displayCfg.Domain, "test")
	}
	if displayCfg.HostPattern != "service-workspace-project-tld" {
		t.Fatalf("display host_pattern = %q, want %q", displayCfg.HostPattern, "service-workspace-project-tld")
	}
}

func TestManagedStateDisplayConfigSkipsStaleReconcileState(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.Domain = "test"
	cfg.HostPattern = "service-workspace-project-tld"

	pid := os.Getpid()
	if err := daemon.WritePID(stateDir, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
		PID:               pid,
		Result:            "success",
		IntervalSeconds:   15,
		UpdatedAt:         time.Now().Add(-2*time.Minute - 1*time.Second),
		ConfigDomain:      "cs",
		ConfigHostPattern: "workspace-tld",
	}); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}

	displayCfg := managedStateDisplayConfig(cfg)
	if displayCfg.Domain != "test" {
		t.Fatalf("display domain = %q, want %q", displayCfg.Domain, "test")
	}
	if displayCfg.HostPattern != "service-workspace-project-tld" {
		t.Fatalf("display host_pattern = %q, want %q", displayCfg.HostPattern, "service-workspace-project-tld")
	}
}

func TestManagedStateDisplayConfigRecoversAfterReconcileRefresh(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.Domain = "test"
	cfg.HostPattern = "service-workspace-project-tld"

	pid := os.Getpid()
	if err := daemon.WritePID(stateDir, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
		PID:             pid,
		Result:          "success",
		Sequence:        1,
		IntervalSeconds: 15,
		UpdatedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
			PID:               pid,
			Result:            "success",
			Sequence:          2,
			IntervalSeconds:   15,
			UpdatedAt:         time.Now(),
			ConfigDomain:      "cs",
			ConfigHostPattern: "workspace-tld",
		})
	}()

	displayCfg := managedStateDisplayConfig(cfg)
	if displayCfg.Domain != "cs" {
		t.Fatalf("display domain = %q, want %q", displayCfg.Domain, "cs")
	}
	if displayCfg.HostPattern != "workspace-tld" {
		t.Fatalf("display host_pattern = %q, want %q", displayCfg.HostPattern, "workspace-tld")
	}

	state := managedState{Containers: []dockerContainer{{
		ID: "abc123", Name: "frontend-1", Service: "frontend", Project: "comment-slayer", Workspace: "feat-1", Running: true,
	}}}
	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, displayCfg, state, nil, false, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "workspace/comment-slayer/feat-1 domain=feat-1.cs") {
		t.Fatalf("output should use refreshed runtime domain:\n%s", out.String())
	}
}

func TestManagedStateDisplayConfigAvoidsConfigLeakWhenRuntimeConfigMissing(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.Domain = "test"
	cfg.HostPattern = "service-workspace-project-tld"

	pid := os.Getpid()
	if err := daemon.WritePID(stateDir, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
		PID:             pid,
		Result:          "success",
		IntervalSeconds: 15,
		UpdatedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}

	displayCfg := managedStateDisplayConfig(cfg)
	if displayCfg.Domain != "" {
		t.Fatalf("display domain = %q, want empty", displayCfg.Domain)
	}
	if displayCfg.HostPattern != "" {
		t.Fatalf("display host_pattern = %q, want empty", displayCfg.HostPattern)
	}

	state := managedState{Containers: []dockerContainer{{
		ID: "abc123", Name: "frontend-1", Service: "frontend", Project: "comment-slayer", Workspace: "feat-1", Running: true,
	}}}
	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, displayCfg, state, nil, false, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "workspace/comment-slayer/feat-1 domain=n/a") {
		t.Fatalf("output should avoid leaking local config domain:\n%s", out.String())
	}
}

func TestManagedStateWorkspaceDomainsReadsRuntimeState(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = stateDir

	pid := os.Getpid()
	if err := daemon.WritePID(stateDir, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := daemon.WriteReconcileState(stateDir, daemon.ReconcileState{
		PID:             pid,
		Result:          "success",
		IntervalSeconds: 15,
		UpdatedAt:       time.Now(),
		WorkspaceDomains: map[string]string{
			"master@Breadstick":     "master.bs",
			"master@Comment-Slayer": "master.cs",
		},
	}); err != nil {
		t.Fatalf("WriteReconcileState: %v", err)
	}

	domains, authoritative := managedStateWorkspaceDomains(cfg)
	if !authoritative {
		t.Fatal("expected runtime authoritative workspace domains")
	}
	if domains["master@breadstick"] != "master.bs" {
		t.Fatalf("breadstick domain = %q, want %q", domains["master@breadstick"], "master.bs")
	}
	if domains["master@comment-slayer"] != "master.cs" {
		t.Fatalf("comment-slayer domain = %q, want %q", domains["master@comment-slayer"], "master.cs")
	}
}

func TestPrintManagedStateUsesWorkspaceDomainsFromRuntimeState(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "bs"
	cfg.HostPattern = "service-workspace-tld"
	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "api-1", Service: "api", Project: "breadstick", Workspace: "master", Running: true},
		{ID: "b", Name: "web-1", Service: "web", Project: "comment-slayer", Workspace: "master", Running: true},
	}}
	runtimeDomains := map[string]string{
		"master@breadstick":     "master.bs",
		"master@comment-slayer": "master.cs",
	}

	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, cfg, state, runtimeDomains, true, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "workspace/breadstick/master domain=master.bs") {
		t.Fatalf("output missing breadstick runtime domain:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "workspace/comment-slayer/master domain=master.cs") {
		t.Fatalf("output missing comment-slayer runtime domain:\n%s", out.String())
	}
}

func TestPrintManagedStateMarksDomainNAWhenRuntimeAuthoritativeAndMissing(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "bs"
	cfg.HostPattern = "service-workspace-tld"
	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "api-1", Service: "api", Project: "breadstick", Workspace: "master", Running: true},
		{ID: "b", Name: "web-1", Service: "web", Project: "comment-slayer", Workspace: "master", Running: true},
	}}
	runtimeDomains := map[string]string{
		"master@breadstick": "master.bs",
	}

	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, cfg, state, runtimeDomains, true, managedStatePrintOptions{GroupByProject: true})
	if !strings.Contains(out.String(), "workspace/breadstick/master domain=master.bs") {
		t.Fatalf("output missing breadstick runtime domain:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "workspace/comment-slayer/master domain=n/a") {
		t.Fatalf("output should not leak fallback suffix for missing runtime domain:\n%s", out.String())
	}
}

func TestParsePSFilterAcceptsWorkspaceTarget(t *testing.T) {
	t.Parallel()

	target, err := parsePSFilter([]string{"workspace/breadstick/feat-1"})
	if err != nil {
		t.Fatalf("parsePSFilter: %v", err)
	}
	if target != "workspace/breadstick/feat-1" {
		t.Fatalf("target = %q, want %q", target, "workspace/breadstick/feat-1")
	}
}

func TestFilterManagedStateWorkspaceRequiresProject(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{{ID: "a", Name: "feat-1-api-1", Project: "breadstick", Workspace: "feat-1", Running: true}}}
	if _, err := filterManagedState(state, "workspace/feat-1", targetMatchExact); err == nil {
		t.Fatal("expected workspace target without project/workspace segments to fail")
	}
}

func TestFilterManagedStateProjectPrefixMatch(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "feat-1-api-1", Service: "api", Project: "breadstick", Workspace: "feat-1", Running: true},
		{ID: "b", Name: "master-api-1", Service: "api", Project: "breadstick", Workspace: "master", Running: true},
		{ID: "c", Name: "review-api-1", Service: "api", Project: "breadstick", Workspace: "review", Running: true},
		{ID: "d", Name: "other-api-1", Service: "api", Project: "comment-slayer", Workspace: "master", Running: true},
	}}

	filtered, err := filterManagedState(state, "workspace/bread", targetMatchPrefix)
	if err != nil {
		t.Fatalf("filterManagedState: %v", err)
	}
	if len(filtered.Containers) != 3 {
		t.Fatalf("filtered container count = %d, want 3", len(filtered.Containers))
	}
	for _, c := range filtered.Containers {
		if c.Project != "breadstick" {
			t.Fatalf("unexpected project in filtered output: %s", c.Project)
		}
	}
}

func TestFilterManagedStateProjectShorthandAndSlashMatchAllProjects(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "feat-1-api-1", Service: "api", Project: "breadstick", Workspace: "feat-1", Running: true},
		{ID: "b", Name: "master-api-1", Service: "api", Project: "comment-slayer", Workspace: "master", Running: true},
	}}

	filteredBare, err := filterManagedState(state, "workspace", targetMatchExact)
	if err != nil {
		t.Fatalf("filterManagedState(workspace): %v", err)
	}
	if len(filteredBare.Containers) != 2 {
		t.Fatalf("project shorthand container count = %d, want 2", len(filteredBare.Containers))
	}

	filteredSlash, err := filterManagedState(state, "workspace/", targetMatchExact)
	if err != nil {
		t.Fatalf("filterManagedState(workspace/): %v", err)
	}
	if len(filteredSlash.Containers) != 2 {
		t.Fatalf("workspace/ container count = %d, want 2", len(filteredSlash.Containers))
	}
}

func TestPrintManagedStateProjectFilterStyle(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "test"
	cfg.HostPattern = "workspace-tld"
	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "feat-1-api-1", Service: "api", Project: "breadstick", Workspace: "feat-1", Running: true},
		{ID: "b", Name: "feat-1-frontend-1", Service: "frontend", Project: "breadstick", Workspace: "feat-1", Running: true},
		{ID: "c", Name: "master-api-1", Service: "api", Project: "breadstick", Workspace: "master", Running: true},
	}}

	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, cfg, state, nil, false, psFilterDisplayOptions("workspace/breadstick", state))
	if strings.Contains(out.String(), "project/breadstick containers=") {
		t.Fatalf("project-group heading should not be present for workspace project filter:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "workspace/breadstick/feat-1 domain=feat-1.test containers=2") {
		t.Fatalf("workspace line should include container count:\n%s", out.String())
	}
}

func TestPrintManagedStateWorkspaceRootFilterShowsProjectTreeWhenMultipleProjects(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "test"
	cfg.HostPattern = "workspace-tld"
	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "feat-1-api-1", Service: "api", Project: "breadstick", Workspace: "feat-1", Running: true},
		{ID: "b", Name: "master-api-1", Service: "api", Project: "comment-slayer", Workspace: "master", Running: true},
	}}

	var out bytes.Buffer
	printManagedStateWithRuntimeDomainsTo(&out, cfg, state, nil, false, psFilterDisplayOptions("workspace", state))
	if !strings.Contains(out.String(), "project/breadstick containers=1") {
		t.Fatalf("project heading missing for breadstick:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "project/comment-slayer containers=1") {
		t.Fatalf("project heading missing for comment-slayer:\n%s", out.String())
	}
}

func TestFilterManagedStateSupportsWorkspaceContainerLeafByService(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "csdev-anonymize-deletions-6496c91a-clickhouse-1", Service: "clickhouse", Project: "comment-slayer", Workspace: "anonymize-deletions", Running: true},
		{ID: "b", Name: "csdev-anonymize-deletions-6496c91a-api-1", Service: "api", Project: "comment-slayer", Workspace: "anonymize-deletions", Running: true},
	}}

	filtered, err := filterManagedState(state, "workspace/comment-slayer/anonymize-deletions/clickhouse", targetMatchExact)
	if err != nil {
		t.Fatalf("filterManagedState: %v", err)
	}
	if len(filtered.Containers) != 1 || filtered.Containers[0].ID != "a" {
		t.Fatalf("filtered containers = %#v", filtered.Containers)
	}
}

func TestFilterManagedStateSupportsWorkspaceContainerLeafByName(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{{ID: "a", Name: "csdev-anonymize-deletions-6496c91a-clickhouse-1", Service: "clickhouse", Project: "comment-slayer", Workspace: "anonymize-deletions", Running: true}}}
	filtered, err := filterManagedState(state, "workspace/comment-slayer/anonymize-deletions/csdev-anonymize-deletions-6496c91a-clickhouse-1", targetMatchExact)
	if err != nil {
		t.Fatalf("filterManagedState: %v", err)
	}
	if len(filtered.Containers) != 1 || filtered.Containers[0].ID != "a" {
		t.Fatalf("filtered containers = %#v", filtered.Containers)
	}
}

func TestPrintManagedStateNameOnlyOutputsCanonicalContainerTargets(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{
		{ID: "a", Name: "csdev-anonymize-deletions-6496c91a-clickhouse-1", Service: "clickhouse", Project: "comment-slayer", Workspace: "anonymize-deletions", Running: true},
		{ID: "b", Name: "csdev-anonymize-deletions-6496c91a-api-1", Service: "api", Project: "comment-slayer", Workspace: "anonymize-deletions", Running: true},
	}}

	var out bytes.Buffer
	printManagedStateNameOnlyTo(&out, state)
	got := strings.TrimSpace(out.String())
	want := strings.Join([]string{
		"workspace/comment-slayer/anonymize-deletions/csdev-anonymize-deletions-6496c91a-api-1",
		"workspace/comment-slayer/anonymize-deletions/csdev-anonymize-deletions-6496c91a-clickhouse-1",
	}, "\n")
	if got != want {
		t.Fatalf("name-only output = %q, want %q", got, want)
	}
}
