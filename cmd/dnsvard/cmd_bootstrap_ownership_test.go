package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnershipPathsNoSymlinkIncludesRegularTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fileA := filepath.Join(dirA, "file.txt")
	if err := os.WriteFile(fileA, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	paths, err := ownershipPathsNoSymlink(root)
	if err != nil {
		t.Fatalf("ownershipPathsNoSymlink returned error: %v", err)
	}
	if !containsPath(paths, root) || !containsPath(paths, dirA) || !containsPath(paths, fileA) {
		t.Fatalf("unexpected ownership path set: %#v", paths)
	}
}

func TestOwnershipPathsNoSymlinkSkipsSymlinkEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	linkPath := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	fileLinkPath := filepath.Join(root, "outside-file-link")
	if err := os.Symlink(outsideFile, fileLinkPath); err != nil {
		t.Fatalf("symlink file: %v", err)
	}

	paths, err := ownershipPathsNoSymlink(root)
	if err != nil {
		t.Fatalf("ownershipPathsNoSymlink returned error: %v", err)
	}
	for _, p := range paths {
		if strings.Contains(p, "outside-link") || strings.Contains(p, "outside-file-link") {
			t.Fatalf("symlink path should not be included: %q (all=%#v)", p, paths)
		}
		if strings.HasPrefix(p, outside) {
			t.Fatalf("outside target should not be included: %q (all=%#v)", p, paths)
		}
	}
}

func TestOwnershipPathsNoSymlinkRejectsSymlinkRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, linkRoot); err != nil {
		t.Fatalf("symlink root: %v", err)
	}

	_, err := ownershipPathsNoSymlink(linkRoot)
	if err == nil {
		t.Fatal("expected symlink root rejection")
	}
	if !strings.Contains(err.Error(), "symlinked root path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func containsPath(paths []string, path string) bool {
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}
