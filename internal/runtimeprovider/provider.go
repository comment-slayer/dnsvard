package runtimeprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/comment-slayer/dnsvard/internal/procutil"
)

type Lease struct {
	ID        string   `json:"id"`
	PID       int      `json:"pid"`
	Hostnames []string `json:"hostnames"`
	Domain    string   `json:"domain,omitempty"`
	HTTPPort  int      `json:"http_port,omitempty"`
	CreatedAt string   `json:"created_at"`
}

type Provider struct {
	statePath string
}

func New(stateDir string) *Provider {
	return &Provider{statePath: filepath.Join(stateDir, "runtime-leases.json")}
}

func (p *Provider) Upsert(lease Lease) error {
	if lease.ID == "" {
		return errors.New("lease id is required")
	}
	if lease.PID <= 0 {
		return errors.New("lease pid is required")
	}
	if len(lease.Hostnames) == 0 {
		return errors.New("lease hostnames are required")
	}
	if strings.TrimSpace(lease.CreatedAt) == "" {
		lease.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	state, err := p.load()
	if err != nil {
		return err
	}

	incomingHosts := canonicalHostSet(lease.Hostnames)
	for id, existing := range state {
		if id == lease.ID {
			continue
		}
		if hasHostnameOverlap(incomingHosts, existing.Hostnames) {
			delete(state, id)
		}
	}

	state[lease.ID] = lease
	return p.save(state)
}

func (p *Provider) Remove(id string) error {
	state, err := p.load()
	if err != nil {
		return err
	}
	delete(state, id)
	return p.save(state)
}

func (p *Provider) Active() ([]Lease, error) {
	state, err := p.load()
	if err != nil {
		return nil, err
	}

	changed := false
	for id, lease := range state {
		if !procutil.Running(lease.PID) {
			delete(state, id)
			changed = true
		}
	}
	if changed {
		if err := p.save(state); err != nil {
			return nil, err
		}
	}

	out := make([]Lease, 0, len(state))
	for _, lease := range state {
		out = append(out, lease)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (p *Provider) All() ([]Lease, error) {
	state, err := p.load()
	if err != nil {
		return nil, err
	}
	out := make([]Lease, 0, len(state))
	for _, lease := range state {
		out = append(out, lease)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (p *Provider) load() (map[string]Lease, error) {
	b, err := os.ReadFile(p.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Lease{}, nil
		}
		return nil, fmt.Errorf("read runtime leases: %w", err)
	}
	if len(b) == 0 {
		return map[string]Lease{}, nil
	}
	out := map[string]Lease{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse runtime leases: %w", err)
	}
	return out, nil
}

func (p *Provider) save(state map[string]Lease) error {
	if err := os.MkdirAll(filepath.Dir(p.statePath), 0o755); err != nil {
		return fmt.Errorf("create runtime state dir: %w", err)
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime leases: %w", err)
	}
	tmp := p.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write runtime lease temp: %w", err)
	}
	if err := os.Rename(tmp, p.statePath); err != nil {
		return fmt.Errorf("rename runtime lease file: %w", err)
	}
	return nil
}

func hasHostnameOverlap(incoming map[string]struct{}, existing []string) bool {
	for _, host := range existing {
		if _, ok := incoming[canonicalHost(host)]; ok {
			return true
		}
	}
	return false
}

func canonicalHost(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	return strings.TrimSuffix(v, ".")
}

func canonicalHostSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range values {
		host := canonicalHost(v)
		if host == "" {
			continue
		}
		out[host] = struct{}{}
	}
	return out
}
