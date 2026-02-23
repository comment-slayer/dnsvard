package routes

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

type Source string

const (
	SourceDocker  Source = "docker"
	SourceRuntime Source = "runtime"
	SourceManual  Source = "manual"
)

type Entry struct {
	Hostname string
	IP       netip.Addr
	Source   Source
}

type Result struct {
	Entries  []Entry
	Warnings []string
}

func Merge(entries ...Entry) Result {
	type current struct {
		entry    Entry
		priority int
	}

	chosen := map[string]current{}
	warnings := []string{}

	for _, entry := range entries {
		host := canonical(entry.Hostname)
		entry.Hostname = host
		p := priorityFor(entry.Source)
		if existing, ok := chosen[host]; ok {
			if existing.entry.IP == entry.IP {
				continue
			}
			if p > existing.priority {
				warnings = append(warnings, fmt.Sprintf("route %s from %s overrides %s", host, entry.Source, existing.entry.Source))
				chosen[host] = current{entry: entry, priority: p}
			} else {
				warnings = append(warnings, fmt.Sprintf("route %s from %s ignored; %s has higher precedence", host, entry.Source, existing.entry.Source))
			}
			continue
		}
		chosen[host] = current{entry: entry, priority: p}
	}

	out := make([]Entry, 0, len(chosen))
	for _, c := range chosen {
		out = append(out, c.entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Hostname < out[j].Hostname
	})

	return Result{Entries: out, Warnings: warnings}
}

func priorityFor(source Source) int {
	switch source {
	case SourceManual:
		return 300
	case SourceRuntime:
		return 200
	case SourceDocker:
		return 100
	default:
		return 0
	}
}

func canonical(hostname string) string {
	h := strings.TrimSpace(strings.ToLower(hostname))
	h = strings.Trim(h, ".")
	if h == "" {
		return "."
	}
	return h + "."
}
