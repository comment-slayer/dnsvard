package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/daemon"
	"github.com/comment-slayer/dnsvard/internal/identity"
	"github.com/comment-slayer/dnsvard/internal/runtimeprovider"
)

func newPSCommand(ctx context.Context, configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ps [target]",
		Aliases: []string{"ls"},
		Short:   "List dnsvard-managed runtime and docker workloads",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := parsePSFilter(args)
			if err != nil {
				return err
			}
			cfg, _, _, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			cfg = managedStateDisplayConfig(cfg)
			workspaceDomains, runtimeAuthoritative := managedStateWorkspaceDomains(cfg)
			state, warn, err := collectManagedState(ctx, cfg)
			if err != nil {
				return err
			}
			state, err = filterManagedState(state, target)
			if err != nil {
				return err
			}
			if warn != "" {
				fmt.Printf("warning: %s\n", warn)
			}
			printManagedStateWithRuntimeDomainsAndOptions(cfg, state, workspaceDomains, runtimeAuthoritative, psFilterDisplayOptions(target, state))
			return nil
		},
	}
	cmd.ValidArgsFunction = completeManagedTargets(configPath, true, false)
	return cmd
}

func parsePSFilter(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	v := strings.TrimSpace(args[0])
	if v == "" {
		return "", usageError("ps target must be non-empty")
	}
	return v, nil
}

func filterManagedState(state managedState, target string) (managedState, error) {
	target = normalizePSFilterTarget(strings.TrimSpace(target))
	if target == "" || target == "all" {
		return state, nil
	}

	if strings.HasPrefix(target, "lease/") {
		needle := strings.TrimSpace(strings.TrimPrefix(target, "lease/"))
		if needle == "" {
			needle = ""
		}
		matched := filterLeasesByPattern(state.Leases, needle)
		if len(matched) == 0 {
			return managedState{}, fmt.Errorf("lease %q not found", needle)
		}
		return managedState{Leases: matched}, nil
	}

	containers, err := filterContainersForPS(state.Containers, target)
	if err != nil {
		return managedState{}, err
	}
	return managedState{Leases: state.Leases, Containers: containers}, nil
}

func filterLeasesByPattern(leases []runtimeprovider.Lease, pattern string) []runtimeprovider.Lease {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return append([]runtimeprovider.Lease{}, leases...)
	}
	out := make([]runtimeprovider.Lease, 0, len(leases))
	for _, lease := range leases {
		if matchTargetValue(strings.ToLower(strings.TrimSpace(lease.ID)), pattern) {
			out = append(out, lease)
			continue
		}
		for _, host := range lease.Hostnames {
			if matchTargetValue(strings.ToLower(strings.TrimSpace(host)), pattern) {
				out = append(out, lease)
				break
			}
		}
	}
	return out
}

func filterContainersForPS(containers []dockerContainer, target string) ([]dockerContainer, error) {
	target = normalizePSFilterTarget(strings.TrimSpace(target))
	if target == "" || target == "all" {
		return append([]dockerContainer{}, containers...), nil
	}

	addUnique := func(dst []dockerContainer, c dockerContainer) []dockerContainer {
		for _, existing := range dst {
			if existing.ID == c.ID {
				return dst
			}
		}
		return append(dst, c)
	}

	out := []dockerContainer{}
	if strings.HasPrefix(target, "project/") {
		projectPattern := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(target, "project/")))
		for _, c := range containers {
			project := strings.ToLower(identity.NormalizeLabel(c.Project))
			if projectPattern == "" || matchTargetValue(project, projectPattern) {
				out = addUnique(out, c)
			}
		}
		if len(out) == 0 {
			if projectPattern == "" {
				return out, nil
			}
			return nil, fmt.Errorf("project %q not found", strings.TrimPrefix(target, "project/"))
		}
		return out, nil
	}

	if strings.HasPrefix(target, "workspace/") {
		raw := strings.TrimSpace(strings.TrimPrefix(target, "workspace/"))
		workspacePattern := raw
		projectPattern := ""
		if at := strings.Index(raw, "@"); at >= 0 {
			workspacePattern = strings.TrimSpace(raw[:at])
			projectPattern = strings.TrimSpace(raw[at+1:])
		}
		workspacePattern = strings.ToLower(workspacePattern)
		projectPattern = strings.ToLower(projectPattern)
		for _, c := range containers {
			workspace := strings.ToLower(identity.NormalizeLabel(c.Workspace))
			project := strings.ToLower(identity.NormalizeLabel(c.Project))
			if workspacePattern != "" && !matchTargetValue(workspace, workspacePattern) {
				continue
			}
			if projectPattern != "" && !matchTargetValue(project, projectPattern) {
				continue
			}
			out = addUnique(out, c)
		}
		if len(out) == 0 {
			if workspacePattern == "" && projectPattern == "" {
				return out, nil
			}
			return nil, fmt.Errorf("workspace %q not found", raw)
		}
		return out, nil
	}

	if strings.HasPrefix(target, "container/") {
		needle := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(target, "container/")))
		for _, c := range containers {
			name := strings.ToLower(strings.TrimSpace(c.Name))
			id := strings.ToLower(strings.TrimSpace(c.ID))
			if needle == "" || matchTargetValue(name, needle) || matchTargetValue(id, needle) {
				out = addUnique(out, c)
			}
		}
		if len(out) == 0 {
			if needle == "" {
				return out, nil
			}
			return nil, fmt.Errorf("container %q not found", strings.TrimPrefix(target, "container/"))
		}
		return out, nil
	}

	needle := strings.ToLower(target)
	for _, c := range containers {
		name := strings.ToLower(strings.TrimSpace(c.Name))
		id := strings.ToLower(strings.TrimSpace(c.ID))
		if matchTargetValue(name, needle) || matchTargetValue(id, needle) {
			out = addUnique(out, c)
		}
	}
	if len(out) > 0 {
		return out, nil
	}

	return nil, fmt.Errorf("unknown target %q\nuse `dnsvard ps` to list managed targets", target)
}

func normalizePSFilterTarget(target string) string {
	target = strings.TrimSpace(target)
	switch target {
	case "project":
		return "project/"
	case "workspace":
		return "workspace/"
	case "container":
		return "container/"
	case "lease":
		return "lease/"
	default:
		return target
	}
}

func matchTargetValue(value string, pattern string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if value == "" || pattern == "" {
		return false
	}
	if value == pattern || strings.HasPrefix(value, pattern) {
		return true
	}
	return false
}

func managedStateDisplayConfig(cfg config.Config) config.Config {
	pid, err := daemon.ReadPID(cfg.StateDir)
	if err != nil || pid <= 0 || !daemon.ProcessRunning(pid) {
		return cfg
	}
	state, err := daemon.ReadReconcileState(cfg.StateDir)
	if err != nil {
		return cfg
	}
	if state.PID != pid {
		return cfg
	}
	if _, stale, _ := formatReconcileStateSummary(state, pid); stale {
		return cfg
	}
	domain, pattern, ok := reconcileRuntimeDisplayConfig(state)
	if !ok {
		if refreshedDomain, refreshedPattern, refreshed := refreshManagedStateRuntimeConfig(cfg.StateDir, pid); refreshed {
			domain = refreshedDomain
			pattern = refreshedPattern
		}
	}
	if domain == "" || pattern == "" {
		cfg.Domain = ""
		cfg.HostPattern = ""
		return cfg
	}
	cfg.Domain = domain
	cfg.HostPattern = pattern
	return cfg
}

func managedStateWorkspaceDomains(cfg config.Config) (map[string]string, bool) {
	pid, err := daemon.ReadPID(cfg.StateDir)
	if err != nil || pid <= 0 || !daemon.ProcessRunning(pid) {
		return nil, false
	}
	state, err := daemon.ReadReconcileState(cfg.StateDir)
	if err != nil {
		return nil, false
	}
	if state.PID != pid {
		return nil, false
	}
	if _, stale, _ := formatReconcileStateSummary(state, pid); stale {
		return nil, false
	}
	if len(state.WorkspaceDomains) == 0 {
		return nil, true
	}
	out := make(map[string]string, len(state.WorkspaceDomains))
	for key, value := range state.WorkspaceDomains {
		normalizedKey := workspaceDomainKeyFromString(key)
		normalizedValue := strings.TrimSpace(value)
		if normalizedKey == "" || normalizedValue == "" {
			continue
		}
		out[normalizedKey] = normalizedValue
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

func reconcileRuntimeDisplayConfig(state daemon.ReconcileState) (string, string, bool) {
	domain := strings.TrimSpace(state.ConfigDomain)
	pattern := strings.TrimSpace(state.ConfigHostPattern)
	if domain == "" || pattern == "" {
		return "", "", false
	}
	return domain, pattern, true
}

func refreshManagedStateRuntimeConfig(stateDir string, pid int) (string, string, bool) {
	if pid <= 0 || !daemon.ProcessRunning(pid) {
		return "", "", false
	}
	timeout := 2 * time.Second
	if pid == os.Getpid() {
		timeout = 120 * time.Millisecond
	} else {
		_ = killPID(pid, syscall.SIGHUP)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := daemon.ReadReconcileState(stateDir)
		if err == nil && state.PID == pid {
			if _, stale, _ := formatReconcileStateSummary(state, pid); !stale {
				if domain, pattern, ok := reconcileRuntimeDisplayConfig(state); ok {
					return domain, pattern, true
				}
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	return "", "", false
}

func newStopCommand(ctx context.Context, configPath *string) *cobra.Command {
	return newSignalCommand(ctx, configPath, "stop", syscall.SIGTERM)
}

func newKillCommand(ctx context.Context, configPath *string) *cobra.Command {
	return newSignalCommand(ctx, configPath, "kill", syscall.SIGKILL)
}

func newRemoveCommand(ctx context.Context, configPath *string) *cobra.Command {
	dryRun := false
	yes := false
	force := false
	cmd := &cobra.Command{
		Use:   "rm <target>",
		Short: "Remove dnsvard-managed containers",
		Long: strings.Join([]string{
			"Targets:",
			"- container/<name-or-id>",
			"- workspace/<workspace>@<project> (project optional)",
			"- project/<project>",
			"- all (requires --yes)",
		}, "\n"),
		Example: strings.Join([]string{
			"  dnsvard rm container/feat-1-api-1",
			"  dnsvard rm workspace/feat-1@project-name",
			"  dnsvard rm project/project-name",
			"  dnsvard rm all --yes",
			"  dnsvard rm all --yes --dry-run",
			"  dnsvard rm workspace/feat-1@project-name --force",
		}, "\n"),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			target := strings.TrimSpace(args[0])
			if target == "all" && !yes {
				return usageError("rm all requires --yes")
			}
			cfg, _, _, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			containers, err := discoverDockerContainers(ctx, true)
			if err != nil {
				return err
			}
			selected, err := selectContainerTarget(containers, target)
			if err != nil {
				return err
			}
			if len(selected) == 0 {
				fmt.Printf("no matching containers for %q\n", target)
				return nil
			}

			ids := make([]string, 0, len(selected))
			for _, c := range selected {
				if c.Running && !force {
					fmt.Printf("warning: skipping running container %s (use --force)\n", c.Name)
					continue
				}
				ids = append(ids, c.ID)
			}
			if len(ids) == 0 {
				fmt.Printf("nothing to remove\n")
				return nil
			}

			fmt.Printf("removing containers: %d\n", len(ids))
			if dryRun {
				for _, c := range selected {
					if c.Running && !force {
						continue
					}
					fmt.Printf("- container/%s status=%s\n", c.Name, c.Status)
				}
				return nil
			}
			if err := dockerRemoveContainers(ctx, ids, force); err != nil {
				return err
			}
			if err := waitForRemovalConvergence(ctx, ids); err != nil {
				fmt.Printf("warning: convergence incomplete: %v\n", err)
			}
			notifyDaemonReconcile(cfg.StateDir)
			fmt.Printf("done\n")
			return nil
		},
	}
	cmd.ValidArgsFunction = completeManagedTargets(configPath, false, false)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be removed without applying changes")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm destructive all-target operation")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force-remove running containers")
	return cmd
}

func newSignalCommand(ctx context.Context, configPath *string, name string, sig syscall.Signal) *cobra.Command {

	dryRun := false
	yes := false
	cmd := &cobra.Command{
		Use:   name + " <target>",
		Short: fmt.Sprintf("%s dnsvard-managed workloads", commandTitle(name)),
		Long: strings.Join([]string{
			"Targets:",
			"- lease/<id>",
			"- container/<name-or-id>",
			"- workspace/<workspace>@<project> (project optional)",
			"- project/<project>",
			"- all (requires --yes)",
		}, "\n"),
		Example: strings.Join([]string{
			fmt.Sprintf("  dnsvard %s lease/run-a1b2c3", name),
			fmt.Sprintf("  dnsvard %s container/feat-1-api-1", name),
			fmt.Sprintf("  dnsvard %s workspace/feat-1@project-name", name),
			fmt.Sprintf("  dnsvard %s project/project-name", name),
			fmt.Sprintf("  dnsvard %s all --yes", name),
			fmt.Sprintf("  dnsvard %s all --yes --dry-run", name),
		}, "\n"),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			target := strings.TrimSpace(args[0])
			if target == "all" && !yes {
				return usageError(fmt.Sprintf("%s all requires --yes", name))
			}
			cfg, _, _, err := loadRuntime(*configPath, true)
			if err != nil {
				return err
			}
			state, warn, err := collectManagedState(ctx, cfg)
			if err != nil {
				return err
			}
			if warn != "" {
				fmt.Printf("warning: %s\n", warn)
			}
			selection, err := selectManagedTarget(state, target)
			if err != nil {
				return err
			}
			if len(selection.Leases) == 0 && len(selection.ContainerIDs) == 0 {
				fmt.Printf("no matching managed targets for %q\n", target)
				return nil
			}

			verb := name + "ping"
			if name == "stop" {
				verb = "stopping"
			}
			if name == "kill" {
				verb = "killing"
			}
			fmt.Printf("%s: leases=%d containers=%d\n", verb, len(selection.Leases), len(selection.ContainerIDs))
			if dryRun {
				for _, lease := range selection.Leases {
					fmt.Printf("- lease/%s pid=%d\n", lease.ID, lease.PID)
				}
				for _, id := range selection.ContainerIDs {
					fmt.Printf("- container/%s\n", id)
				}
				return nil
			}

			rp := runtimeprovider.New(cfg.StateDir)
			for _, lease := range selection.Leases {
				if err := killProcessGroup(lease.PID, sig); err != nil {
					fmt.Printf("warning: lease/%s signal failed: %v\n", lease.ID, err)
					continue
				}
				if sig == syscall.SIGKILL {
					_ = rp.Remove(lease.ID)
				}
			}
			if len(selection.ContainerIDs) > 0 {
				if err := dockerSignalContainers(ctx, selection.ContainerIDs, sig); err != nil {
					return err
				}
			}
			if err := waitForSignalConvergence(ctx, selection.Leases, selection.ContainerIDs); err != nil {
				fmt.Printf("warning: convergence incomplete: %v\n", err)
			}
			notifyDaemonReconcile(cfg.StateDir)
			fmt.Printf("done\n")
			return nil
		},
	}
	cmd.ValidArgsFunction = completeManagedTargets(configPath, true, true)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be signaled without applying changes")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm destructive all-target operation")
	return cmd
}

func completeManagedTargets(configPath *string, includeLeases bool, runningOnly bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		candidates := []string{"all", "container/", "workspace/", "project/"}
		if includeLeases {
			candidates = append(candidates, "lease/")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		state := managedState{}
		if containers, err := discoverDockerContainers(ctx, true); err == nil {
			state.Containers = containers
		}

		if includeLeases {
			explicitPath := ""
			if configPath != nil {
				explicitPath = strings.TrimSpace(*configPath)
			}
			if cfg, err := config.Load(config.LoadOptions{CWD: mustGetwd(), ExplicitPath: explicitPath}); err == nil {
				rp := runtimeprovider.New(cfg.StateDir)
				if leases, leaseErr := rp.All(); leaseErr == nil {
					state.Leases = leases
				}
			}
		}

		candidates = append(candidates, managedTargetSuggestions(state, includeLeases, runningOnly)...)
		return filterCompletionCandidates(candidates, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func managedTargetSuggestions(state managedState, includeLeases bool, runningOnly bool) []string {
	seen := map[string]struct{}{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		seen[v] = struct{}{}
	}
	for _, c := range state.Containers {
		if runningOnly && !c.Running {
			continue
		}
		if name := strings.TrimSpace(c.Name); name != "" {
			add("container/" + name)
		}
		project := identity.NormalizeLabel(c.Project)
		workspace := identity.NormalizeLabel(c.Workspace)
		if project != "" {
			add("project/" + project)
		}
		if workspace != "" {
			add("workspace/" + workspace)
		}
		if workspace != "" && project != "" {
			add(fmt.Sprintf("workspace/%s@%s", workspace, project))
		}
	}
	if includeLeases {
		for _, lease := range state.Leases {
			if id := strings.TrimSpace(lease.ID); id != "" {
				add("lease/" + id)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func filterCompletionCandidates(candidates []string, toComplete string) []string {
	prefix := strings.TrimSpace(toComplete)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if prefix != "" && !strings.HasPrefix(c, prefix) {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

type managedState struct {
	Leases     []runtimeprovider.Lease
	Containers []dockerContainer
}

type managedSelection struct {
	Leases       []runtimeprovider.Lease
	ContainerIDs []string
}

type dockerContainer struct {
	ID        string
	Name      string
	Service   string
	Project   string
	Workspace string
	Status    string
	Running   bool
}

func processAlive(pid int) bool {
	return daemon.ProcessRunning(pid)
}

func containerMarker(c dockerContainer) string {
	if c.Running {
		return "running"
	}
	status := strings.ToLower(strings.TrimSpace(c.Status))
	if status == "exited" || status == "created" {
		return "stopped"
	}
	if status == "dead" || status == "removing" || status == "" {
		return "stale"
	}
	return "stopped"
}

func aggregateWorkspaceStatus(containers []dockerContainer) string {
	if len(containers) == 0 {
		return "stale"
	}
	hasRunning := false
	hasStale := false
	for _, c := range containers {
		m := containerMarker(c)
		if m == "running" {
			hasRunning = true
		}
		if m == "stale" {
			hasStale = true
		}
	}
	if hasRunning {
		return "running"
	}
	if hasStale {
		return "stale"
	}
	return "stopped"
}

func collectManagedState(ctx context.Context, cfg config.Config) (managedState, string, error) {
	rp := runtimeprovider.New(cfg.StateDir)
	leases, err := rp.All()
	if err != nil {
		return managedState{}, "", err
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	containers, dockerErr := discoverDockerContainers(dctx, true)
	if dockerErr != nil {
		return managedState{Leases: leases}, fmt.Sprintf("docker discovery unavailable (%v); runtime leases still shown", dockerErr), nil
	}
	return managedState{Leases: leases, Containers: containers}, "", nil
}

func printManagedState(cfg config.Config, state managedState) {
	printManagedStateWithRuntimeDomainsAndOptions(cfg, state, nil, false, managedStatePrintOptions{GroupByProject: true})
}

func printManagedStateWithRuntimeDomains(cfg config.Config, state managedState, workspaceDomains map[string]string, runtimeAuthoritative bool) {
	printManagedStateWithRuntimeDomainsAndOptions(cfg, state, workspaceDomains, runtimeAuthoritative, managedStatePrintOptions{GroupByProject: true})
}

type managedStatePrintOptions struct {
	GroupByProject                 bool
	IncludeWorkspaceContainerCount bool
}

func psFilterDisplayOptions(target string, state managedState) managedStatePrintOptions {
	target = strings.TrimSpace(target)
	if target != "" && target != "all" {
		target = normalizePSFilterTarget(target)
		if strings.HasPrefix(target, "project/") && managedProjectCount(state) > 1 {
			return managedStatePrintOptions{GroupByProject: true}
		}
		return managedStatePrintOptions{GroupByProject: false, IncludeWorkspaceContainerCount: true}
	}
	return managedStatePrintOptions{GroupByProject: true}
}

func managedProjectCount(state managedState) int {
	seen := map[string]struct{}{}
	for _, container := range state.Containers {
		project := identity.NormalizeLabel(container.Project)
		if project == "" {
			project = "unknown"
		}
		seen[project] = struct{}{}
	}
	return len(seen)
}

func printManagedStateWithRuntimeDomainsAndOptions(cfg config.Config, state managedState, workspaceDomains map[string]string, runtimeAuthoritative bool, options managedStatePrintOptions) {
	printManagedStateWithRuntimeDomainsTo(os.Stdout, cfg, state, workspaceDomains, runtimeAuthoritative, options)
}

func printManagedStateWithRuntimeDomainsTo(out io.Writer, cfg config.Config, state managedState, workspaceDomains map[string]string, runtimeAuthoritative bool, options managedStatePrintOptions) {
	fmt.Fprintf(out, "runtime leases: %d\n", len(state.Leases))
	for _, lease := range state.Leases {
		host := ""
		if len(lease.Hostnames) > 0 {
			host = lease.Hostnames[0]
		}
		leaseStatus := "running"
		if !processAlive(lease.PID) {
			leaseStatus = "stale"
		}
		fmt.Fprintf(out, "- [%s] lease/%s pid=%d host=%s\n", leaseStatus, lease.ID, lease.PID, host)
	}

	fmt.Fprintf(out, "docker containers: %d\n", len(state.Containers))
	type workspaceNode struct {
		project    string
		workspace  string
		containers []dockerContainer
	}
	byScope := map[string]*workspaceNode{}
	for _, container := range state.Containers {
		project := identity.NormalizeLabel(container.Project)
		workspace := identity.NormalizeLabel(container.Workspace)
		if project == "" {
			project = "unknown"
		}
		if workspace == "" {
			workspace = "unknown"
		}
		key := workspace + "@" + project
		node, ok := byScope[key]
		if !ok {
			node = &workspaceNode{project: project, workspace: workspace}
			byScope[key] = node
		}
		node.containers = append(node.containers, container)
	}
	keys := make([]string, 0, len(byScope))
	for key := range byScope {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	multiWorkspace := len(keys) > 1
	byProjectKeys := map[string][]string{}
	projectOrder := []string{}
	projectSeen := map[string]struct{}{}
	for _, key := range keys {
		node := byScope[key]
		if _, ok := projectSeen[node.project]; !ok {
			projectSeen[node.project] = struct{}{}
			projectOrder = append(projectOrder, node.project)
		}
		byProjectKeys[node.project] = append(byProjectKeys[node.project], key)
	}
	sort.Strings(projectOrder)
	for _, project := range projectOrder {
		sort.Strings(byProjectKeys[project])
	}

	if multiWorkspace && options.GroupByProject {
		for _, project := range projectOrder {
			projectContainerCount := 0
			for _, key := range byProjectKeys[project] {
				projectContainerCount += len(byScope[key].containers)
			}
			fmt.Fprintf(out, "- project/%s containers=%d\n", project, projectContainerCount)
			for _, key := range byProjectKeys[project] {
				node := byScope[key]
				sort.Slice(node.containers, func(i, j int) bool {
					if node.containers[i].Service == node.containers[j].Service {
						return node.containers[i].Name < node.containers[j].Name
					}
					return node.containers[i].Service < node.containers[j].Service
				})
				domain := workspaceDisplayDomainWithRuntimeState(workspaceDomainDisplayInput{Cfg: cfg, Project: node.project, Workspace: node.workspace, WorkspaceDomains: workspaceDomains, RuntimeAuthoritative: runtimeAuthoritative})
				fmt.Fprintf(out, "  - [%s] workspace/%s@%s domain=%s containers=%d\n", aggregateWorkspaceStatus(node.containers), node.workspace, node.project, domain, len(node.containers))
				for _, container := range node.containers {
					fmt.Fprintf(out, "    - [%s] container/%s service=%s\n", containerMarker(container), container.Name, container.Service)
				}
			}
		}
		return
	}

	for _, key := range keys {
		node := byScope[key]
		sort.Slice(node.containers, func(i, j int) bool {
			if node.containers[i].Service == node.containers[j].Service {
				return node.containers[i].Name < node.containers[j].Name
			}
			return node.containers[i].Service < node.containers[j].Service
		})
		domain := workspaceDisplayDomainWithRuntimeState(workspaceDomainDisplayInput{Cfg: cfg, Project: node.project, Workspace: node.workspace, WorkspaceDomains: workspaceDomains, RuntimeAuthoritative: runtimeAuthoritative})
		if options.IncludeWorkspaceContainerCount {
			fmt.Fprintf(out, "- [%s] workspace/%s@%s domain=%s containers=%d\n", aggregateWorkspaceStatus(node.containers), node.workspace, node.project, domain, len(node.containers))
		} else {
			fmt.Fprintf(out, "- [%s] workspace/%s@%s domain=%s\n", aggregateWorkspaceStatus(node.containers), node.workspace, node.project, domain)
		}
		for _, container := range node.containers {
			fmt.Fprintf(out, "  - [%s] container/%s service=%s\n", containerMarker(container), container.Name, container.Service)
		}
	}
}

type workspaceDomainDisplayInput struct {
	Cfg                  config.Config
	Project              string
	Workspace            string
	WorkspaceDomains     map[string]string
	RuntimeAuthoritative bool
}

func workspaceDisplayDomainWithRuntimeState(input workspaceDomainDisplayInput) string {
	key := workspaceDomainKey(input.Workspace, input.Project)
	if key != "" {
		if value, ok := input.WorkspaceDomains[key]; ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if input.RuntimeAuthoritative {
		return "n/a"
	}
	return workspaceDisplayDomain(input.Cfg, input.Project, input.Workspace)
}

func workspaceDomainKey(workspace string, project string) string {
	workspace = identity.NormalizeLabel(workspace)
	project = identity.NormalizeLabel(project)
	if workspace == "" || project == "" {
		return ""
	}
	return workspace + "@" + project
}

func workspaceDomainKeyFromString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return ""
	}
	return workspaceDomainKey(parts[0], parts[1])
}

func workspaceDisplayDomain(cfg config.Config, project string, workspace string) string {
	hosts, err := identity.Hostnames(identity.HostnameInput{
		Domain:    cfg.Domain,
		Project:   project,
		Workspace: workspace,
		Pattern:   cfg.HostPattern,
	})
	if err != nil || strings.TrimSpace(hosts.WorkspaceFQDN) == "" {
		return "n/a"
	}
	return hosts.WorkspaceFQDN
}

func selectManagedTarget(state managedState, target string) (managedSelection, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return managedSelection{}, usageError("target is required")
	}
	if target == "all" {
		ids := make([]string, 0, len(state.Containers))
		seen := map[string]struct{}{}
		for _, c := range state.Containers {
			if _, ok := seen[c.ID]; ok {
				continue
			}
			seen[c.ID] = struct{}{}
			if c.Running {
				ids = append(ids, c.ID)
			}
		}
		sort.Strings(ids)
		return managedSelection{Leases: state.Leases, ContainerIDs: ids}, nil
	}

	if strings.HasPrefix(target, "lease/") {
		id := strings.TrimPrefix(target, "lease/")
		for _, lease := range state.Leases {
			if lease.ID == id {
				return managedSelection{Leases: []runtimeprovider.Lease{lease}}, nil
			}
		}
		return managedSelection{}, fmt.Errorf("lease %q not found", id)
	}

	if strings.HasPrefix(target, "container/") {
		needle := strings.TrimPrefix(target, "container/")
		for _, c := range state.Containers {
			if c.Name == needle || strings.HasPrefix(c.ID, needle) {
				if c.Running {
					return managedSelection{ContainerIDs: []string{c.ID}}, nil
				}
				return managedSelection{}, fmt.Errorf("container %q is not running", needle)
			}
		}
		return managedSelection{}, fmt.Errorf("container %q not found", needle)
	}

	if strings.HasPrefix(target, "project/") {
		project := identity.NormalizeLabel(strings.TrimPrefix(target, "project/"))
		ids := []string{}
		seen := map[string]struct{}{}
		for _, c := range state.Containers {
			if identity.NormalizeLabel(c.Project) != project || !c.Running {
				continue
			}
			if _, ok := seen[c.ID]; ok {
				continue
			}
			seen[c.ID] = struct{}{}
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return managedSelection{}, fmt.Errorf("project %q not found", project)
		}
		return managedSelection{ContainerIDs: ids}, nil
	}

	if strings.HasPrefix(target, "workspace/") {
		raw := strings.TrimPrefix(target, "workspace/")
		workspace := raw
		project := ""
		if at := strings.Index(raw, "@"); at >= 0 {
			workspace = raw[:at]
			project = raw[at+1:]
		}
		workspace = identity.NormalizeLabel(workspace)
		project = identity.NormalizeLabel(project)
		ids := []string{}
		seen := map[string]struct{}{}
		for _, c := range state.Containers {
			if identity.NormalizeLabel(c.Workspace) != workspace || !c.Running {
				continue
			}
			if project != "" && identity.NormalizeLabel(c.Project) != project {
				continue
			}
			if _, ok := seen[c.ID]; ok {
				continue
			}
			seen[c.ID] = struct{}{}
			ids = append(ids, c.ID)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return managedSelection{}, fmt.Errorf("workspace %q not found", raw)
		}
		return managedSelection{ContainerIDs: ids}, nil
	}

	for _, lease := range state.Leases {
		if lease.ID == target {
			return managedSelection{Leases: []runtimeprovider.Lease{lease}}, nil
		}
	}
	for _, c := range state.Containers {
		if c.Name == target || strings.HasPrefix(c.ID, target) {
			if c.Running {
				return managedSelection{ContainerIDs: []string{c.ID}}, nil
			}
			return managedSelection{}, fmt.Errorf("container %q is not running", target)
		}
	}

	return managedSelection{}, fmt.Errorf("unknown target %q\nuse `dnsvard ps` to list managed targets", target)
}
func dockerSignalContainers(ctx context.Context, containerIDs []string, sig syscall.Signal) error {
	if len(containerIDs) == 0 {
		return nil
	}
	dockerPath, err := resolveDockerCLIPath()
	if err != nil {
		return err
	}
	args := []string{"stop"}
	if sig == syscall.SIGKILL {
		args = []string{"kill"}
	}
	args = append(args, containerIDs...)
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s failed: %w\n%s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerRemoveContainers(ctx context.Context, containerIDs []string, force bool) error {
	if len(containerIDs) == 0 {
		return nil
	}
	dockerPath, err := resolveDockerCLIPath()
	if err != nil {
		return err
	}
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, containerIDs...)
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func discoverDockerContainers(ctx context.Context, all bool) ([]dockerContainer, error) {
	dockerPath, err := resolveDockerCLIPath()
	if err != nil {
		return nil, err
	}
	args := []string{"ps", "--format", "{{.ID}}"}
	if all {
		args = []string{"ps", "-a", "--format", "{{.ID}}"}
	}
	listCmd := exec.CommandContext(ctx, dockerPath, args...)
	out, err := listCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	inspectArgs := append([]string{"inspect"}, ids...)
	inspectCmd := exec.CommandContext(ctx, dockerPath, inspectArgs...)
	inspectOut, err := inspectCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker inspect failed: %w", err)
	}

	type inspectEntry struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
	}
	entries := []inspectEntry{}
	if err := json.Unmarshal(inspectOut, &entries); err != nil {
		return nil, fmt.Errorf("parse docker inspect: %w", err)
	}

	containers := make([]dockerContainer, 0, len(entries))
	for _, in := range entries {
		labels := in.Config.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		if !isDiscoverableContainer(labels) {
			continue
		}
		service := strings.TrimSpace(labels["com.docker.compose.service"])
		if service == "" {
			service = strings.TrimPrefix(strings.TrimSpace(in.Name), "/")
		}
		project := deriveProjectLabel(labels)
		workspace := deriveWorkspaceLabel(labels)
		containers = append(containers, dockerContainer{
			ID:        strings.TrimSpace(in.ID),
			Name:      strings.TrimPrefix(strings.TrimSpace(in.Name), "/"),
			Service:   service,
			Project:   project,
			Workspace: workspace,
			Status:    strings.TrimSpace(in.State.Status),
			Running:   in.State.Running,
		})
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Project == containers[j].Project {
			if containers[i].Workspace == containers[j].Workspace {
				return containers[i].Name < containers[j].Name
			}
			return containers[i].Workspace < containers[j].Workspace
		}
		return containers[i].Project < containers[j].Project
	})
	return containers, nil
}

func isDiscoverableContainer(labels map[string]string) bool {
	if labels == nil {
		return false
	}
	if hasDnsvardLabel(labels) {
		return true
	}
	if strings.TrimSpace(labels["com.docker.compose.project"]) != "" || strings.TrimSpace(labels["com.docker.compose.service"]) != "" || strings.TrimSpace(labels["com.docker.compose.project.working_dir"]) != "" {
		return true
	}
	return false
}

func hasDnsvardLabel(labels map[string]string) bool {
	if labels == nil {
		return false
	}
	for k, v := range labels {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(k)), "dnsvard.") && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func deriveProjectLabel(labels map[string]string) string {
	if v := strings.TrimSpace(labels["dnsvard.project"]); v != "" {
		return identity.NormalizeLabel(v)
	}
	if wd := strings.TrimSpace(labels["com.docker.compose.project.working_dir"]); wd != "" {
		parent := filepath.Base(filepath.Dir(wd))
		if parent != "." && parent != string(filepath.Separator) {
			return identity.NormalizeLabel(parent)
		}
		base := filepath.Base(wd)
		if base != "." && base != string(filepath.Separator) {
			return identity.NormalizeLabel(base)
		}
	}
	return identity.NormalizeLabel(labels["com.docker.compose.project"])
}

func deriveWorkspaceLabel(labels map[string]string) string {
	if v := strings.TrimSpace(labels["dnsvard.workspace"]); v != "" {
		return identity.NormalizeLabel(v)
	}
	if wd := strings.TrimSpace(labels["com.docker.compose.project.working_dir"]); wd != "" {
		base := filepath.Base(wd)
		if base != "." && base != string(filepath.Separator) {
			return identity.NormalizeLabel(base)
		}
	}
	return identity.NormalizeLabel(labels["com.docker.compose.project"])
}

func selectContainerTarget(containers []dockerContainer, target string) ([]dockerContainer, error) {
	return filterContainersForPS(containers, target)
}

func resolveDockerCLIPath() (string, error) {
	search := []string{"docker", "/opt/homebrew/bin/docker", "/usr/local/bin/docker", "/usr/bin/docker"}
	for _, candidate := range search {
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
	}
	return "", errors.New("docker executable not found")
}

func waitForSignalConvergence(ctx context.Context, leases []runtimeprovider.Lease, containerIDs []string) error {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		allStopped := true
		for _, lease := range leases {
			if processAlive(lease.PID) {
				allStopped = false
				break
			}
		}
		if allStopped {
			for _, id := range containerIDs {
				running, exists := dockerContainerRuntimeState(ctx, id)
				if exists && running {
					allStopped = false
					break
				}
			}
		}
		if allStopped {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("targets did not stop in time")
}

func waitForRemovalConvergence(ctx context.Context, containerIDs []string) error {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		allRemoved := true
		for _, id := range containerIDs {
			_, exists := dockerContainerRuntimeState(ctx, id)
			if exists {
				allRemoved = false
				break
			}
		}
		if allRemoved {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("targets did not remove in time")
}

func dockerContainerRuntimeState(ctx context.Context, id string) (bool, bool) {
	dockerPath, err := resolveDockerCLIPath()
	if err != nil {
		return false, false
	}
	cmd := exec.CommandContext(ctx, dockerPath, "inspect", "--format", "{{.State.Running}}", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, false
	}
	v := strings.TrimSpace(string(out))
	return strings.EqualFold(v, "true"), true
}

func commandTitle(name string) string {
	if name == "stop" {
		return "Stop"
	}
	if name == "kill" {
		return "Kill"
	}
	if name == "ps" {
		return "List"
	}
	return name
}
