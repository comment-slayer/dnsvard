package main

import (
	"reflect"
	"testing"

	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
)

func TestManagedTargetSuggestionsIncludeProjectWorkspaceAndLease(t *testing.T) {
	t.Parallel()

	state := managedState{
		Leases: []runtimeprovider.Lease{{ID: "run-a1"}},
		Containers: []dockerContainer{
			{ID: "1", Name: "feat-1-api-1", Project: "project-name", Workspace: "feat-1", Running: true},
		},
	}

	got := managedTargetSuggestions(state, true, true)
	want := []string{
		"container/feat-1-api-1",
		"lease/run-a1",
		"workspace/project-name/feat-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managedTargetSuggestions = %#v, want %#v", got, want)
	}
}

func TestManagedTargetSuggestionsRunningOnlyFiltersStopped(t *testing.T) {
	t.Parallel()

	state := managedState{
		Containers: []dockerContainer{
			{ID: "1", Name: "running-api", Project: "project-name", Workspace: "feat-1", Running: true},
			{ID: "2", Name: "stopped-api", Project: "project-name", Workspace: "feat-1", Running: false},
		},
	}

	got := managedTargetSuggestions(state, false, true)
	want := []string{
		"container/running-api",
		"workspace/project-name/feat-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managedTargetSuggestions runningOnly = %#v, want %#v", got, want)
	}

	gotAll := managedTargetSuggestions(state, false, false)
	if len(gotAll) <= len(got) {
		t.Fatalf("expected stopped container to be included when runningOnly=false, got %#v", gotAll)
	}
}

func TestFilterCompletionCandidatesByPrefixAndDedupe(t *testing.T) {
	t.Parallel()

	got := filterCompletionCandidates([]string{"all", "workspace/foo", "workspace/foo", "workspace/bar", "container/api"}, "workspace/f")
	want := []string{"workspace/foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates = %#v, want %#v", got, want)
	}
}

func TestFilterCompletionCandidatesDropsNamespacePlaceholderWhenSpecificExists(t *testing.T) {
	t.Parallel()

	got := filterCompletionCandidates([]string{"workspace/", "workspace/comment-slayer/worker-duplication"}, "work")
	want := []string{"workspace/comment-slayer/worker-duplication"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates namespace placeholder = %#v, want %#v", got, want)
	}
}

func TestManagedTargetSuggestionsAddsProjectScopeOnlyWhenProjectHasMultipleWorkspaces(t *testing.T) {
	t.Parallel()

	state := managedState{Containers: []dockerContainer{
		{ID: "1", Name: "feat-api", Project: "comment-slayer", Workspace: "feat-1", Running: true},
		{ID: "2", Name: "fix-api", Project: "comment-slayer", Workspace: "fix-1", Running: true},
	}}
	got := managedTargetSuggestions(state, false, true)
	want := []string{
		"container/feat-api",
		"container/fix-api",
		"workspace/comment-slayer",
		"workspace/comment-slayer/feat-1",
		"workspace/comment-slayer/fix-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("managedTargetSuggestions = %#v, want %#v", got, want)
	}
}
