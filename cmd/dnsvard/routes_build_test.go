package main

import (
	"errors"
	"testing"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/docker"
	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
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

func TestPickServiceRouteTargetAvoidsQuarantinedTarget(t *testing.T) {
	t.Parallel()

	now := time.Now()
	route := docker.ServiceRoute{
		ContainerIP:  "192.168.10.7",
		CandidateIPs: []string{"192.168.10.8"},
		HTTPPort:     8888,
	}
	avoid := map[string]time.Time{"http://192.168.10.7:8888": now.Add(30 * time.Second)}

	target, avoided, switched := pickServiceRouteTarget(route, avoid, now)
	if target != "http://192.168.10.8:8888" {
		t.Fatalf("target = %q", target)
	}
	if avoided != "http://192.168.10.7:8888" || !switched {
		t.Fatalf("avoided=%q switched=%t", avoided, switched)
	}
}

func TestPickServiceRouteTargetFallsBackWhenAllQuarantined(t *testing.T) {
	t.Parallel()

	now := time.Now()
	route := docker.ServiceRoute{
		ContainerIP:  "192.168.10.7",
		CandidateIPs: []string{"192.168.10.8"},
		HTTPPort:     8888,
	}
	avoid := map[string]time.Time{
		"http://192.168.10.7:8888": now.Add(30 * time.Second),
		"http://192.168.10.8:8888": now.Add(30 * time.Second),
	}

	target, avoided, switched := pickServiceRouteTarget(route, avoid, now)
	if target != "http://192.168.10.7:8888" {
		t.Fatalf("target = %q", target)
	}
	if avoided != "" || switched {
		t.Fatalf("avoided=%q switched=%t", avoided, switched)
	}
}

func TestRuntimeLeaseDomainPrefersExplicitDomain(t *testing.T) {
	t.Parallel()

	lease := runtimeprovider.Lease{Domain: "Dnsvard", Hostnames: []string{"www.cs"}}
	if got := runtimeLeaseDomain(lease); got != "dnsvard" {
		t.Fatalf("runtimeLeaseDomain = %q, want %q", got, "dnsvard")
	}
}

func TestRuntimeLeaseDomainInfersFromHostnames(t *testing.T) {
	t.Parallel()

	lease := runtimeprovider.Lease{Hostnames: []string{"www.master.bs", "master.bs"}}
	if got := runtimeLeaseDomain(lease); got != "bs" {
		t.Fatalf("runtimeLeaseDomain = %q, want %q", got, "bs")
	}
}

func TestInferDomainFromHostnamesSupportsMultiLabelSuffix(t *testing.T) {
	t.Parallel()

	hosts := []string{"app.feature.dev.test", "feature.dev.test"}
	if got := inferDomainFromHostnames(hosts); got != "dev.test" {
		t.Fatalf("inferDomainFromHostnames = %q, want %q", got, "dev.test")
	}
}
