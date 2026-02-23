package main

import (
	"testing"
)

func TestBootstrapStateReadWrite(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	if err := writeBootstrapStateVersion(stateDir, 2); err != nil {
		t.Fatalf("writeBootstrapStateVersion returned error: %v", err)
	}

	version, ok, err := readBootstrapStateVersion(stateDir)
	if err != nil {
		t.Fatalf("readBootstrapStateVersion returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected bootstrap state file to exist")
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
}

func TestLoopbackHasResolverSyncFromStatus(t *testing.T) {
	t.Parallel()

	if !loopbackHasResolverSync("arguments = { --resolver-state-file }") {
		t.Fatal("expected status detection to return true")
	}
	if loopbackHasResolverSync("arguments = { --state-file }") {
		t.Fatal("expected status detection to return false")
	}
}
