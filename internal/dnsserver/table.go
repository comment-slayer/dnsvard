package dnsserver

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
)

type Record struct {
	Hostname string
	IP       netip.Addr
}

type Table struct {
	zone         string
	managedZones []string
	records      map[string]netip.Addr
	mu           sync.RWMutex
}

func NewTable(zone string) *Table {
	canonical := canonicalFQDN(zone)
	return &Table{
		zone:         canonical,
		managedZones: []string{canonical},
		records:      map[string]netip.Addr{},
	}
}

func (t *Table) Zone() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.zone
}

func (t *Table) SetZone(zone string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.zone = canonicalFQDN(zone)
	t.managedZones = []string{t.zone}
	t.records = map[string]netip.Addr{}
}

func (t *Table) SetManagedZones(zones []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.managedZones = normalizeManagedZones(zones)
}

func (t *Table) Allows(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return hasManagedZone(t.managedZones, canonicalFQDN(name))
}

func (t *Table) ZoneForName(name string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if zone, ok := bestManagedZone(t.managedZones, canonicalFQDN(name)); ok {
		return zone
	}
	return t.zone
}

func (t *Table) Set(records []Record) error {
	next := map[string]netip.Addr{}
	for _, r := range records {
		h := canonicalFQDN(r.Hostname)
		if !strings.HasSuffix(h, t.zone) {
			return fmt.Errorf("record %q is outside zone %q", h, t.zone)
		}
		if existing, ok := next[h]; ok && existing != r.IP {
			return fmt.Errorf("duplicate hostname %q with multiple ips", h)
		}
		next[h] = r.IP
	}

	t.mu.Lock()
	t.records = next
	t.mu.Unlock()
	return nil
}

func (t *Table) Lookup(name string) (netip.Addr, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ip, ok := t.records[canonicalFQDN(name)]
	return ip, ok
}

func (t *Table) Exists(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.records[canonicalFQDN(name)]
	return ok
}

func (t *Table) Snapshot() []Record {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Record, 0, len(t.records))
	for h, ip := range t.records {
		out = append(out, Record{Hostname: h, IP: ip})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Hostname < out[j].Hostname
	})
	return out
}

func canonicalFQDN(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.Trim(n, ".")
	if n == "" {
		return "."
	}
	return n + "."
}

func normalizeManagedZones(zones []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(zones))
	for _, zone := range zones {
		z := canonicalFQDN(zone)
		if z == "." {
			continue
		}
		if _, ok := seen[z]; ok {
			continue
		}
		seen[z] = struct{}{}
		out = append(out, z)
	}
	sort.Strings(out)
	return out
}

func hasManagedZone(zones []string, fqdn string) bool {
	for _, zone := range zones {
		if strings.HasSuffix(fqdn, zone) {
			return true
		}
	}
	return false
}

func bestManagedZone(zones []string, fqdn string) (string, bool) {
	best := ""
	for _, zone := range zones {
		if !strings.HasSuffix(fqdn, zone) {
			continue
		}
		if len(zone) > len(best) {
			best = zone
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}
