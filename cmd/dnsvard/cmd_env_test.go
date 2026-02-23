package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comment-slayer/dnsvard/internal/config"
)

func TestRunEnvShellIncludesSuffixAndWorkspace(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "cs"
	cfg.HostPattern = "workspace-tld"
	cfg.StateDir = t.TempDir()
	cfg.Project = config.ProjectInfo{RepoBase: "comment-slayer"}
	cfg.Workspace = config.WorkspaceInfo{
		WorktreeBase: "feat-2",
		Branch:       "feat-2",
		CwdBase:      "feat-2",
		Path:         filepath.Join(t.TempDir(), "feat-2"),
	}

	var out bytes.Buffer
	if err := runEnvTo(&out, cfg, true); err != nil {
		t.Fatalf("runEnv(shell): %v", err)
	}

	if !strings.Contains(out.String(), "export DNSVARD_SUFFIX=cs") {
		t.Fatalf("shell output missing DNSVARD_SUFFIX:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "export DNSVARD_WORKSPACE=feat-2") {
		t.Fatalf("shell output missing DNSVARD_WORKSPACE:\n%s", out.String())
	}
}

func TestRunEnvPlainIncludesSuffixAndWorkspace(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Domain = "cs"
	cfg.HostPattern = "workspace-tld"
	cfg.StateDir = t.TempDir()
	cfg.Project = config.ProjectInfo{RepoBase: "comment-slayer"}
	cfg.Workspace = config.WorkspaceInfo{
		WorktreeBase: "feat-2",
		Branch:       "feat-2",
		CwdBase:      "feat-2",
		Path:         filepath.Join(t.TempDir(), "feat-2"),
	}

	var out bytes.Buffer
	if err := runEnvTo(&out, cfg, false); err != nil {
		t.Fatalf("runEnv(plain): %v", err)
	}

	if !strings.Contains(out.String(), "DNSVARD_SUFFIX=cs") {
		t.Fatalf("plain output missing DNSVARD_SUFFIX:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "DNSVARD_WORKSPACE=feat-2") {
		t.Fatalf("plain output missing DNSVARD_WORKSPACE:\n%s", out.String())
	}
}
