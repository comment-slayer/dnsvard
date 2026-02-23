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
		"project/project-name",
		"workspace/feat-1",
		"workspace/feat-1@project-name",
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
		"project/project-name",
		"workspace/feat-1",
		"workspace/feat-1@project-name",
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

	got := filterCompletionCandidates([]string{"all", "project/foo", "project/foo", "workspace/bar", "container/api"}, "project/")
	want := []string{"project/foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates = %#v, want %#v", got, want)
	}
}
