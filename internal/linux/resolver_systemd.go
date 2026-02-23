package linux

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ResolverSpec struct {
	Domain     string
	Nameserver string
	Port       string
}

const managedByComment = "# managed-by: dnsvard"

var resolvedDropInDir = "/etc/systemd/resolved.conf.d"

func EnsureSystemdResolvedResolver(spec ResolverSpec) error {
	domain := normalizeDomain(spec.Domain)
	if domain == "" {
		return errors.New("resolver domain is required")
	}
	if strings.TrimSpace(spec.Nameserver) == "" {
		return errors.New("resolver nameserver is required")
	}
	if strings.TrimSpace(spec.Port) == "" {
		return errors.New("resolver port is required")
	}

	path := resolverConfigPath(domain)
	want := renderSystemdResolvedDropIn(domain, spec.Nameserver, spec.Port)
	current, err := os.ReadFile(path)
	if err == nil {
		if string(current) == want {
			return nil
		}
		parsed := parseSystemdResolvedDropIn(string(current))
		if !parsed.Managed {
			return fmt.Errorf("resolver config conflict at %s; file is not managed by dnsvard", path)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read resolver config %s: %w", path, err)
	}

	if err := os.MkdirAll(resolvedDropInDir, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("creating %s requires elevated permissions; re-run with sudo", resolvedDropInDir)
		}
		return fmt.Errorf("create resolver config dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("writing %s requires elevated permissions; re-run with sudo", path)
		}
		return fmt.Errorf("write resolver config %s: %w", path, err)
	}

	if err := restartSystemdResolved(); err != nil {
		return err
	}
	return nil
}

func SystemdResolvedResolverMatches(spec ResolverSpec) (bool, error) {
	domain := normalizeDomain(spec.Domain)
	if domain == "" {
		return false, errors.New("resolver domain is required")
	}

	b, err := os.ReadFile(resolverConfigPath(domain))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read resolver config: %w", err)
	}

	parsed := parseSystemdResolvedDropIn(string(b))
	if !parsed.Managed {
		return false, nil
	}
	wantNameserver := strings.TrimSpace(spec.Nameserver)
	wantPort := strings.TrimSpace(spec.Port)
	return parsed.Nameserver == wantNameserver && parsed.Port == wantPort && parsed.Domain == domain, nil
}

func RemoveSystemdResolvedResolver(spec ResolverSpec) error {
	domain := normalizeDomain(spec.Domain)
	if domain == "" {
		return errors.New("resolver domain is required")
	}
	path := resolverConfigPath(domain)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read resolver config %s: %w", path, err)
	}
	if !parseSystemdResolvedDropIn(string(b)).Managed {
		return fmt.Errorf("resolver config %s is not managed by dnsvard", path)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("removing %s requires elevated permissions; re-run with sudo", path)
		}
		return fmt.Errorf("remove resolver config %s: %w", path, err)
	}
	if err := restartSystemdResolved(); err != nil {
		return err
	}
	return nil
}

func ListManagedSystemdResolvedResolvers() ([]string, error) {
	entries, err := os.ReadDir(resolvedDropInDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read resolver config dir %s: %w", resolvedDropInDir, err)
	}

	out := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "dnsvard-") || !strings.HasSuffix(name, ".conf") {
			continue
		}
		path := filepath.Join(resolvedDropInDir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsed := parseSystemdResolvedDropIn(string(b))
		if parsed.Managed && parsed.Domain != "" {
			out = append(out, parsed.Domain)
		}
	}

	sort.Strings(out)
	return out, nil
}

func resolverConfigPath(domain string) string {
	return filepath.Join(resolvedDropInDir, fmt.Sprintf("dnsvard-%s.conf", domain))
}

func renderSystemdResolvedDropIn(domain string, nameserver string, port string) string {
	dns := strings.TrimSpace(nameserver)
	if strings.TrimSpace(port) != "" {
		dns = fmt.Sprintf("%s:%s", dns, strings.TrimSpace(port))
	}
	return fmt.Sprintf("%s\n# domain: %s\n[Resolve]\nDNS=%s\nDomains=~%s\n", managedByComment, domain, dns, domain)
}

type parsedSystemdResolved struct {
	Managed    bool
	Domain     string
	Nameserver string
	Port       string
}

func parseSystemdResolvedDropIn(v string) parsedSystemdResolved {
	out := parsedSystemdResolved{}
	s := bufio.NewScanner(strings.NewReader(v))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, managedByComment) {
			out.Managed = true
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "# domain:") {
			out.Domain = normalizeDomain(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "# domain:")))
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "dns=") {
			dnsValue := strings.TrimSpace(strings.TrimPrefix(line, "DNS="))
			hostPort := strings.SplitN(dnsValue, ":", 2)
			out.Nameserver = strings.TrimSpace(hostPort[0])
			if len(hostPort) == 2 {
				out.Port = strings.TrimSpace(hostPort[1])
			}
			continue
		}
	}
	return out
}

func normalizeDomain(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.Trim(v, ".")
	return v
}

func restartSystemdResolved() error {
	cmd := exec.Command("systemctl", "restart", "systemd-resolved")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart systemd-resolved: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
