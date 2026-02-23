package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelectOwnedForegroundDaemonPIDsRejectsForgedPattern(t *testing.T) {
	t.Parallel()

	identity := executableIdentity{BaseName: "dnsvard"}
	processes := []processSnapshot{
		{PID: 120, UID: 501, Command: "/bin/sh -c dnsvard daemon start --foreground"},
		{PID: 121, UID: 501, Command: "dnsvard daemon start --foreground"},
		{PID: 122, UID: 777, Command: "dnsvard daemon start --foreground"},
		{PID: 123, UID: 501, Command: "dnsvard daemon start"},
		{PID: 124, UID: 501, Command: "dnsvard daemon start --foreground"},
	}

	got := selectOwnedForegroundDaemonPIDs(processes, 124, 501, identity)
	want := []int{121}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected pids = %v, want %v", got, want)
	}
}

func TestResolvePrivilegedBindRevocationTargetRejectsTamperedMarker(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	binaryPath := filepath.Join(stateDir, "dnsvard")
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	marker, err := buildPrivilegedBindMarker(binaryPath)
	if err != nil {
		t.Fatalf("build marker: %v", err)
	}
	marker.Inode++

	b, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(privilegedBindMarkerPath(stateDir), b, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	path, ok, err := resolvePrivilegedBindRevocationTarget(stateDir)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !errors.Is(err, errPrivilegedBindMarkerValidation) {
		t.Fatalf("error = %v, want validation error", err)
	}
	if ok {
		t.Fatal("expected no revocation target")
	}
	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
}

func TestResolvePrivilegedBindRevocationTargetStaleMarkerNoop(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	binaryPath := filepath.Join(stateDir, "dnsvard")
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	marker, err := buildPrivilegedBindMarker(binaryPath)
	if err != nil {
		t.Fatalf("build marker: %v", err)
	}
	if err := os.Remove(binaryPath); err != nil {
		t.Fatalf("remove binary: %v", err)
	}

	b, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	markerPath := privilegedBindMarkerPath(stateDir)
	if err := os.WriteFile(markerPath, b, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	path, ok, err := resolvePrivilegedBindRevocationTarget(stateDir)
	if err != nil {
		t.Fatalf("resolve target error: %v", err)
	}
	if ok {
		t.Fatal("expected no revocation target for stale marker")
	}
	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale marker should be removed, stat error = %v", err)
	}
}
