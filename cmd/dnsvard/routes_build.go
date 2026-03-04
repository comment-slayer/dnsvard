package main

import (
	"context"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/comment-slayer/dnsvard/internal/allocator"
	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/dnsserver"
	"github.com/comment-slayer/dnsvard/internal/docker"
	"github.com/comment-slayer/dnsvard/internal/httprouter"
	"github.com/comment-slayer/dnsvard/internal/identity"
	"github.com/comment-slayer/dnsvard/internal/logx"
	"github.com/comment-slayer/dnsvard/internal/routes"
	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
	"github.com/comment-slayer/dnsvard/internal/tcpproxy"
)

type workspaceScope struct {
	project   string
	workspace string
	path      string
	ip        netip.Addr
}

type buildRoutesInput struct {
	Cfg             config.Config
	Project         string
	Workspace       string
	WorkspaceIP     netip.Addr
	Allocator       *allocator.Allocator
	Provider        *docker.Provider
	RuntimeProvider *runtimeprovider.Provider
	AvoidHTTPTarget map[string]time.Time
	Now             time.Time
}

type buildRoutesResult struct {
	Records              []dnsserver.Record
	HTTPRoutes           []httprouter.Route
	TCPRoutes            []tcpproxy.Route
	ManagedDomains       []string
	WorkspaceDomains     map[string]string
	Warnings             []string
	WorkspaceConfigFiles []string
}

func buildRoutes(in buildRoutesInput) (buildRoutesResult, error) {
	cfg := in.Cfg
	project := in.Project
	workspace := in.Workspace
	workspaceIP := in.WorkspaceIP
	alloc := in.Allocator
	provider := in.Provider
	runtimeProvider := in.RuntimeProvider
	routingWarningSet := map[string]struct{}{}
	routingWarnings := []string{}
	managedDomainSet := map[string]struct{}{}
	workspaceDomainMap := map[string]string{}
	type cachedScopeConfig struct {
		cfg config.Config
		err error
	}
	scopeConfigCache := map[string]cachedScopeConfig{}
	loadScopeConfig := func(scopePath string) (config.Config, error) {
		key := filepath.Clean(strings.TrimSpace(scopePath))
		if key == "" {
			return config.Config{}, nil
		}
		if cached, ok := scopeConfigCache[key]; ok {
			return cached.cfg, cached.err
		}
		loaded, err := config.Load(config.LoadOptions{CWD: key})
		scopeConfigCache[key] = cachedScopeConfig{cfg: loaded, err: err}
		return loaded, err
	}
	pushRoutingWarning := func(msg string) {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			return
		}
		if _, ok := routingWarningSet[msg]; ok {
			return
		}
		routingWarningSet[msg] = struct{}{}
		routingWarnings = append(routingWarnings, msg)
	}
	hostnamesForScope := func(scope workspaceScope, service string) (identity.HostnameSet, error) {
		scopeDomain, scopePattern := effectiveRoutingForScope(cfg, scope.path, loadScopeConfig, pushRoutingWarning)
		domain := strings.ToLower(strings.Trim(strings.TrimSpace(scopeDomain), "."))
		if domain != "" {
			managedDomainSet[domain] = struct{}{}
		}
		hosts, err := identity.Hostnames(identity.HostnameInput{
			Domain:    scopeDomain,
			Project:   scope.project,
			Workspace: scope.workspace,
			Service:   service,
			Pattern:   scopePattern,
		})
		if err != nil {
			return identity.HostnameSet{}, err
		}
		if fqdn := strings.TrimSpace(hosts.WorkspaceFQDN); fqdn != "" {
			workspaceDomainMap[scope.workspace+"@"+scope.project] = fqdn
		}
		return hosts, nil
	}

	entries := []routes.Entry{}
	httpRoutes := []httprouter.Route{}
	tcpRoutes := []tcpproxy.Route{}
	workspaceScopes := map[string]workspaceScope{}

	workspaceKey := func(projectLabel string, workspaceLabel string) string {
		return identity.NormalizeLabel(projectLabel) + "|" + identity.NormalizeLabel(workspaceLabel)
	}

	upsertScope := func(projectLabel string, workspaceLabel string, path string, preferredIP netip.Addr) (workspaceScope, error) {
		projectLabel = identity.NormalizeLabel(projectLabel)
		workspaceLabel = identity.NormalizeLabel(workspaceLabel)
		if projectLabel == "" {
			projectLabel = identity.NormalizeLabel(project)
		}
		if workspaceLabel == "" {
			workspaceLabel = identity.NormalizeLabel(workspace)
		}
		if projectLabel == "" || workspaceLabel == "" {
			return workspaceScope{}, fmt.Errorf("workspace scope requires project/workspace labels")
		}

		key := workspaceKey(projectLabel, workspaceLabel)
		if existing, ok := workspaceScopes[key]; ok {
			if existing.path == "" && strings.TrimSpace(path) != "" {
				existing.path = strings.TrimSpace(path)
				workspaceScopes[key] = existing
			}
			return existing, nil
		}

		scope := workspaceScope{
			project:   projectLabel,
			workspace: workspaceLabel,
			path:      strings.TrimSpace(path),
		}
		if preferredIP.IsValid() {
			scope.ip = preferredIP
		} else {
			workspaceID := ""
			if scope.path != "" {
				workspaceID = identity.WorkspaceID(scope.path)
			} else {
				workspaceID = identity.WorkspaceID(scope.project + "/" + scope.workspace)
			}
			ip, err := alloc.Allocate(workspaceID)
			if err != nil {
				return workspaceScope{}, err
			}
			scope.ip = ip
		}
		workspaceScopes[key] = scope
		return scope, nil
	}

	if _, err := upsertScope(project, workspace, cfg.Workspace.Path, workspaceIP); err != nil {
		return buildRoutesResult{}, err
	}

	serviceRoutes, diag, err := provider.Discover(context.Background())
	if err != nil {
		if cfg.DockerDiscoveryMode == config.DockerDiscoveryModeRequired {
			return buildRoutesResult{}, fmt.Errorf("docker discover failed: %w\nfix: start docker or set docker_discovery_mode: optional", err)
		}
		warnings := []string{fmt.Sprintf("docker discover failed: %v", err)}
		for _, scope := range workspaceScopes {
			h, hostErr := hostnamesForScope(scope, "")
			if hostErr != nil {
				return buildRoutesResult{}, hostErr
			}
			entries = append(entries, routes.Entry{Hostname: h.WorkspaceFQDN, IP: scope.ip, Source: routes.SourceRuntime})
			entries = append(entries, routes.Entry{Hostname: h.ProjectFQDN, IP: scope.ip, Source: routes.SourceRuntime})
		}
		merged := routes.Merge(entries...)
		warnings = append(warnings, routingWarnings...)
		warnings = append(warnings, merged.Warnings...)
		final := make([]dnsserver.Record, 0, len(merged.Entries))
		for _, entry := range merged.Entries {
			final = append(final, dnsserver.Record{Hostname: entry.Hostname, IP: entry.IP})
		}
		return buildRoutesResult{
			Records:              final,
			HTTPRoutes:           httpRoutes,
			TCPRoutes:            tcpRoutes,
			ManagedDomains:       managedDomainsFromSet(managedDomainSet, cfg.Domain),
			WorkspaceDomains:     workspaceDomainMap,
			Warnings:             warnings,
			WorkspaceConfigFiles: workspaceConfigFiles(workspaceScopes),
		}, nil
	}
	warnings := append([]string{}, diag.Warnings...)
	for _, e := range diag.Errors {
		warnings = append(warnings, "docker label validation: "+e)
	}
	for _, route := range serviceRoutes {
		_, scopeErr := upsertScope(route.Project, route.Workspace, route.WorkspacePath, netip.Addr{})
		if scopeErr != nil {
			warnings = append(warnings, fmt.Sprintf("workspace scope allocation failed for container %s: %v", route.ContainerName, scopeErr))
		}
	}

	projectWorkspaces := map[string][]string{}
	for _, scope := range workspaceScopes {
		h, hostErr := hostnamesForScope(scope, "")
		if hostErr != nil {
			return buildRoutesResult{}, hostErr
		}
		entries = append(entries, routes.Entry{Hostname: h.WorkspaceFQDN, IP: scope.ip, Source: routes.SourceRuntime})
		projectWorkspaces[scope.project] = append(projectWorkspaces[scope.project], scope.workspace)
	}

	activeLeases, err := runtimeProvider.Active()
	if err != nil {
		return buildRoutesResult{}, fmt.Errorf("read runtime leases: %w", err)
	}
	for _, lease := range activeLeases {
		if d := runtimeLeaseDomain(lease); d != "" {
			managedDomainSet[d] = struct{}{}
		}
		for _, h := range lease.Hostnames {
			entries = append(entries, routes.Entry{Hostname: h, IP: workspaceIP, Source: routes.SourceRuntime})
			if lease.HTTPPort > 0 {
				httpRoutes = append(httpRoutes, httprouter.Route{Hostname: h, Target: fmt.Sprintf("http://localhost:%d", lease.HTTPPort)})
			}
		}
	}

	workspaceHTTPCounts := map[string]int{}
	workspaceDefaultCounts := map[string]int{}
	workspaceDefaultTargets := map[string]string{}
	workspaceFirstTargets := map[string]string{}

	for _, route := range serviceRoutes {
		scope, scopeErr := upsertScope(route.Project, route.Workspace, route.WorkspacePath, netip.Addr{})
		if scopeErr != nil {
			continue
		}
		scopeID := workspaceKey(scope.project, scope.workspace)
		target := ""
		if route.HTTPPort > 0 {
			var avoidedTarget string
			var selectedAlternate bool
			target, avoidedTarget, selectedAlternate = pickServiceRouteTarget(route, in.AvoidHTTPTarget, in.Now)
			if selectedAlternate && avoidedTarget != "" {
				warnings = append(warnings, fmt.Sprintf("quarantined unreachable upstream target %s for service %s (container=%s id=%s network=%s/%s); selected alternate %s", avoidedTarget, route.ServiceName, route.ContainerName, route.ContainerID, route.NetworkName, route.NetworkID, target))
			}
		}
		portSeen := map[int]struct{}{}
		for _, port := range route.TCPPorts {
			if port == cfg.HTTPPort {
				continue
			}
			if _, ok := portSeen[port]; ok {
				continue
			}
			portSeen[port] = struct{}{}
			tcpRoutes = append(tcpRoutes, tcpproxy.Route{
				ListenIP:   scope.ip.String(),
				ListenPort: port,
				TargetIP:   route.ContainerIP,
				TargetPort: port,
			})
		}
		if route.HTTPEnabled && route.HTTPPort > 0 {
			workspaceHTTPCounts[scopeID]++
			if workspaceFirstTargets[scopeID] == "" {
				workspaceFirstTargets[scopeID] = target
			}
		}
		for _, label := range route.HostLabels {
			serviceHosts, err := hostnamesForScope(scope, label)
			if err != nil {
				return buildRoutesResult{}, err
			}
			entries = append(entries, routes.Entry{Hostname: serviceHosts.ServiceFQDN, IP: scope.ip, Source: routes.SourceDocker})
			if route.HTTPEnabled && route.HTTPPort > 0 {
				httpRoutes = append(httpRoutes, httprouter.Route{Hostname: serviceHosts.ServiceFQDN, Target: target})
			}
		}
		if route.DefaultHTTP {
			workspaceDefaultCounts[scopeID]++
			if workspaceDefaultTargets[scopeID] == "" {
				workspaceDefaultTargets[scopeID] = target
			}
		}
	}

	for key, count := range workspaceHTTPCounts {
		defaults := workspaceDefaultCounts[key]
		if defaults == 0 && count == 1 {
			workspaceDefaultTargets[key] = workspaceFirstTargets[key]
		}
		if defaults == 0 && count > 1 {
			warnings = append(warnings, fmt.Sprintf("multiple HTTP services found but no default in workspace %s; skipping workspace HTTP default route", key))
		}
		if defaults > 1 {
			warnings = append(warnings, fmt.Sprintf("multiple services declare dnsvard.default_http=true in workspace %s; using first discovered default", key))
		}
	}

	for key, target := range workspaceDefaultTargets {
		scope, ok := workspaceScopes[key]
		if !ok || target == "" {
			continue
		}
		h, hostErr := hostnamesForScope(scope, "")
		if hostErr != nil {
			return buildRoutesResult{}, hostErr
		}
		httpRoutes = append(httpRoutes, httprouter.Route{Hostname: h.WorkspaceFQDN, Target: target})
	}

	for projectLabel, workspaces := range projectWorkspaces {
		defaultWorkspace := identity.ResolveDefaultWorkspace(workspaces)
		if defaultWorkspace == "" {
			continue
		}
		scopeID := workspaceKey(projectLabel, defaultWorkspace)
		scope, ok := workspaceScopes[scopeID]
		if !ok {
			continue
		}
		h, hostErr := hostnamesForScope(scope, "")
		if hostErr != nil {
			return buildRoutesResult{}, hostErr
		}
		entries = append(entries, routes.Entry{Hostname: h.ProjectFQDN, IP: scope.ip, Source: routes.SourceRuntime})
		if target := workspaceDefaultTargets[scopeID]; target != "" {
			httpRoutes = append(httpRoutes, httprouter.Route{Hostname: h.ProjectFQDN, Target: target})
		}
	}

	merged := routes.Merge(entries...)
	warnings = append(warnings, routingWarnings...)
	warnings = append(warnings, merged.Warnings...)

	final := make([]dnsserver.Record, 0, len(merged.Entries))
	for _, entry := range merged.Entries {
		final = append(final, dnsserver.Record{Hostname: entry.Hostname, IP: entry.IP})
	}

	return buildRoutesResult{
		Records:              final,
		HTTPRoutes:           httpRoutes,
		TCPRoutes:            tcpRoutes,
		ManagedDomains:       managedDomainsFromSet(managedDomainSet, cfg.Domain),
		WorkspaceDomains:     workspaceDomainMap,
		Warnings:             warnings,
		WorkspaceConfigFiles: workspaceConfigFiles(workspaceScopes),
	}, nil
}

func pickServiceRouteTarget(route docker.ServiceRoute, avoid map[string]time.Time, now time.Time) (string, string, bool) {
	if route.HTTPPort <= 0 {
		return "", "", false
	}
	if now.IsZero() {
		now = time.Now()
	}
	candidates := make([]string, 0, len(route.CandidateIPs)+1)
	if strings.TrimSpace(route.ContainerIP) != "" {
		candidates = append(candidates, strings.TrimSpace(route.ContainerIP))
	}
	for _, ip := range route.CandidateIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		duplicate := false
		for _, existing := range candidates {
			if existing == ip {
				duplicate = true
				break
			}
		}
		if !duplicate {
			candidates = append(candidates, ip)
		}
	}
	if len(candidates) == 0 {
		return "", "", false
	}
	firstAvoided := ""
	for _, ip := range candidates {
		target := fmt.Sprintf("http://%s:%d", ip, route.HTTPPort)
		if isTargetAvoided(target, avoid, now) {
			if firstAvoided == "" {
				firstAvoided = target
			}
			continue
		}
		return target, firstAvoided, firstAvoided != ""
	}
	return fmt.Sprintf("http://%s:%d", candidates[0], route.HTTPPort), "", false
}

func isTargetAvoided(target string, avoid map[string]time.Time, now time.Time) bool {
	if len(avoid) == 0 {
		return false
	}
	expiresAt, ok := avoid[strings.TrimSpace(target)]
	if !ok {
		return false
	}
	return now.Before(expiresAt)
}

func managedDomainsFromSet(domains map[string]struct{}, fallback string) []string {
	out := make([]string, 0, len(domains)+1)
	for domain := range domains {
		d := strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
		if d != "" {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		d := strings.ToLower(strings.Trim(strings.TrimSpace(fallback), "."))
		if d != "" {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

func runtimeLeaseDomain(lease runtimeprovider.Lease) string {
	if d := strings.ToLower(strings.Trim(strings.TrimSpace(lease.Domain), ".")); d != "" {
		return d
	}
	return inferDomainFromHostnames(lease.Hostnames)
}

func inferDomainFromHostnames(hostnames []string) string {
	best := ""
	bestParts := 0
	for _, host := range hostnames {
		host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
		if host == "" {
			continue
		}
		parts := strings.Split(host, ".")
		if len(parts) < 2 {
			continue
		}
		candidate := strings.Join(parts[1:], ".")
		candidateParts := len(parts) - 1
		if candidate == "" {
			continue
		}
		if best == "" || candidateParts < bestParts || (candidateParts == bestParts && len(candidate) < len(best)) {
			best = candidate
			bestParts = candidateParts
		}
	}
	return best
}

func logDiscoveryWarnings(logger *logx.Logger, warnings []string, prior map[string]struct{}) {
	current := map[string]struct{}{}
	for _, warning := range warnings {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		current[warning] = struct{}{}
		if _, seen := prior[warning]; seen {
			continue
		}
		logger.Warn("docker discovery warning", "warning", warning)
	}
	for k := range prior {
		if _, ok := current[k]; ok {
			continue
		}
		delete(prior, k)
	}
	for k := range current {
		prior[k] = struct{}{}
	}
}

func equalStringSet(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	am := map[string]struct{}{}
	for _, v := range a {
		am[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	for _, v := range b {
		n := strings.ToLower(strings.TrimSpace(v))
		if _, ok := am[n]; !ok {
			return false
		}
	}
	return true
}

func effectiveRoutingForScope(
	cfg config.Config,
	scopePath string,
	loadScopeConfig func(string) (config.Config, error),
	pushWarning func(string),
) (string, string) {
	domain := cfg.Domain
	pattern := cfg.HostPattern

	scopePath = strings.TrimSpace(scopePath)
	if scopePath == "" {
		return domain, pattern
	}

	scopeCfg, err := loadScopeConfig(scopePath)
	if err != nil {
		pushWarning(fmt.Sprintf("workspace config load failed for %s: %v", scopePath, err))
		return domain, pattern
	}

	scopeDomain := strings.ToLower(strings.Trim(strings.TrimSpace(scopeCfg.Domain), "."))
	if scopeDomain != "" {
		domain = scopeDomain
	}
	scopePattern := strings.ToLower(strings.TrimSpace(scopeCfg.HostPattern))
	if scopePattern != "" {
		pattern = scopePattern
	}

	return domain, pattern
}

func workspaceConfigFiles(scopes map[string]workspaceScope) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		path := strings.TrimSpace(scope.path)
		if path == "" {
			continue
		}
		cfgPath := filepath.Join(path, config.DefaultLocalConfigName)
		if _, ok := seen[cfgPath]; ok {
			continue
		}
		seen[cfgPath] = struct{}{}
		out = append(out, cfgPath)
	}
	return out
}
