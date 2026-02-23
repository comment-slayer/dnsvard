package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/comment-slayer/dnsvard/internal/config"
	"github.com/comment-slayer/dnsvard/internal/ownership"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

func resolverSpecFromListen(cfg config.Config, domain string) (platform.ResolverSpec, error) {
	host, port, err := parseResolverListen(cfg.DNSListen)
	if err != nil {
		return platform.ResolverSpec{}, err
	}
	return platform.ResolverSpec{Domain: normalizeResolverDomain(domain), Nameserver: host, Port: port}, nil
}

type resolverSyncState struct {
	Domains   []string `json:"domains"`
	DNSListen string   `json:"dns_listen"`
}

type bootstrapState struct {
	Version int `json:"version"`
}

func bootstrapStatePath(stateDir string) string {
	return filepath.Join(stateDir, "bootstrap-state.json")
}

func readBootstrapStateVersion(stateDir string) (int, bool, error) {
	b, err := os.ReadFile(bootstrapStatePath(stateDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	state := bootstrapState{}
	if err := json.Unmarshal(b, &state); err != nil {
		return 0, false, err
	}
	if state.Version <= 0 {
		return 0, false, nil
	}
	return state.Version, true, nil
}

func writeBootstrapStateVersion(stateDir string, version int) error {
	if version <= 0 {
		return errors.New("bootstrap state version must be > 0")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(bootstrapState{Version: version}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(bootstrapStatePath(stateDir), b, 0o644)
}

func resolverSyncStatePath(stateDir string) string {
	return filepath.Join(stateDir, "resolver-sync-state.json")
}

func persistResolverSyncState(cfg config.Config) error {
	path := resolverSyncStatePath(cfg.StateDir)
	state := resolverSyncState{Domains: cfg.Domains, DNSListen: cfg.DNSListen}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode resolver sync state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create resolver sync state dir: %w", err)
	}
	if err := ownership.ChownPathToSudoInvoker(filepath.Dir(path)); err != nil {
		return fmt.Errorf("set resolver sync state dir ownership %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write resolver sync state %s: %w", path, err)
	}
	if err := ownership.ChownPathToSudoInvoker(path); err != nil {
		return fmt.Errorf("set resolver sync state ownership %s: %w", path, err)
	}
	return nil
}

type resolverReconcileOptions struct {
	OnlyWhenDrift bool
}

type resolverReconcileResult struct {
	Desired []string
	Removed []string
	Changed bool
	Host    string
	Port    string
}

func parseResolverListen(v string) (string, string, error) {
	listen := strings.TrimSpace(v)
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", "", fmt.Errorf("parse dns_listen %q: %w", v, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	return host, strings.TrimSpace(port), nil
}

func normalizeResolverDomain(v string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(v), "."))
}

func normalizeResolverDomainList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		n := normalizeResolverDomain(v)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func reconcileDesiredResolvers(domains []string, dnsListen string, plat platform.Controller, opts resolverReconcileOptions) (resolverReconcileResult, error) {
	host, port, err := parseResolverListen(dnsListen)
	if err != nil {
		return resolverReconcileResult{}, err
	}
	desiredDomains := normalizeResolverDomainList(domains)
	desiredSet := make(map[string]struct{}, len(desiredDomains))
	for _, d := range desiredDomains {
		desiredSet[d] = struct{}{}
	}

	changed := false
	for _, domain := range desiredDomains {
		spec := platform.ResolverSpec{Domain: domain, Nameserver: host, Port: port}
		if opts.OnlyWhenDrift {
			match, matchErr := plat.ResolverMatches(spec)
			if matchErr != nil {
				return resolverReconcileResult{}, matchErr
			}
			if match {
				continue
			}
		}
		if err := plat.EnsureResolver(spec); err != nil {
			return resolverReconcileResult{}, err
		}
		changed = true
	}

	managed, err := plat.ListManagedResolvers()
	if err != nil {
		return resolverReconcileResult{}, err
	}
	removed := make([]string, 0)
	for _, domain := range managed {
		n := normalizeResolverDomain(domain)
		if n == "" {
			continue
		}
		if _, ok := desiredSet[n]; ok {
			continue
		}
		spec := platform.ResolverSpec{Domain: n, Nameserver: host, Port: port}
		if err := plat.RemoveResolver(spec); err != nil {
			return resolverReconcileResult{}, err
		}
		removed = append(removed, n)
		changed = true
	}
	sort.Strings(removed)
	return resolverReconcileResult{Desired: desiredDomains, Removed: removed, Changed: changed, Host: host, Port: port}, nil
}

func syncManagedResolvers(path string, plat platform.Controller) (string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read resolver sync state %s: %w", path, err)
	}
	state := resolverSyncState{}
	if err := json.Unmarshal(b, &state); err != nil {
		return "", false, fmt.Errorf("parse resolver sync state %s: %w", path, err)
	}
	if strings.TrimSpace(state.DNSListen) == "" {
		return "", false, nil
	}

	result, err := reconcileDesiredResolvers(state.Domains, state.DNSListen, plat, resolverReconcileOptions{OnlyWhenDrift: true})
	if err != nil {
		return "", false, err
	}
	if len(result.Desired) == 0 {
		return "", false, nil
	}
	return strings.Join(result.Desired, ",") + "@" + result.Host + ":" + result.Port, result.Changed, nil
}

func reconcileResolvers(cfg config.Config, plat platform.Controller) ([]string, []string, error) {
	result, err := reconcileDesiredResolvers(cfg.Domains, cfg.DNSListen, plat, resolverReconcileOptions{})
	if err != nil {
		return nil, nil, err
	}
	return result.Desired, result.Removed, nil
}

func ensureDNSListenAvailable(listenAddr string) error {
	pc, err := net.ListenPacket("udp", listenAddr)
	if err != nil {
		_, port, splitErr := net.SplitHostPort(listenAddr)
		if splitErr != nil {
			port = "<port>"
		}
		return fmt.Errorf("dns listen address %s is already in use\nfix: stop existing daemon first with `dnsvard daemon stop`\ninspect: `lsof -nP -iUDP:%s`", listenAddr, port)
	}
	_ = pc.Close()
	return nil
}

func waitForDNSListenAvailable(listenAddr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ensureDNSListenAvailable(listenAddr); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ensureDNSListenAvailable(listenAddr)
}
