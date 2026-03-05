package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionShellFromValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "bash", want: "bash"},
		{in: "zsh", want: "zsh"},
		{in: "fish", want: "fish"},
		{in: "pwsh", want: "powershell"},
		{in: "powershell.exe", want: "powershell"},
		{in: "", want: ""},
		{in: "unknown", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := completionShellFromValue(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("completionShellFromValue(%q) returned error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("completionShellFromValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUpsertManagedBlockAndRemoveManagedBlock(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "rc")
	begin := "# >>> dnsvard completion >>>"
	end := "# <<< dnsvard completion <<<"
	block := "echo dnsvard"

	changed, err := upsertManagedBlock(filePath, begin, end, block)
	if err != nil {
		t.Fatalf("upsertManagedBlock returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected first upsert to change file")
	}

	b, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read rc file: %v", err)
	}
	v := string(b)
	if !strings.Contains(v, begin) || !strings.Contains(v, end) || !strings.Contains(v, block) {
		t.Fatalf("managed block missing from file:\n%s", v)
	}

	changed, err = upsertManagedBlock(filePath, begin, end, block)
	if err != nil {
		t.Fatalf("second upsertManagedBlock returned error: %v", err)
	}
	if changed {
		t.Fatal("expected second upsert to be no-op")
	}

	removed, err := removeManagedBlock(filePath, begin, end)
	if err != nil {
		t.Fatalf("removeManagedBlock returned error: %v", err)
	}
	if !removed {
		t.Fatal("expected managed block removal")
	}

	b, err = os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read rc file after removal: %v", err)
	}
	if strings.Contains(string(b), begin) || strings.Contains(string(b), end) {
		t.Fatalf("managed block still present:\n%s", string(b))
	}
}

func TestCompletionScriptBashIncludesCompatShim(t *testing.T) {
	t.Parallel()

	root := newRootCommand(context.Background())
	script, err := completionScript(root, "bash")
	if err != nil {
		t.Fatalf("completionScript returned error: %v", err)
	}
	if !strings.Contains(script, "_get_comp_words_by_ref") {
		t.Fatalf("bash script missing compatibility shim")
	}
	if !strings.Contains(script, "__start_dnsvard") {
		t.Fatalf("bash script missing generated cobra completion function")
	}
}

func TestBashCompletionRCPathSelection(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	bashProfile := filepath.Join(home, ".bash_profile")
	bashRC := filepath.Join(home, ".bashrc")

	if got := bashCompletionRCPath(home); got != bashProfile {
		t.Fatalf("rc path without files = %q, want %q", got, bashProfile)
	}

	if err := os.WriteFile(bashRC, []byte("# rc\n"), 0o644); err != nil {
		t.Fatalf("write .bashrc: %v", err)
	}
	if got := bashCompletionRCPath(home); got != bashRC {
		t.Fatalf("rc path with only .bashrc = %q, want %q", got, bashRC)
	}

	if err := os.WriteFile(bashProfile, []byte("# profile\n"), 0o644); err != nil {
		t.Fatalf("write .bash_profile: %v", err)
	}
	if got := bashCompletionRCPath(home); got != bashProfile {
		t.Fatalf("rc path with both files and no source = %q, want %q", got, bashProfile)
	}

	if err := os.WriteFile(bashProfile, []byte("[ -f ~/.bashrc ] && . ~/.bashrc\n"), 0o644); err != nil {
		t.Fatalf("rewrite .bash_profile: %v", err)
	}
	if got := bashCompletionRCPath(home); got != bashRC {
		t.Fatalf("rc path when profile sources .bashrc = %q, want %q", got, bashRC)
	}
}

func TestCompletionInstallTargetUsesEvalInRCBlock(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	bashTarget, err := completionInstallTarget("bash", home)
	if err != nil {
		t.Fatalf("completionInstallTarget(bash) returned error: %v", err)
	}
	if strings.TrimSpace(bashTarget.scriptPath) != "" {
		t.Fatalf("bash target scriptPath = %q, want empty for eval mode", bashTarget.scriptPath)
	}
	if !strings.Contains(bashTarget.rcBlock, "eval \"$(dnsvard completion bash)\"") {
		t.Fatalf("bash rcBlock missing eval completion line: %q", bashTarget.rcBlock)
	}

	zshTarget, err := completionInstallTarget("zsh", home)
	if err != nil {
		t.Fatalf("completionInstallTarget(zsh) returned error: %v", err)
	}
	if strings.TrimSpace(zshTarget.scriptPath) != "" {
		t.Fatalf("zsh target scriptPath = %q, want empty for eval mode", zshTarget.scriptPath)
	}
	if !strings.Contains(zshTarget.rcBlock, "eval \"$(dnsvard completion zsh)\"") {
		t.Fatalf("zsh rcBlock missing eval completion line: %q", zshTarget.rcBlock)
	}

	fishTarget, err := completionInstallTarget("fish", home)
	if err != nil {
		t.Fatalf("completionInstallTarget(fish) returned error: %v", err)
	}
	if strings.TrimSpace(fishTarget.scriptPath) == "" {
		t.Fatal("fish target scriptPath should be set")
	}
}

func TestBashUsesBashRC(t *testing.T) {
	t.Parallel()

	if !bashUsesBashRC([]string{"/tmp/.bashrc"}) {
		t.Fatal("expected bashUsesBashRC to detect .bashrc path")
	}
	if bashUsesBashRC([]string{"/tmp/.bash_profile", "/tmp/.zshrc"}) {
		t.Fatal("expected bashUsesBashRC to ignore non-.bashrc paths")
	}
}
