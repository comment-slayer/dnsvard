package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/comment-slayer/dnsvard/internal/allocator"
	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/docker"
	"github.com/comment-slayer/dnsvard/internal/identity"
	"github.com/comment-slayer/dnsvard/internal/logx"
	"github.com/comment-slayer/dnsvard/internal/platform"
	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
)

type doctorRunOptions struct {
	Cfg               config.Config
	Platform          platform.Controller
	FlushCache        bool
	CheckLocalNetwork bool
	ProbeRouting      bool
	JSONOutput        bool
}

func runDoctor(options doctorRunOptions) error {
	cfg := options.Cfg
	plat := options.Platform
	flushCache := options.FlushCache
	checkLocalNetwork := options.CheckLocalNetwork
	probeRouting := options.ProbeRouting
	jsonOutput := options.JSONOutput

	resolverOK, resolverMsg := "unknown", "not checked"
	missingDomains := []string{}
	for _, domain := range cfg.Domains {
		spec, specErr := resolverSpecFromListen(cfg, domain)
		if specErr != nil {
			return specErr
		}
		ok, matchErr := plat.ResolverMatches(spec)
		if matchErr != nil {
			resolverOK = "error"
			resolverMsg = matchErr.Error()
			break
		}
		if !ok {
			missingDomains = append(missingDomains, domain)
		}
	}
	if resolverOK != "error" {
		if len(missingDomains) == 0 {
			resolverOK = "ok"
			resolverMsg = "all managed resolver files match expected config"
		} else {
			resolverOK = "missing_or_conflict"
			resolverMsg = fmt.Sprintf("resolver not ready for suffixes %s; auto-heal should reconcile shortly (if not, run `sudo dnsvard bootstrap -f`)", strings.Join(missingDomains, ","))
		}
	}

	if flushCache {
		if err := plat.FlushDNSCache(); err != nil {
			return err
		}
	}

	localNetwork := localNetworkStatus("not_applicable")
	localNetworkDetail := "not applicable on this platform"
	if plat.SupportsLocalNetworkProbe() {
		localNetwork = localNetworkStatus("not_checked")
		localNetworkDetail = "run `dnsvard doctor --check-local-network` to probe permission"
		if checkLocalNetwork {
			localNetwork, localNetworkDetail = probeLocalNetworkAccess(250 * time.Millisecond)
		}
	}

	workspace, err := identity.DeriveWorkspaceLabel(identity.WorkspaceInput{
		WorktreeBase: cfg.Workspace.WorktreeBase,
		Branch:       cfg.Workspace.Branch,
		CwdBase:      cfg.Workspace.CwdBase,
	})
	if err != nil {
		return err
	}
	project, err := identity.DeriveProjectLabel(identity.ProjectInput{
		RemoteURL: cfg.Project.RemoteURL,
		RepoBase:  cfg.Project.RepoBase,
	})
	if err != nil {
		return err
	}
	wsID := identity.WorkspaceID(cfg.Workspace.Path)

	alloc, err := allocator.New(cfg.LoopbackCIDR, filepath.Join(cfg.StateDir, "allocator-state.json"))
	if err != nil {
		return err
	}
	ip, err := alloc.Allocate(wsID)
	if err != nil {
		return err
	}

	h, err := identity.Hostnames(identity.HostnameInput{
		Domain:    cfg.Domain,
		Project:   project,
		Workspace: workspace,
		Pattern:   cfg.HostPattern,
	})
	if err != nil {
		return err
	}

	managedResolvers := []string{}
	if managed, listErr := plat.ListManagedResolvers(); listErr == nil {
		managedResolvers = managed
	}

	daemonPID := 0
	daemonRunning := false
	if pid, pidErr := daemon.ReadPID(cfg.StateDir); pidErr == nil {
		daemonPID = pid
		daemonRunning = daemon.ProcessRunning(pid)
	}

	var recState daemon.ReconcileState
	reconcileFound := false
	if state, recErr := daemon.ReadReconcileState(cfg.StateDir); recErr == nil {
		recState = state
		reconcileFound = true
	}

	rp := runtimeprovider.New(cfg.StateDir)
	leases, leasesErr := rp.Active()

	var probe *doctorRoutingProbe
	if probeRouting {
		p, probeErr := collectDoctorRoutingProbe(cfg, project, workspace, ip)
		if probeErr != nil {
			p = &doctorRoutingProbe{Error: probeErr.Error()}
		}
		probe = p
	}

	if jsonOutput {
		payload := doctorJSON{
			Status:   "ok",
			Platform: plat.Name(),
			Global: doctorGlobalJSON{
				ManagedSuffixes: cfg.Domains,
			},
			Workspace: doctorWorkspaceJSON{
				Suffix:        cfg.Domain,
				HostPattern:   cfg.HostPattern,
				Project:       project,
				Workspace:     workspace,
				WorkspaceID:   wsID,
				WorkspaceIP:   ip.String(),
				WorkspaceHost: h.WorkspaceFQDN,
				ProjectHost:   h.ProjectFQDN,
			},
			PlatformDetails: doctorPlatformJSON{
				ResolverStatus:      resolverOK,
				ResolverMessage:     resolverMsg,
				LocalNetworkAccess:  string(localNetwork),
				LocalNetworkMessage: localNetworkDetail,
				ManagedResolvers:    managedResolvers,
			},
			Runtime: doctorRuntimeJSON{
				DaemonPID:      daemonPID,
				DaemonRunning:  daemonRunning,
				ReconcileFound: reconcileFound,
				Reconcile:      recState,
				RuntimeLeases:  leases,
				RuntimeLeasesError: func() string {
					if leasesErr != nil {
						return leasesErr.Error()
					}
					return ""
				}(),
			},
			RoutingProbe: probe,
		}
		if reconcileFound {
			for _, problem := range selfHealProblemsFromState(recState, cfg, plat) {
				payload.Problems = append(payload.Problems, doctorProblemFromSelfHeal(problem))
			}
		}
		encoded, marshalErr := json.MarshalIndent(payload, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Printf("%s\n", string(encoded))
		return nil
	}

	fmt.Printf("doctor ok\n")
	for _, line := range plat.Diagnostics() {
		fmt.Printf("- %s\n", line)
	}
	showAdvice := resolverOK != "ok"
	if showAdvice {
		for _, line := range plat.DoctorHints() {
			fmt.Printf("- hint: %s\n", line)
		}
		if v := strings.TrimSpace(plat.DoctorRecommendation()); v != "" {
			fmt.Printf("- hint: %s\n", v)
		}
	}
	fmt.Printf("[global]\n")
	fmt.Printf("- managed_suffixes: %s\n", strings.Join(cfg.Domains, ","))

	fmt.Printf("[workspace]\n")
	fmt.Printf("- suffix: %s\n", cfg.Domain)
	fmt.Printf("- host_pattern: %s\n", cfg.HostPattern)
	fmt.Printf("- project: %s\n", project)
	fmt.Printf("- workspace: %s\n", workspace)
	fmt.Printf("- workspace_id: %s\n", wsID)
	fmt.Printf("- workspace_ip: %s\n", ip)
	fmt.Printf("- workspace_host: %s\n", h.WorkspaceFQDN)
	fmt.Printf("- project_host: %s\n", h.ProjectFQDN)

	fmt.Printf("[platform]\n")
	fmt.Printf("- resolver_status: %s (%s)\n", resolverOK, resolverMsg)
	fmt.Printf("- local_network_access: %s (%s)\n", localNetwork, localNetworkDetail)
	if localNetwork == localNetworkDenied {
		if advice := strings.TrimSpace(plat.LocalNetworkDeniedAdvice()); advice != "" {
			fmt.Printf("- local_network_fix: %s\n", advice)
		}
	} else if localNetwork == localNetworkUnknown {
		if advice := strings.TrimSpace(plat.LocalNetworkUnknownAdvice(cfg.Domain)); advice != "" {
			fmt.Printf("- local_network_note: %s\n", advice)
		}
	}
	fmt.Printf("- managed_resolvers: %s\n", strings.Join(managedResolvers, ","))

	fmt.Printf("[runtime]\n")
	if daemonPID > 0 {
		fmt.Printf("- daemon_pid: %d (running=%t)\n", daemonPID, daemonRunning)
	} else {
		fmt.Printf("- daemon_pid: not found\n")
	}
	if reconcileFound {
		fmt.Printf("- reconcile: sequence=%d cause=%s result=%s updated_at=%s\n", recState.Sequence, recState.Cause, recState.Result, recState.UpdatedAt.Format(time.RFC3339))
		fmt.Printf("- docker_watch: running=%t restarts=%d last_start=%s last_event=%s\n", recState.DockerWatchRunning, recState.DockerWatchRestartCount, recState.DockerWatchLastStartAt.Format(time.RFC3339), recState.DockerWatchLastEventAt.Format(time.RFC3339))
		if recState.DockerWatchLastError != "" {
			fmt.Printf("- docker_watch_error: %s\n", recState.DockerWatchLastError)
		}
		if recState.SelfHealTrigger != "" || recState.SelfHealAction != "" || recState.SelfHealDetail != "" {
			fmt.Printf("- self_heal: trigger=%s action=%s detail=%s\n", recState.SelfHealTrigger, recState.SelfHealAction, recState.SelfHealDetail)
			if recState.SelfHealDetector != "" || recState.SelfHealComponent != "" || recState.SelfHealActionID != "" {
				fmt.Printf("- self_heal_meta: detector=%s component=%s action_id=%s\n", recState.SelfHealDetector, recState.SelfHealComponent, recState.SelfHealActionID)
			}
		}
		for _, problem := range selfHealProblemsFromState(recState, cfg, plat) {
			for _, line := range selfHealProblemLines(problem) {
				fmt.Printf("%s\n", line)
			}
		}
	} else {
		fmt.Printf("- reconcile: not found\n")
	}

	if leasesErr != nil {
		fmt.Printf("- runtime_leases: error (%v)\n", leasesErr)
	} else {
		fmt.Printf("- runtime_leases: %d\n", len(leases))
		for _, lease := range leases {
			fmt.Printf("  - %s pid=%d http_port=%d hosts=%s\n", lease.ID, lease.PID, lease.HTTPPort, strings.Join(lease.Hostnames, ","))
		}
	}

	if probeRouting {
		fmt.Printf("[routing_probe]\n")
		if err := doctorProbeRouting(cfg, project, workspace, ip); err != nil {
			fmt.Printf("- routing_probe: error (%v)\n", err)
		}
	}

	return nil
}

type doctorProblem struct {
	Code         string   `json:"code"`
	ActionID     string   `json:"action_id,omitempty"`
	Component    string   `json:"component"`
	Message      string   `json:"message"`
	Fixes        []string `json:"fixes"`
	FailureCount int      `json:"failure_count,omitempty"`
	BlockedUntil string   `json:"blocked_until,omitempty"`
}

func doctorProblemFromSelfHeal(problem selfHealProblem) doctorProblem {
	blockedUntil := ""
	if !problem.BlockedUntil.IsZero() {
		blockedUntil = problem.BlockedUntil.Format(time.RFC3339)
	}
	return doctorProblem{
		Code:         problem.Code,
		ActionID:     problem.ActionID,
		Component:    problem.Component,
		Message:      problem.Message,
		Fixes:        problem.Fixes,
		FailureCount: problem.FailureCount,
		BlockedUntil: blockedUntil,
	}
}

type doctorGlobalJSON struct {
	ManagedSuffixes []string `json:"managed_suffixes"`
}

type doctorWorkspaceJSON struct {
	Suffix        string `json:"suffix"`
	HostPattern   string `json:"host_pattern"`
	Project       string `json:"project"`
	Workspace     string `json:"workspace"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceIP   string `json:"workspace_ip"`
	WorkspaceHost string `json:"workspace_host"`
	ProjectHost   string `json:"project_host"`
}

type doctorPlatformJSON struct {
	ResolverStatus      string   `json:"resolver_status"`
	ResolverMessage     string   `json:"resolver_message"`
	LocalNetworkAccess  string   `json:"local_network_access"`
	LocalNetworkMessage string   `json:"local_network_message"`
	ManagedResolvers    []string `json:"managed_resolvers"`
}

type doctorRuntimeJSON struct {
	DaemonPID          int                     `json:"daemon_pid"`
	DaemonRunning      bool                    `json:"daemon_running"`
	ReconcileFound     bool                    `json:"reconcile_found"`
	Reconcile          daemon.ReconcileState   `json:"reconcile"`
	RuntimeLeases      []runtimeprovider.Lease `json:"runtime_leases"`
	RuntimeLeasesError string                  `json:"runtime_leases_error,omitempty"`
}

type doctorRoutingWarning struct {
	Message string   `json:"message"`
	Code    string   `json:"code"`
	Fixes   []string `json:"fixes"`
}

type doctorRoutingHost struct {
	Host   string `json:"host"`
	Source string `json:"source"`
	DNS    string `json:"dns"`
	HTTP   string `json:"http"`
}

type doctorRoutingProbe struct {
	Warnings []doctorRoutingWarning `json:"warnings,omitempty"`
	Hosts    []doctorRoutingHost    `json:"hosts,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

type doctorJSON struct {
	Status          string              `json:"status"`
	Platform        string              `json:"platform"`
	Global          doctorGlobalJSON    `json:"global"`
	Workspace       doctorWorkspaceJSON `json:"workspace"`
	PlatformDetails doctorPlatformJSON  `json:"platform_details"`
	Runtime         doctorRuntimeJSON   `json:"runtime"`
	RoutingProbe    *doctorRoutingProbe `json:"routing_probe,omitempty"`
	Problems        []doctorProblem     `json:"problems,omitempty"`
}

func doctorProbeRouting(cfg config.Config, project string, workspace string, ip netip.Addr) error {
	probe, err := collectDoctorRoutingProbe(cfg, project, workspace, ip)
	if err != nil {
		return err
	}
	if len(probe.Warnings) > 0 {
		fmt.Printf("- routing_probe_warnings: %d\n", len(probe.Warnings))
		for _, warning := range probe.Warnings {
			fmt.Printf("- routing_probe_warning: %s\n", warning.Message)
			fmt.Printf("- routing_probe_warning_code: %s\n", warning.Code)
			for _, hint := range warning.Fixes {
				fmt.Printf("- routing_probe_fix: %s\n", hint)
			}
		}
	}
	for _, host := range probe.Hosts {
		fmt.Printf("- routing_probe: host=%s source=%s dns=%s http=%s\n", host.Host, host.Source, host.DNS, host.HTTP)
	}
	return nil
}

func collectDoctorRoutingProbe(cfg config.Config, project string, workspace string, ip netip.Addr) (*doctorRoutingProbe, error) {
	alloc, err := allocator.New(cfg.LoopbackCIDR, filepath.Join(cfg.StateDir, "allocator-state.json"))
	if err != nil {
		return nil, err
	}
	provider := docker.New()
	runtimeProvider := runtimeprovider.New(cfg.StateDir)
	routesResult, err := buildRoutes(buildRoutesInput{
		Cfg:             cfg,
		Project:         project,
		Workspace:       workspace,
		WorkspaceIP:     ip,
		Allocator:       alloc,
		Provider:        provider,
		RuntimeProvider: runtimeProvider,
	})
	if err != nil {
		return nil, err
	}
	httpRoutes := routesResult.HTTPRoutes
	warnings := routesResult.Warnings
	out := &doctorRoutingProbe{}
	for _, warning := range warnings {
		code, fixes := routingProbeWarningResolution(cfg, warning)
		out.Warnings = append(out.Warnings, doctorRoutingWarning{Message: warning, Code: code, Fixes: fixes})
	}

	h, err := identity.Hostnames(identity.HostnameInput{Domain: cfg.Domain, Project: project, Workspace: workspace, Pattern: cfg.HostPattern})
	if err != nil {
		return nil, err
	}
	routeSource := "global_default"
	if _, loadErr := config.Load(config.LoadOptions{CWD: cfg.Workspace.Path}); loadErr == nil {
		routeSource = "workspace_config"
	}
	hosts := []string{h.WorkspaceFQDN, h.ProjectFQDN}
	httpHostSet := map[string]struct{}{}
	for _, route := range httpRoutes {
		httpHostSet[strings.ToLower(strings.TrimSpace(route.Hostname))] = struct{}{}
	}
	for _, host := range hosts {
		dnsStatus := "missing"
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		addrs, lookupErr := net.DefaultResolver.LookupHost(ctx, host)
		cancel()
		if lookupErr == nil && len(addrs) > 0 {
			dnsStatus = "ok"
		}
		httpStatus := "missing"
		if _, ok := httpHostSet[strings.ToLower(host)]; ok {
			httpStatus = "configured"
			client := &http.Client{Timeout: 500 * time.Millisecond}
			resp, reqErr := client.Get("http://" + host)
			if reqErr == nil {
				httpStatus = fmt.Sprintf("http_%d", resp.StatusCode)
				_ = resp.Body.Close()
			}
		}
		out.Hosts = append(out.Hosts, doctorRoutingHost{Host: host, Source: routeSource, DNS: dnsStatus, HTTP: httpStatus})
	}
	return out, nil
}

func routingProbeWarningResolution(cfg config.Config, warning string) (string, []string) {
	lower := strings.ToLower(strings.TrimSpace(warning))
	code := "routing_probe_warning_unknown"
	hints := []string{}
	if strings.Contains(lower, "manual discovery") || strings.Contains(lower, "dnsvard.detect") {
		code = "routing_probe_manual_discovery"
		hints = append(hints, "set Docker labels `dnsvard.http_port` and `dnsvard.hosts` on that service, or remove `dnsvard.detect=manual`")
	}
	if strings.Contains(lower, "multiple networks") {
		code = "routing_probe_multiple_networks"
		hints = append(hints, "attach service to one primary network or ensure the target port is reachable from all attached networks")
	}
	if strings.Contains(lower, "has no network ip") {
		code = "routing_probe_missing_network_ip"
		hints = append(hints, "ensure the container is attached to a Docker network before expecting routing")
	}
	if strings.Contains(lower, "no project") || strings.Contains(lower, "workspace") {
		code = "routing_probe_label_ambiguity"
		hints = append(hints, "set `dnsvard.project` and `dnsvard.workspace` labels explicitly when auto-detection is ambiguous")
	}
	if strings.Contains(lower, "http") && strings.Contains(lower, "port") {
		code = "routing_probe_http_port"
		hints = append(hints, fmt.Sprintf("verify your app listens on configured HTTP port and that `http_port`=%d is free locally", cfg.HTTPPort))
	}
	if len(hints) == 0 {
		hints = append(hints, "inspect container labels and ports with `docker inspect <container>` to align dnsvard routing metadata")
	}
	return code, dedupeStrings(hints)
}

func runEnv(_ context.Context, _ *logx.Logger, cfg config.Config, shell bool) error {
	return runEnvTo(os.Stdout, cfg, shell)
}

func runEnvTo(out io.Writer, cfg config.Config, shell bool) error {
	workspace, err := identity.DeriveWorkspaceLabel(identity.WorkspaceInput{
		WorktreeBase: cfg.Workspace.WorktreeBase,
		Branch:       cfg.Workspace.Branch,
		CwdBase:      cfg.Workspace.CwdBase,
	})
	if err != nil {
		return err
	}
	project, err := identity.DeriveProjectLabel(identity.ProjectInput{
		RemoteURL: cfg.Project.RemoteURL,
		RepoBase:  cfg.Project.RepoBase,
	})
	if err != nil {
		return err
	}
	hosts, err := identity.Hostnames(identity.HostnameInput{
		Domain:    cfg.Domain,
		Project:   project,
		Workspace: workspace,
		Pattern:   cfg.HostPattern,
	})
	if err != nil {
		return err
	}
	alloc, err := allocator.New(cfg.LoopbackCIDR, filepath.Join(cfg.StateDir, "allocator-state.json"))
	if err != nil {
		return err
	}
	ip, err := alloc.Allocate(identity.WorkspaceID(cfg.Workspace.Path))
	if err != nil {
		return err
	}

	if shell {
		fmt.Fprintf(out, "export DNSVARD_SUFFIX=%s\n", cfg.Domain)
		fmt.Fprintf(out, "export DNSVARD_WORKSPACE=%s\n", workspace)
		fmt.Fprintf(out, "export DNSVARD_HOST_IP=%s\n", ip)
		fmt.Fprintf(out, "export DNSVARD_WORKSPACE_HOST=%s\n", hosts.WorkspaceFQDN)
		fmt.Fprintf(out, "export DNSVARD_PROJECT_HOST=%s\n", hosts.ProjectFQDN)
		return nil
	}

	fmt.Fprintf(out, "DNSVARD_SUFFIX=%s\n", cfg.Domain)
	fmt.Fprintf(out, "DNSVARD_WORKSPACE=%s\n", workspace)
	fmt.Fprintf(out, "DNSVARD_HOST_IP=%s\n", ip)
	fmt.Fprintf(out, "DNSVARD_WORKSPACE_HOST=%s\n", hosts.WorkspaceFQDN)
	fmt.Fprintf(out, "DNSVARD_PROJECT_HOST=%s\n", hosts.ProjectFQDN)
	return nil
}
