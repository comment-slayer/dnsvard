package docker

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseInspectWithLabels(t *testing.T) {
	t.Parallel()

	in := dockerInspect{ID: "abc123", Name: "/frontend-1"}
	in.Config.Labels = map[string]string{
		"dnsvard.service_names":                  "frontend,ui",
		"dnsvard.http_port":                      "3000",
		"dnsvard.default_http":                   "true",
		"com.docker.compose.service":             "frontend",
		"com.docker.compose.project":             "master",
		"com.docker.compose.project.working_dir": "/workspace/cool-name/master",
	}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string "json:\"IPAddress\""
		NetworkID string "json:\"NetworkID\""
	}{"default": {IPAddress: "172.18.0.12"}}

	route, skip, diag := parseInspect(in)
	if skip {
		t.Fatal("expected service to be included")
	}
	if len(diag.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", diag.Errors)
	}
	if route.ServiceName != "frontend" {
		t.Fatalf("ServiceName = %q", route.ServiceName)
	}
	if route.HTTPPort != 3000 {
		t.Fatalf("HTTPPort = %d", route.HTTPPort)
	}
	if !route.HTTPEnabled {
		t.Fatal("expected HTTPEnabled true")
	}
	if route.ContainerIP != "172.18.0.12" {
		t.Fatalf("ContainerIP = %q", route.ContainerIP)
	}
	if !route.DefaultHTTP {
		t.Fatal("expected DefaultHTTP true")
	}
	if len(route.HostLabels) != 2 {
		t.Fatalf("HostLabels len = %d", len(route.HostLabels))
	}
	if route.Project != "cool-name" {
		t.Fatalf("Project = %q", route.Project)
	}
	if route.Workspace != "master" {
		t.Fatalf("Workspace = %q", route.Workspace)
	}
}

func TestParseInspectInvalidPort(t *testing.T) {
	t.Parallel()

	in := dockerInspect{ID: "abc123", Name: "/frontend-1"}
	in.Config.Labels = map[string]string{
		"dnsvard.http_port":     "not-a-port",
		"dnsvard.service_names": "frontend",
	}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string "json:\"IPAddress\""
		NetworkID string "json:\"NetworkID\""
	}{"default": {IPAddress: "172.18.0.12"}}

	_, skip, diag := parseInspect(in)
	if !skip {
		t.Fatal("expected skip on invalid port")
	}
	if len(diag.Errors) == 0 {
		t.Fatal("expected validation error")
	}
}

func TestValidateDefaultHTTP(t *testing.T) {
	t.Parallel()

	err := validateDefaultHTTP([]ServiceRoute{
		{ServiceName: "a", Project: "cool-name", Workspace: "master", DefaultHTTP: true},
		{ServiceName: "b", Project: "cool-name", Workspace: "master", DefaultHTTP: true},
	})
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestValidateDefaultHTTPDifferentWorkspaces(t *testing.T) {
	t.Parallel()

	err := validateDefaultHTTP([]ServiceRoute{
		{ServiceName: "a", Project: "cool-name", Workspace: "master", DefaultHTTP: true},
		{ServiceName: "b", Project: "cool-name", Workspace: "new-feat-1", DefaultHTTP: true},
	})
	if err != nil {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestDetectPortFromExposedPorts(t *testing.T) {
	t.Parallel()

	in := dockerInspect{ID: "abc123", Name: "/frontend-1"}
	in.Config.Labels = map[string]string{
		"com.docker.compose.service": "frontend",
	}
	in.Config.ExposedPorts = map[string]any{"14000/tcp": struct{}{}}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string "json:\"IPAddress\""
		NetworkID string "json:\"NetworkID\""
	}{"default": {IPAddress: "172.18.0.12"}}

	route, skip, diag := parseInspect(in)
	if skip {
		t.Fatal("expected include")
	}
	if len(diag.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", diag.Errors)
	}
	if route.HTTPPort != 0 {
		t.Fatalf("HTTPPort = %d, want 0", route.HTTPPort)
	}
	if route.HTTPEnabled {
		t.Fatal("expected HTTPEnabled false")
	}
}

func TestParseInspectAddsCanonicalServiceName(t *testing.T) {
	t.Parallel()

	in := dockerInspect{ID: "abc123", Name: "/postgres-1"}
	in.Config.Labels = map[string]string{
		"com.docker.compose.service": "postgres",
	}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string "json:\"IPAddress\""
		NetworkID string "json:\"NetworkID\""
	}{"default": {IPAddress: "172.18.0.12"}}

	route, skip, diag := parseInspect(in)
	if skip {
		t.Fatal("expected include")
	}
	if len(diag.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", diag.Errors)
	}
	if len(route.HostLabels) != 1 || route.HostLabels[0] != "postgres" {
		t.Fatalf("HostLabels = %#v", route.HostLabels)
	}
}

func TestWorkspaceLabelPrefersWorkingDirOverComposeProject(t *testing.T) {
	t.Parallel()

	in := dockerInspect{ID: "abc123", Name: "/frontend-1"}
	in.Config.Labels = map[string]string{
		"com.docker.compose.service":             "frontend",
		"com.docker.compose.project":             "csdev-master-6b9fe1af",
		"com.docker.compose.project.working_dir": "/worktrees/comment-slayer/master",
	}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string "json:\"IPAddress\""
		NetworkID string "json:\"NetworkID\""
	}{"default": {IPAddress: "172.18.0.12"}}

	route, skip, diag := parseInspect(in)
	if skip {
		t.Fatal("expected include")
	}
	if len(diag.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", diag.Errors)
	}
	if route.Workspace != "master" {
		t.Fatalf("Workspace = %q", route.Workspace)
	}
}

func TestSelectContainerNetworkPrefersComposeDefault(t *testing.T) {
	t.Parallel()

	in := dockerInspect{}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
		NetworkID string `json:"NetworkID"`
	}{
		"bridge":               {IPAddress: "172.20.0.9", NetworkID: "net-bridge"},
		"csdev-master_default": {IPAddress: "192.168.100.7", NetworkID: "net-default"},
	}
	labels := map[string]string{"com.docker.compose.project": "csdev-master"}

	name, id, ip := selectContainerNetwork(in, labels, nil)
	if name != "csdev-master_default" || id != "net-default" || ip != "192.168.100.7" {
		t.Fatalf("selected network = (%q,%q,%q)", name, id, ip)
	}
}

func TestSelectContainerNetworkFallsBackDeterministically(t *testing.T) {
	t.Parallel()

	in := dockerInspect{}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
		NetworkID string `json:"NetworkID"`
	}{
		"zeta":  {IPAddress: "172.20.0.9", NetworkID: "net-z"},
		"alpha": {IPAddress: "172.20.0.8", NetworkID: "net-a"},
	}

	name, id, ip := selectContainerNetwork(in, map[string]string{}, nil)
	if name != "alpha" || id != "net-a" || ip != "172.20.0.8" {
		t.Fatalf("selected network = (%q,%q,%q)", name, id, ip)
	}
}

func TestSelectContainerNetworkPrefersReachableCandidate(t *testing.T) {
	t.Parallel()

	in := dockerInspect{}
	in.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
		NetworkID string `json:"NetworkID"`
	}{
		"proj_default": {IPAddress: "192.168.100.7", NetworkID: "net-default"},
		"bridge":       {IPAddress: "172.20.0.9", NetworkID: "net-bridge"},
	}
	labels := map[string]string{"com.docker.compose.project": "proj"}

	originalDial := dialTCP
	dialTCP = func(address string, _ time.Duration) (net.Conn, error) {
		if strings.HasPrefix(address, "172.20.0.9:") {
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}
		return nil, errors.New("unreachable")
	}
	t.Cleanup(func() { dialTCP = originalDial })

	name, id, ip := selectContainerNetwork(in, labels, []int{8080})
	if name != "bridge" || id != "net-bridge" || ip != "172.20.0.9" {
		t.Fatalf("selected network = (%q,%q,%q)", name, id, ip)
	}
}
