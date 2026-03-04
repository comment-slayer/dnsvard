package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/comment-slayer/dnsvard/internal/config"
)

func TestDoctorDesiredManagedSuffixesFallsBackToConfigDomains(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.Domains = []string{"Dnsvard", "test"}

	got := doctorDesiredManagedSuffixes(cfg)
	want := []string{"dnsvard", "test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("doctorDesiredManagedSuffixes = %#v, want %#v", got, want)
	}
}

func TestDoctorDesiredManagedSuffixesUsesResolverSyncStateWhenPresent(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	stateFile := resolverSyncStatePath(stateDir)
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	content := []byte("{\n  \"domains\": [\"cs\", \"Dnsvard\"],\n  \"dns_listen\": \"127.0.0.1:1053\"\n}\n")
	if err := os.WriteFile(stateFile, content, 0o644); err != nil {
		t.Fatalf("write resolver sync state: %v", err)
	}

	cfg := config.Default()
	cfg.StateDir = stateDir
	cfg.Domains = []string{"test"}

	got := doctorDesiredManagedSuffixes(cfg)
	want := []string{"cs", "dnsvard"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("doctorDesiredManagedSuffixes = %#v, want %#v", got, want)
	}
}
