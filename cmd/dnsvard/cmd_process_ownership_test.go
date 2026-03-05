package main

import (
	"testing"

	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
)

func TestContainerOwnershipPredicates(t *testing.T) {
	t.Parallel()

	dnsvardLabels := map[string]string{
		"dnsvard.project":                        "myproj",
		"dnsvard.workspace":                      "feat-1",
		"com.docker.compose.project":             "myproj",
		"com.docker.compose.service":             "api",
		"com.docker.compose.project.working_dir": "/work/myproj/feat-1",
	}
	composeOnly := map[string]string{
		"com.docker.compose.project":             "myproj",
		"com.docker.compose.service":             "api",
		"com.docker.compose.project.working_dir": "/work/myproj/feat-1",
	}
	plain := map[string]string{"some.label": "x"}
	swarm := map[string]string{
		"com.docker.swarm.service.name": "api",
		"com.docker.stack.namespace":    "stack",
	}
	swarmWithCompose := map[string]string{
		"com.docker.swarm.service.name": "api",
		"com.docker.compose.project":    "myproj",
		"com.docker.compose.service":    "api",
	}

	if !hasDnsvardLabel(dnsvardLabels) {
		t.Fatal("expected dnsvard labels to be detected")
	}
	if !isDiscoverableContainer(dnsvardLabels) {
		t.Fatal("expected dnsvard labels to be discoverable")
	}
	if hasDnsvardLabel(composeOnly) {
		t.Fatal("compose-only labels should not be treated as dnsvard labels")
	}
	if !isDiscoverableContainer(composeOnly) {
		t.Fatal("compose-only labels should remain discoverable")
	}
	if isDiscoverableContainer(plain) {
		t.Fatal("plain labels should not be discoverable")
	}
	if !isSwarmManagedContainer(swarm) {
		t.Fatal("expected swarm labels to be detected")
	}
	if isDiscoverableContainer(swarm) {
		t.Fatal("swarm containers should not be discoverable")
	}
	if isDiscoverableContainer(swarmWithCompose) {
		t.Fatal("swarm containers should be ignored even when compose labels are present")
	}
}

func TestSelectManagedTargetAllUsesAllRunningContainers(t *testing.T) {
	t.Parallel()

	state := managedState{
		Leases: []runtimeprovider.Lease{{ID: "lease-a", PID: 1234}},
		Containers: []dockerContainer{
			{ID: "owned-1", Name: "owned-api", Running: true},
			{ID: "compose-1", Name: "compose-api", Running: true},
		},
	}

	sel, err := selectManagedTarget(state, "all", targetMatchExact)
	if err != nil {
		t.Fatalf("selectManagedTarget returned error: %v", err)
	}
	if len(sel.ContainerIDs) != 2 || sel.ContainerIDs[0] != "compose-1" || sel.ContainerIDs[1] != "owned-1" {
		t.Fatalf("container ids = %#v, want compose-1 and owned-1", sel.ContainerIDs)
	}
	if len(sel.Leases) != 1 || sel.Leases[0].ID != "lease-a" {
		t.Fatalf("leases = %#v", sel.Leases)
	}
}

func TestSelectManagedTargetAllowsComposeOnlyContainerForDestructiveAction(t *testing.T) {
	t.Parallel()

	state := managedState{
		Containers: []dockerContainer{{ID: "compose-1", Name: "compose-api", Running: true}},
	}

	sel, err := selectManagedTarget(state, "container/compose-api", targetMatchExact)
	if err != nil {
		t.Fatalf("selectManagedTarget returned error: %v", err)
	}
	if len(sel.ContainerIDs) != 1 || sel.ContainerIDs[0] != "compose-1" {
		t.Fatalf("container ids = %#v", sel.ContainerIDs)
	}
}

func TestSelectContainerTargetIncludesAllInMixedWorkspace(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{
		{ID: "owned-1", Name: "owned-api", Project: "proj", Workspace: "ws"},
		{ID: "compose-1", Name: "compose-api", Project: "proj", Workspace: "ws"},
	}
	selected, err := selectContainerTarget(containers, "workspace/proj/ws", targetMatchExact)
	if err != nil {
		t.Fatalf("selectContainerTarget returned error: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectContainerTargetSupportsWorkspaceContainerLeafByService(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{{ID: "a", Name: "csdev-anonymize-deletions-6496c91a-clickhouse-1", Service: "clickhouse", Project: "comment-slayer", Workspace: "anonymize-deletions"}}
	selected, err := selectContainerTarget(containers, "workspace/comment-slayer/anonymize-deletions/clickhouse", targetMatchExact)
	if err != nil {
		t.Fatalf("selectContainerTarget returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "a" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectContainerTargetSupportsWorkspaceContainerLeafByName(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{{ID: "a", Name: "csdev-anonymize-deletions-6496c91a-clickhouse-1", Service: "clickhouse", Project: "comment-slayer", Workspace: "anonymize-deletions"}}
	selected, err := selectContainerTarget(containers, "workspace/comment-slayer/anonymize-deletions/csdev-anonymize-deletions-6496c91a-clickhouse-1", targetMatchExact)
	if err != nil {
		t.Fatalf("selectContainerTarget returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "a" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectContainerTargetAllowsComposeOnlyProjectScope(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{{ID: "compose-1", Name: "compose-api", Project: "proj", Workspace: "ws"}}
	selected, err := selectContainerTarget(containers, "workspace/proj", targetMatchExact)
	if err != nil {
		t.Fatalf("selectContainerTarget returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "compose-1" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectContainerTargetSupportsWorkspacePrefixPattern(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{
		{ID: "a", Name: "feat-1-api", Project: "breadstick", Workspace: "feat-1"},
		{ID: "b", Name: "master-api", Project: "comment-slayer", Workspace: "master"},
	}
	selected, err := selectContainerTarget(containers, "workspace/bread", targetMatchPrefix)
	if err != nil {
		t.Fatalf("selectContainerTarget returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "a" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectContainerTargetSupportsWorkspaceFullPathRegex(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{
		{ID: "a", Name: "feat-1-api", Project: "breadstick", Workspace: "feat-1"},
		{ID: "b", Name: "master-api", Project: "comment-slayer", Workspace: "master"},
	}
	selected, err := selectContainerTarget(containers, "workspace/comment-slayer/ma.*", targetMatchRegex)
	if err != nil {
		t.Fatalf("selectContainerTarget returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "b" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectContainerTargetRegexMatchesWholeWorkspacePath(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{{ID: "b", Name: "master-api", Project: "comment-slayer", Workspace: "master"}}
	if _, err := selectContainerTarget(containers, "workspace/comment-slayer/ma", targetMatchRegex); err == nil {
		t.Fatal("expected whole-path regex to reject partial workspace suffix match")
	}
}

func TestSelectContainerTargetSupportsWorkspaceShorthand(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{
		{ID: "a", Name: "feat-1-api", Project: "breadstick", Workspace: "feat-1"},
		{ID: "b", Name: "master-api", Project: "comment-slayer", Workspace: "master"},
	}
	selected, err := selectContainerTarget(containers, "workspace", targetMatchExact)
	if err != nil {
		t.Fatalf("selectContainerTarget(workspace) returned error: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("selected(workspace) = %#v", selected)
	}

	selectedSlash, err := selectContainerTarget(containers, "workspace/", targetMatchExact)
	if err != nil {
		t.Fatalf("selectContainerTarget(workspace/) returned error: %v", err)
	}
	if len(selectedSlash) != 2 {
		t.Fatalf("selected(workspace/) = %#v", selectedSlash)
	}
}

func TestSelectContainerTargetRequiresWorkspacePathToStartWithProject(t *testing.T) {
	t.Parallel()

	containers := []dockerContainer{{ID: "a", Name: "feat-1-api", Project: "comment-slayer", Workspace: "feat-1"}}
	if _, err := selectContainerTarget(containers, "workspace/feat-1", targetMatchExact); err == nil {
		t.Fatal("expected workspace target without project/workspace segments to fail")
	}
}

func TestSelectManagedTargetRequiresWorkspacePathToStartWithProject(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{{ID: "a", Name: "feat-1-api", Project: "comment-slayer", Workspace: "feat-1", Running: true}}}
	if _, err := selectManagedTarget(state, "workspace/feat-1", targetMatchExact); err == nil {
		t.Fatal("expected managed workspace target without project/workspace segments to fail")
	}
}

func TestSelectManagedTargetSupportsWorkspaceContainerLeafByService(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{{ID: "a", Name: "csdev-anonymize-deletions-6496c91a-clickhouse-1", Service: "clickhouse", Project: "comment-slayer", Workspace: "anonymize-deletions", Running: true}}}
	selected, err := selectManagedTarget(state, "workspace/comment-slayer/anonymize-deletions/clickhouse", targetMatchExact)
	if err != nil {
		t.Fatalf("selectManagedTarget returned error: %v", err)
	}
	if len(selected.ContainerIDs) != 1 || selected.ContainerIDs[0] != "a" {
		t.Fatalf("container ids = %#v", selected.ContainerIDs)
	}
}
