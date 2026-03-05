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

	got := filterCompletionCandidates([]string{"all", "project/foo", "project/foo", "workspace/bar", "container/api"}, nil, "project/")
	want := []string{"project/foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates = %#v, want %#v", got, want)
	}
}

func TestFilterCompletionCandidatesDropsNamespacePlaceholderWhenSpecificExists(t *testing.T) {
	t.Parallel()

	got := filterCompletionCandidates([]string{"workspace/", "workspace/worker-duplication@comment-slayer"}, nil, "work")
	want := []string{"workspace/worker-duplication@comment-slayer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates namespace placeholder = %#v, want %#v", got, want)
	}
}

func TestFilterCompletionCandidatesAtWordbreakReturnsSuffix(t *testing.T) {
	line := "dnsvard rm workspace/anonymize-deletions@com"
	t.Setenv("COMP_LINE", line)
	t.Setenv("COMP_POINT", "44")

	got := filterCompletionCandidates([]string{
		"workspace/anonymize-deletions@comment-slayer",
		"workspace/other@comment-slayer",
	}, []string{"workspace/anonymize-deletions"}, "com")
	want := []string{"comment-slayer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates wordbreak = %#v, want %#v", got, want)
	}
}

func TestFilterCompletionCandidatesBashShimWordbreakEmptySuffix(t *testing.T) {
	line := "dnsvard rm workspace/anonymize-deletions@"
	t.Setenv("COMP_LINE", line)
	t.Setenv("COMP_POINT", "41")

	got := filterCompletionCandidates([]string{
		"workspace/anonymize-deletions@comment-slayer",
		"workspace/anonymize-deletions@other-project",
	}, []string{"workspace/anonymize-deletions"}, "")
	want := []string{"comment-slayer", "other-project"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates bash shim empty suffix = %#v, want %#v", got, want)
	}
}

func TestFilterCompletionCandidatesAtWordbreakIgnoresMismatchedContext(t *testing.T) {
	line := "dnsvard rm workspace/anonymize-deletions@"
	t.Setenv("COMP_LINE", line)
	t.Setenv("COMP_POINT", "41")

	got := filterCompletionCandidates([]string{"workspace/anonymize-deletions@comment-slayer"}, []string{"workspace/other"}, "")
	want := []string{"workspace/anonymize-deletions@comment-slayer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates mismatch = %#v, want %#v", got, want)
	}
}

func TestFilterCompletionCandidatesAtWordbreakAcceptsArgWithTrailingAt(t *testing.T) {
	line := "dnsvard rm workspace/anonymize-deletions@"
	t.Setenv("COMP_LINE", line)
	t.Setenv("COMP_POINT", "41")

	got := filterCompletionCandidates([]string{"workspace/anonymize-deletions@comment-slayer"}, []string{"workspace/anonymize-deletions@"}, "")
	want := []string{"comment-slayer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCompletionCandidates trailing @ arg = %#v, want %#v", got, want)
	}
}

func TestSplitWordbreakAtContextWithoutEnv(t *testing.T) {
	t.Setenv("COMP_LINE", "")
	t.Setenv("COMP_POINT", "")
	if _, _, ok := splitWordbreakAtContext(nil, "com"); ok {
		t.Fatal("expected splitWordbreakAtContext to be disabled without shell env")
	}
}
