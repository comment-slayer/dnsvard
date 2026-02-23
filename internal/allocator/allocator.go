package allocator

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

const defaultCooldown = 10 * time.Minute

type Assignment struct {
	WorkspaceID string    `json:"workspace_id"`
	IP          string    `json:"ip"`
	AssignedAt  time.Time `json:"assigned_at"`
	ReleasedAt  time.Time `json:"released_at,omitempty"`
}

type State struct {
	Assignments map[string]Assignment `json:"assignments"`
}

type Allocator struct {
	prefix    netip.Prefix
	state     State
	statePath string
	now       func() time.Time
	cooldown  time.Duration
}

func New(cidr string, statePath string) (*Allocator, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse loopback cidr: %w", err)
	}
	if !prefix.Addr().Is4() {
		return nil, errors.New("only ipv4 cidr is currently supported")
	}

	a := &Allocator{
		prefix:    prefix.Masked(),
		statePath: statePath,
		now:       time.Now,
		cooldown:  defaultCooldown,
		state:     State{Assignments: map[string]Assignment{}},
	}

	if err := a.load(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Allocator) Allocate(workspaceID string) (netip.Addr, error) {
	if workspaceID == "" {
		return netip.Addr{}, errors.New("workspace id is required")
	}

	if asg, ok := a.state.Assignments[workspaceID]; ok {
		ip, err := netip.ParseAddr(asg.IP)
		if err == nil {
			return ip, nil
		}
	}

	base := a.hashToHost(workspaceID)
	maxHosts := hostCapacity(a.prefix)
	if maxHosts < 4 {
		return netip.Addr{}, errors.New("loopback cidr is too small")
	}

	now := a.now().UTC()
	for i := uint32(0); i < maxHosts; i++ {
		host := ((base + i) % maxHosts)
		if host < 2 {
			host += 2
		}
		ip, err := hostToAddr(a.prefix, host)
		if err != nil {
			continue
		}
		if a.isIPAvailable(ip, now) {
			a.state.Assignments[workspaceID] = Assignment{
				WorkspaceID: workspaceID,
				IP:          ip.String(),
				AssignedAt:  now,
			}
			if err := a.save(); err != nil {
				return netip.Addr{}, err
			}
			return ip, nil
		}
	}

	return netip.Addr{}, errors.New("no free loopback ip available in cidr")
}

func (a *Allocator) Release(workspaceID string) error {
	asg, ok := a.state.Assignments[workspaceID]
	if !ok {
		return nil
	}
	asg.ReleasedAt = a.now().UTC()
	a.state.Assignments[workspaceID] = asg
	return a.save()
}

func (a *Allocator) Assignments() map[string]Assignment {
	out := make(map[string]Assignment, len(a.state.Assignments))
	for k, v := range a.state.Assignments {
		out[k] = v
	}
	return out
}

func (a *Allocator) isIPAvailable(ip netip.Addr, now time.Time) bool {
	for _, asg := range a.state.Assignments {
		if asg.IP != ip.String() {
			continue
		}
		if asg.ReleasedAt.IsZero() {
			return false
		}
		if now.Sub(asg.ReleasedAt) < a.cooldown {
			return false
		}
	}
	return true
}

func (a *Allocator) hashToHost(workspaceID string) uint32 {
	h := sha1.Sum([]byte(workspaceID))
	return binary.BigEndian.Uint32(h[:4])
}

func (a *Allocator) load() error {
	b, err := os.ReadFile(a.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read allocator state: %w", err)
	}

	if len(b) == 0 {
		return nil
	}

	if err := json.Unmarshal(b, &a.state); err != nil {
		return fmt.Errorf("parse allocator state: %w", err)
	}
	if a.state.Assignments == nil {
		a.state.Assignments = map[string]Assignment{}
	}
	return nil
}

func (a *Allocator) save() error {
	if err := os.MkdirAll(filepath.Dir(a.statePath), 0o755); err != nil {
		return fmt.Errorf("create allocator state dir: %w", err)
	}

	b, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode allocator state: %w", err)
	}

	tmp := a.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write allocator state temp: %w", err)
	}
	if err := os.Rename(tmp, a.statePath); err != nil {
		return fmt.Errorf("rename allocator state: %w", err)
	}
	return nil
}

func hostCapacity(prefix netip.Prefix) uint32 {
	bits := 32 - prefix.Bits()
	if bits <= 0 {
		return 0
	}
	if bits >= 31 {
		return 1 << 30
	}
	return uint32(1 << bits)
}

func hostToAddr(prefix netip.Prefix, host uint32) (netip.Addr, error) {
	base := prefix.Addr().As4()
	baseNum := binary.BigEndian.Uint32(base[:])
	addrNum := baseNum + host
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], addrNum)
	ip := netip.AddrFrom4(out)
	if !prefix.Contains(ip) {
		return netip.Addr{}, errors.New("calculated ip out of prefix")
	}
	return ip, nil
}
