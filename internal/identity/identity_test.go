package identity

import "testing"

func TestDeriveProjectLabelFromRemoteURL(t *testing.T) {
	t.Parallel()

	got, err := DeriveProjectLabel(ProjectInput{RemoteURL: "git@github.com:acme/comment-slayer.git"})
	if err != nil {
		t.Fatalf("DeriveProjectLabel returned error: %v", err)
	}

	if got != "comment-slayer" {
		t.Fatalf("DeriveProjectLabel = %q, want %q", got, "comment-slayer")
	}
}

func TestDeriveProjectLabelFallsBackToRepoBase(t *testing.T) {
	t.Parallel()

	got, err := DeriveProjectLabel(ProjectInput{RepoBase: "Portless"})
	if err != nil {
		t.Fatalf("DeriveProjectLabel returned error: %v", err)
	}

	if got != "portless" {
		t.Fatalf("DeriveProjectLabel = %q, want %q", got, "portless")
	}
}

func TestDeriveWorkspaceLabelOrder(t *testing.T) {
	t.Parallel()

	got, err := DeriveWorkspaceLabel(WorkspaceInput{
		WorktreeBase: "",
		Branch:       "feature/add-routing",
		CwdBase:      "fallback-dir",
	})
	if err != nil {
		t.Fatalf("DeriveWorkspaceLabel returned error: %v", err)
	}

	if got != "feature-add-routing" {
		t.Fatalf("DeriveWorkspaceLabel = %q, want %q", got, "feature-add-routing")
	}
}

func TestDeriveWorkspaceLabelFallbackDefault(t *testing.T) {
	t.Parallel()

	got, err := DeriveWorkspaceLabel(WorkspaceInput{})
	if err != nil {
		t.Fatalf("DeriveWorkspaceLabel returned error: %v", err)
	}
	if got != "default" {
		t.Fatalf("DeriveWorkspaceLabel = %q, want %q", got, "default")
	}
}

func TestDeriveProjectLabelFallbackProject(t *testing.T) {
	t.Parallel()

	got, err := DeriveProjectLabel(ProjectInput{RemoteURL: "/"})
	if err != nil {
		t.Fatalf("DeriveProjectLabel returned error: %v", err)
	}
	if got != "project" {
		t.Fatalf("DeriveProjectLabel = %q, want %q", got, "project")
	}
}

func TestHostnames(t *testing.T) {
	t.Parallel()

	set, err := Hostnames(HostnameInput{
		Domain:    "test",
		Project:   "comment-slayer",
		Workspace: "master",
		Service:   "frontend",
		Pattern:   "service-workspace-project-tld",
	})
	if err != nil {
		t.Fatalf("Hostnames returned error: %v", err)
	}

	if set.ProjectFQDN != "comment-slayer.test" {
		t.Fatalf("ProjectFQDN = %q", set.ProjectFQDN)
	}
	if set.WorkspaceFQDN != "master.comment-slayer.test" {
		t.Fatalf("WorkspaceFQDN = %q", set.WorkspaceFQDN)
	}
	if set.ServiceFQDN != "frontend.master.comment-slayer.test" {
		t.Fatalf("ServiceFQDN = %q", set.ServiceFQDN)
	}
}

func TestHostnamesWorkspaceTLDPattern(t *testing.T) {
	t.Parallel()

	set, err := Hostnames(HostnameInput{
		Domain:    "cs",
		Project:   "comment-slayer",
		Workspace: "master",
		Service:   "postgres",
		Pattern:   "service-workspace-tld",
	})
	if err != nil {
		t.Fatalf("Hostnames returned error: %v", err)
	}

	if set.WorkspaceFQDN != "master.cs" {
		t.Fatalf("WorkspaceFQDN = %q", set.WorkspaceFQDN)
	}
	if set.ServiceFQDN != "postgres.master.cs" {
		t.Fatalf("ServiceFQDN = %q", set.ServiceFQDN)
	}
}

func TestParseHostPattern(t *testing.T) {
	t.Parallel()

	pattern, err := ParseHostPattern("workspace-project-service-tld")
	if err != nil {
		t.Fatalf("ParseHostPattern returned error: %v", err)
	}
	if len(pattern.Tokens) != 4 {
		t.Fatalf("token count = %d", len(pattern.Tokens))
	}
	if !pattern.HasService {
		t.Fatal("expected HasService true")
	}
}

func TestParseHostPatternRequiresTLDLast(t *testing.T) {
	t.Parallel()

	_, err := ParseHostPattern("service-tld-workspace")
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveDefaultWorkspacePrefersMasterThenMain(t *testing.T) {
	t.Parallel()

	if got := ResolveDefaultWorkspace([]string{"main", "feature-x"}); got != "main" {
		t.Fatalf("ResolveDefaultWorkspace(main case) = %q, want %q", got, "main")
	}

	if got := ResolveDefaultWorkspace([]string{"feature-x", "master", "main"}); got != "master" {
		t.Fatalf("ResolveDefaultWorkspace(master case) = %q, want %q", got, "master")
	}
}

func TestWorkspaceIDStable(t *testing.T) {
	t.Parallel()

	a := WorkspaceID("/tmp/repo")
	b := WorkspaceID("/tmp/repo")
	if a != b {
		t.Fatalf("WorkspaceID is not stable: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Fatalf("WorkspaceID length = %d, want 8", len(a))
	}
}
