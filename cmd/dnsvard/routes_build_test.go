package main

import (
	"errors"
	"testing"

	"github.com/comment-slayer/dnsvard/internal/config"
)

func TestEffectiveRoutingForScopeWithoutWorkspacePathUsesBaseConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Domain: "test", HostPattern: "service-workspace-tld"}
	gotDomain, gotPattern := effectiveRoutingForScope(
		cfg,
		"",
		func(string) (config.Config, error) {
			t.Fatal("scope loader should not run when path empty")
			return config.Config{}, nil
		},
		func(string) {},
	)
	if gotDomain != "test" {
		t.Fatalf("domain = %q, want %q", gotDomain, "test")
	}
	if gotPattern != "service-workspace-tld" {
		t.Fatalf("pattern = %q, want %q", gotPattern, "service-workspace-tld")
	}
}

func TestEffectiveRoutingForScopePrefersScopeConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Domain: "test", HostPattern: "service-workspace-tld"}

	loaded := false
	warnings := []string{}
	domain, pattern := effectiveRoutingForScope(
		cfg,
		"/tmp/project-name/feat-1",
		func(path string) (config.Config, error) {
			loaded = true
			if path != "/tmp/project-name/feat-1" {
				t.Fatalf("load path = %q", path)
			}
			return config.Config{Domain: "test", HostPattern: "service-workspace-tld"}, nil
		},
		func(msg string) { warnings = append(warnings, msg) },
	)
	if !loaded {
		t.Fatal("expected scope config loader call")
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if domain != "test" {
		t.Fatalf("domain = %q, want %q", domain, "test")
	}
	if pattern != "service-workspace-tld" {
		t.Fatalf("pattern = %q, want %q", pattern, "service-workspace-tld")
	}
}

func TestEffectiveRoutingForScopeFallsBackOnLoadError(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Domain: "test", HostPattern: "service-workspace-tld"}

	warnings := []string{}
	domain, pattern := effectiveRoutingForScope(
		cfg,
		"/tmp/project-name/feat-1",
		func(string) (config.Config, error) {
			return config.Config{}, errors.New("boom")
		},
		func(msg string) { warnings = append(warnings, msg) },
	)
	if domain != "test" {
		t.Fatalf("domain fallback = %q, want %q", domain, "test")
	}
	if pattern != "service-workspace-tld" {
		t.Fatalf("pattern fallback = %q, want %q", pattern, "service-workspace-tld")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings len = %d, want 1", len(warnings))
	}
}
