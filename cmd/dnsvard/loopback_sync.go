package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/comment-slayer/dnsvard/internal/allocator"
	"github.com/comment-slayer/dnsvard/internal/platform"
)

func runLoopbackSync(stateFile string, resolverStateFile string, cidr string) error {
	if os.Geteuid() != 0 {
		return errors.New("loopback-sync must run as root; run with sudo")
	}
	if strings.TrimSpace(stateFile) == "" {
		return errors.New("daemon loopback-sync requires --state-file")
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return fmt.Errorf("parse cidr: %w", err)
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	plat := platform.New()
	resolverSignature := ""

	for {
		if err := syncLoopbackAliases(stateFile, prefix); err != nil {
			fmt.Fprintf(os.Stderr, "loopback-sync: %v\n", err)
		}
		if strings.TrimSpace(resolverStateFile) != "" {
			sig, changed, err := syncManagedResolvers(resolverStateFile, plat)
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolver-sync: %v\n", err)
			} else if changed && sig != resolverSignature {
				resolverSignature = sig
				if err := plat.FlushDNSCache(); err != nil {
					fmt.Fprintf(os.Stderr, "resolver-sync cache flush: %v\n", err)
				}
			}
		}
		<-ticker.C
	}
}

func syncLoopbackAliases(stateFile string, prefix netip.Prefix) error {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	state := allocator.State{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &state); err != nil {
			return err
		}
	}
	active := map[string]struct{}{}
	for _, asg := range state.Assignments {
		if !asg.ReleasedAt.IsZero() {
			continue
		}
		ip, err := netip.ParseAddr(asg.IP)
		if err != nil {
			continue
		}
		if !prefix.Contains(ip) {
			continue
		}
		active[ip.String()] = struct{}{}
		if err := ensureLoopbackAlias(ip.String()); err != nil {
			return err
		}
	}
	current, err := currentLoopbackAliases(prefix)
	if err != nil {
		return err
	}
	for _, ip := range current {
		if _, ok := active[ip]; ok {
			continue
		}
		if err := removeLoopbackAlias(ip); err != nil {
			return err
		}
	}
	return nil
}

func ensureLoopbackAlias(ip string) error {
	cmd := exec.Command("ifconfig", "lo0", "alias", ip, "up")
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "file exists") || strings.Contains(text, "exists") {
			return nil
		}
		return fmt.Errorf("ifconfig lo0 alias %s failed: %s", ip, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeLoopbackAlias(ip string) error {
	cmd := exec.Command("ifconfig", "lo0", "-alias", ip)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "can't assign requested address") || strings.Contains(text, "invalid argument") {
			return nil
		}
		return fmt.Errorf("ifconfig lo0 -alias %s failed: %s", ip, strings.TrimSpace(string(out)))
	}
	return nil
}

func currentLoopbackAliases(prefix netip.Prefix) ([]string, error) {
	cmd := exec.Command("ifconfig", "lo0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read lo0 aliases: %w", err)
	}

	aliases := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "inet ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip, err := netip.ParseAddr(fields[1])
		if err != nil || !ip.Is4() {
			continue
		}
		if !prefix.Contains(ip) {
			continue
		}
		if ip.String() == "127.0.0.1" {
			continue
		}
		aliases = append(aliases, ip.String())
	}
	return aliases, nil
}
