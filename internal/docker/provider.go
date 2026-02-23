package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var dialTCP = func(address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", address, timeout)
}

type ServiceRoute struct {
	ContainerID   string
	ContainerName string
	ServiceName   string
	Project       string
	Workspace     string
	WorkspacePath string
	ContainerIP   string
	NetworkName   string
	NetworkID     string
	HostLabels    []string
	HTTPPort      int
	TCPPorts      []int
	HTTPEnabled   bool
	DefaultHTTP   bool
	DetectManual  bool
}

type Diagnostics struct {
	Warnings []string
	Errors   []string
}

type WatchStats struct {
	Running      bool
	RestartCount int
	LastStartAt  time.Time
	LastEventAt  time.Time
	LastError    string
}

type Provider struct {
	watchMu    sync.Mutex
	watchStats WatchStats
}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Discover(ctx context.Context) ([]ServiceRoute, Diagnostics, error) {
	ids, err := p.containerIDs(ctx)
	if err != nil {
		return nil, Diagnostics{}, err
	}
	if len(ids) == 0 {
		return nil, Diagnostics{}, nil
	}

	inspectList, err := p.inspectContainers(ctx, ids)
	if err != nil {
		return nil, Diagnostics{}, err
	}

	routes := make([]ServiceRoute, 0, len(ids))
	diag := Diagnostics{}

	for _, inspect := range inspectList {
		route, skip, rd := parseInspect(inspect)
		diag.Warnings = append(diag.Warnings, rd.Warnings...)
		diag.Errors = append(diag.Errors, rd.Errors...)
		if skip {
			continue
		}
		routes = append(routes, route)
	}

	if err := validateDefaultHTTP(routes); err != nil {
		diag.Errors = append(diag.Errors, err.Error())
	}
	if err := validateHTTPPorts(routes); err != nil {
		diag.Errors = append(diag.Errors, err.Error())
	}

	return routes, diag, nil
}

func (p *Provider) Watch(ctx context.Context, trigger chan<- struct{}) error {
	go func() {
		backoff := 200 * time.Millisecond
		for {
			if ctx.Err() != nil {
				p.updateWatchStats(func(s *WatchStats) {
					s.Running = false
				})
				return
			}

			cmd, err := dockerCommand(ctx, "events", "--format", "{{json .}}", "--filter", "type=container")
			if err != nil {
				p.updateWatchStats(func(s *WatchStats) {
					s.Running = false
					s.LastError = err.Error()
				})
				if !sleepOrDone(ctx, backoff) {
					return
				}
				if backoff < 3*time.Second {
					backoff *= 2
				}
				continue
			}

			stdout, err := cmd.StdoutPipe()
			if err != nil {
				p.updateWatchStats(func(s *WatchStats) {
					s.Running = false
					s.LastError = err.Error()
				})
				if !sleepOrDone(ctx, backoff) {
					return
				}
				if backoff < 3*time.Second {
					backoff *= 2
				}
				continue
			}

			if err := cmd.Start(); err != nil {
				p.updateWatchStats(func(s *WatchStats) {
					s.Running = false
					s.LastError = err.Error()
				})
				if !sleepOrDone(ctx, backoff) {
					return
				}
				if backoff < 3*time.Second {
					backoff *= 2
				}
				continue
			}

			p.updateWatchStats(func(s *WatchStats) {
				s.Running = true
				s.LastStartAt = time.Now()
				s.LastError = ""
			})
			backoff = 200 * time.Millisecond

			s := bufio.NewScanner(stdout)
			for s.Scan() {
				p.updateWatchStats(func(s *WatchStats) {
					s.LastEventAt = time.Now()
				})
				select {
				case <-ctx.Done():
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					p.updateWatchStats(func(s *WatchStats) {
						s.Running = false
					})
					return
				case trigger <- struct{}{}:
				default:
				}
			}

			waitErr := cmd.Wait()
			scanErr := s.Err()
			errText := "docker watch stream ended"
			if scanErr != nil {
				errText = scanErr.Error()
			} else if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
				errText = waitErr.Error()
			} else if errors.Is(waitErr, io.EOF) {
				errText = "docker watch reached EOF"
			}

			p.updateWatchStats(func(s *WatchStats) {
				s.Running = false
				s.RestartCount++
				s.LastError = errText
			})

			select {
			case trigger <- struct{}{}:
			default:
			}

			if !sleepOrDone(ctx, backoff) {
				return
			}
			if backoff < 3*time.Second {
				backoff *= 2
			}
		}
	}()

	return nil
}

func (p *Provider) WatchStats() WatchStats {
	p.watchMu.Lock()
	defer p.watchMu.Unlock()
	return p.watchStats
}

func (p *Provider) updateWatchStats(apply func(*WatchStats)) {
	p.watchMu.Lock()
	defer p.watchMu.Unlock()
	apply(&p.watchStats)
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *Provider) containerIDs(ctx context.Context) ([]string, error) {
	cmd, err := dockerCommand(ctx, "ps", "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}

func (p *Provider) inspectContainers(ctx context.Context, ids []string) ([]dockerInspect, error) {
	args := make([]string, 0, len(ids)+1)
	args = append(args, "inspect")
	args = append(args, ids...)
	cmd, err := dockerCommand(ctx, args...)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var list []dockerInspect
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("empty inspect response")
	}
	return list, nil
}

func parseInspect(in dockerInspect) (ServiceRoute, bool, Diagnostics) {
	labels := in.Config.Labels

	serviceName := labels["com.docker.compose.service"]
	if serviceName == "" {
		serviceName = strings.TrimPrefix(in.Name, "/")
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return ServiceRoute{}, true, Diagnostics{}
	}

	diag := Diagnostics{}
	hostLabels := append([]string{serviceName}, parseCSV(labels["dnsvard.service_names"])...)
	hostLabels = uniqueHostLabels(hostLabels)

	httpPort := 0
	httpEnabled := false
	hasExplicitHTTPPort := false
	detectManual := strings.EqualFold(strings.TrimSpace(labels["dnsvard.detect"]), "manual")
	if mode := strings.TrimSpace(labels["dnsvard.detect"]); mode != "" && !strings.EqualFold(mode, "manual") {
		diag.Warnings = append(diag.Warnings, fmt.Sprintf("container %s has unknown dnsvard.detect=%q (expected manual)", in.ID, mode))
	}
	if hp := strings.TrimSpace(labels["dnsvard.http_port"]); hp != "" {
		httpEnabled = true
		hasExplicitHTTPPort = true
		v, err := strconv.Atoi(hp)
		if err != nil || v <= 0 || v > 65535 {
			diag.Errors = append(diag.Errors, fmt.Sprintf("container %s has invalid dnsvard.http_port=%q; use a valid TCP port", in.ID, hp))
			return ServiceRoute{}, true, diag
		}
		httpPort = v
	}

	defaultHTTP := strings.EqualFold(labels["dnsvard.default_http"], "true")
	if defaultHTTP {
		httpEnabled = true
	}

	if httpEnabled && !hasExplicitHTTPPort && httpPort == 0 {
		ports := candidateHTTPPorts(in)
		if len(ports) == 1 {
			httpPort = ports[0]
		} else if len(ports) > 1 && !detectManual {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf("container %s has multiple HTTP port candidates %v; set dnsvard.http_port or dnsvard.detect=manual", in.ID, ports))
		} else if len(ports) == 0 && !detectManual {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf("container %s HTTP port not detected; set dnsvard.http_port or dnsvard.detect=manual", in.ID))
		}
	}

	tcpPorts := candidateTCPPorts(in)
	networkName, networkID, containerIP := selectContainerNetwork(in, labels, preferredProbePorts(httpPort, tcpPorts))
	route := ServiceRoute{
		ContainerID:   in.ID,
		ContainerName: strings.TrimPrefix(in.Name, "/"),
		ServiceName:   serviceName,
		Project:       projectLabel(labels),
		Workspace:     workspaceLabel(labels),
		WorkspacePath: strings.TrimSpace(labels["com.docker.compose.project.working_dir"]),
		ContainerIP:   containerIP,
		NetworkName:   networkName,
		NetworkID:     networkID,
		HostLabels:    hostLabels,
		HTTPPort:      httpPort,
		TCPPorts:      tcpPorts,
		HTTPEnabled:   httpEnabled,
		DefaultHTTP:   defaultHTTP,
		DetectManual:  detectManual,
	}
	if route.ContainerIP == "" {
		diag.Errors = append(diag.Errors, fmt.Sprintf("container %s has no network IP; ensure it is attached to a network", in.ID))
		return ServiceRoute{}, true, diag
	}
	if len(in.NetworkSettings.Networks) > 1 {
		diag.Warnings = append(diag.Warnings, fmt.Sprintf("container %s attached to multiple networks; selected %s (%s)", in.ID, route.NetworkName, route.ContainerIP))
	}

	return route, false, diag
}

func validateDefaultHTTP(routes []ServiceRoute) error {
	defaultsByWorkspace := map[string]int{}
	for _, r := range routes {
		if !r.DefaultHTTP {
			continue
		}
		workspaceKey := scopeKey(r.Project, r.Workspace)
		defaultsByWorkspace[workspaceKey]++
		if defaultsByWorkspace[workspaceKey] <= 1 {
			continue
		}
		if strings.TrimSpace(r.Project) != "" || strings.TrimSpace(r.Workspace) != "" {
			return fmt.Errorf("multiple services declare dnsvard.default_http=true in workspace %q/%q; keep exactly one default service per workspace\nfix: in your compose labels keep dnsvard.default_http=true on one service only", r.Project, r.Workspace)
		}
		return errors.New("multiple services declare dnsvard.default_http=true; keep exactly one default service per workspace\nfix: in your compose labels keep dnsvard.default_http=true on one service only")
	}
	return nil
}

func scopeKey(project string, workspace string) string {
	return strings.ToLower(strings.TrimSpace(project)) + "|" + strings.ToLower(strings.TrimSpace(workspace))
}

func projectLabel(labels map[string]string) string {
	if v := strings.TrimSpace(labels["dnsvard.project"]); v != "" {
		return v
	}
	if wd := strings.TrimSpace(labels["com.docker.compose.project.working_dir"]); wd != "" {
		parent := filepath.Base(filepath.Dir(wd))
		if parent != "." && parent != string(filepath.Separator) && strings.TrimSpace(parent) != "" {
			return parent
		}
		base := filepath.Base(wd)
		if base != "." && base != string(filepath.Separator) && strings.TrimSpace(base) != "" {
			return base
		}
	}
	if v := strings.TrimSpace(labels["com.docker.compose.project"]); v != "" {
		return v
	}
	return ""
}

func workspaceLabel(labels map[string]string) string {
	if v := strings.TrimSpace(labels["dnsvard.workspace"]); v != "" {
		return v
	}
	if wd := strings.TrimSpace(labels["com.docker.compose.project.working_dir"]); wd != "" {
		base := filepath.Base(wd)
		if base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	if v := strings.TrimSpace(labels["com.docker.compose.project"]); v != "" {
		return v
	}
	return ""
}

func validateHTTPPorts(routes []ServiceRoute) error {
	for _, r := range routes {
		if !r.HTTPEnabled {
			continue
		}
		if r.HTTPPort <= 0 {
			return fmt.Errorf("service %s has no HTTP backend port\nfix: add labels:\n  dnsvard.http_port=3000\noptional silence: dnsvard.detect=manual", r.ServiceName)
		}
	}
	return nil
}

func uniqueHostLabels(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		n := strings.TrimSpace(v)
		if n == "" {
			continue
		}
		k := strings.ToLower(n)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, n)
	}
	return out
}

func parseCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

type dockerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels       map[string]string `json:"Labels"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
	} `json:"Config"`
	NetworkSettings struct {
		Ports    map[string]any `json:"Ports"`
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
			NetworkID string `json:"NetworkID"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

func candidateHTTPPorts(in dockerInspect) []int {
	all := candidateTCPPorts(in)
	if len(all) == 0 {
		return nil
	}
	remaining := map[int]struct{}{}
	for _, p := range all {
		remaining[p] = struct{}{}
	}

	priority := []int{80, 3000, 5173, 8080, 8888, 14000}
	ordered := make([]int, 0, len(remaining))
	for _, p := range priority {
		if _, ok := remaining[p]; ok {
			ordered = append(ordered, p)
			delete(remaining, p)
		}
	}
	for p := range remaining {
		ordered = append(ordered, p)
	}
	return ordered
}

func candidateTCPPorts(in dockerInspect) []int {
	portsSet := map[int]struct{}{}

	for key := range in.Config.ExposedPorts {
		if p := parseTCPPort(key); p > 0 {
			portsSet[p] = struct{}{}
		}
	}
	for key := range in.NetworkSettings.Ports {
		if p := parseTCPPort(key); p > 0 {
			portsSet[p] = struct{}{}
		}
	}

	ordered := make([]int, 0, len(portsSet))
	for p := range portsSet {
		ordered = append(ordered, p)
	}
	sort.Ints(ordered)
	return ordered
}

func parseTCPPort(v string) int {
	parts := strings.Split(v, "/")
	if len(parts) != 2 || strings.ToLower(parts[1]) != "tcp" {
		return 0
	}
	p, err := strconv.Atoi(parts[0])
	if err != nil || p <= 0 || p > 65535 {
		return 0
	}
	return p
}

func preferredProbePorts(httpPort int, tcpPorts []int) []int {
	set := map[int]struct{}{}
	out := make([]int, 0, len(tcpPorts)+1)
	if httpPort > 0 {
		set[httpPort] = struct{}{}
		out = append(out, httpPort)
	}
	for _, p := range tcpPorts {
		if p <= 0 {
			continue
		}
		if _, ok := set[p]; ok {
			continue
		}
		set[p] = struct{}{}
		out = append(out, p)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func selectContainerNetwork(in dockerInspect, labels map[string]string, probePorts []int) (string, string, string) {
	type candidate struct {
		name string
		id   string
		ip   string
	}
	candidates := make([]candidate, 0, len(in.NetworkSettings.Networks))
	for name, nw := range in.NetworkSettings.Networks {
		ip := strings.TrimSpace(nw.IPAddress)
		if ip == "" {
			continue
		}
		if parsed := net.ParseIP(ip); parsed == nil {
			continue
		}
		candidates = append(candidates, candidate{name: name, id: strings.TrimSpace(nw.NetworkID), ip: ip})
	}
	if len(candidates) == 0 {
		return "", "", ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].name < candidates[j].name
	})

	ordered := make([]candidate, 0, len(candidates))
	appendByName := func(name string) bool {
		for _, c := range candidates {
			if c.name == name {
				ordered = append(ordered, c)
				return true
			}
		}
		return false
	}

	composeProject := strings.TrimSpace(labels["com.docker.compose.project"])
	if composeProject != "" {
		preferred := composeProject + "_default"
		_ = appendByName(preferred)
	}
	_ = appendByName("default")
	for _, c := range candidates {
		seen := false
		for _, o := range ordered {
			if o.name == c.name {
				seen = true
				break
			}
		}
		if !seen {
			ordered = append(ordered, c)
		}
	}

	if len(probePorts) > 0 && len(ordered) > 1 {
		for _, c := range ordered {
			if containerIPReachable(c.ip, probePorts) {
				return c.name, c.id, c.ip
			}
		}
	}
	selected := ordered[0]
	return selected.name, selected.id, selected.ip
}

func containerIPReachable(ip string, ports []int) bool {
	for _, p := range ports {
		if p <= 0 {
			continue
		}
		conn, err := dialTCP(net.JoinHostPort(ip, strconv.Itoa(p)), 150*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return true
	}
	return false
}

func ReconcileLoop(ctx context.Context, interval time.Duration, trigger <-chan struct{}, fn func(context.Context) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	failureStreak := 0
	run := func() {
		err := fn(ctx)
		if err == nil {
			failureStreak = 0
			return
		}
		failureStreak++
		if failureStreak >= 3 {
			sleep := time.Duration(failureStreak) * time.Second
			if sleep > 10*time.Second {
				sleep = 10 * time.Second
			}
			timer := time.NewTimer(sleep)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		case <-trigger:
			run()
		}
	}
}

func dockerCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	path, err := dockerPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(cmd.Env, "PATH="+defaultPathEnv())
	return cmd, nil
}

func dockerPath() (string, error) {
	search := []string{"docker", "/opt/homebrew/bin/docker", "/usr/local/bin/docker", "/usr/bin/docker"}
	for _, candidate := range search {
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
	}
	return "", errors.New("docker executable not found")
}

func defaultPathEnv() string {
	return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}
