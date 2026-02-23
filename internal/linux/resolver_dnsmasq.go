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

var dnsmasqDirPath = "/etc/dnsmasq.d"
var restartDnsmasqFn = restartDnsmasq

func EnsureDnsmasqResolver(spec ResolverSpec) error {
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

	path := dnsmasqResolverPath(domain)
	want := renderDnsmasqConfig(domain, spec.Nameserver, spec.Port)

	current, err := os.ReadFile(path)
	if err == nil {
		if string(current) == want {
			return nil
		}
		parsed := parseDnsmasqConfig(string(current))
		if !parsed.Managed {
			return fmt.Errorf("resolver config conflict at %s; file is not managed by dnsvard", path)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read resolver config %s: %w", path, err)
	}

	if err := os.MkdirAll(dnsmasqDirPath, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("creating %s requires elevated permissions; re-run with sudo", dnsmasqDirPath)
		}
		return fmt.Errorf("create dnsmasq config dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("writing %s requires elevated permissions; re-run with sudo", path)
		}
		return fmt.Errorf("write resolver config %s: %w", path, err)
	}

	if err := restartDnsmasqFn(); err != nil {
		return err
	}
	return nil
}

func DnsmasqResolverMatches(spec ResolverSpec) (bool, error) {
	domain := normalizeDomain(spec.Domain)
	if domain == "" {
		return false, errors.New("resolver domain is required")
	}
	b, err := os.ReadFile(dnsmasqResolverPath(domain))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read resolver config: %w", err)
	}
	parsed := parseDnsmasqConfig(string(b))
	if !parsed.Managed {
		return false, nil
	}
	return parsed.Domain == domain && parsed.Nameserver == strings.TrimSpace(spec.Nameserver) && parsed.Port == strings.TrimSpace(spec.Port), nil
}

func RemoveDnsmasqResolver(spec ResolverSpec) error {
	domain := normalizeDomain(spec.Domain)
	if domain == "" {
		return errors.New("resolver domain is required")
	}
	path := dnsmasqResolverPath(domain)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read resolver config %s: %w", path, err)
	}
	if !parseDnsmasqConfig(string(b)).Managed {
		return fmt.Errorf("resolver config %s is not managed by dnsvard", path)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("removing %s requires elevated permissions; re-run with sudo", path)
		}
		return fmt.Errorf("remove resolver config %s: %w", path, err)
	}
	if err := restartDnsmasqFn(); err != nil {
		return err
	}
	return nil
}

func ListManagedDnsmasqResolvers() ([]string, error) {
	entries, err := os.ReadDir(dnsmasqDirPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dnsmasq dir %s: %w", dnsmasqDirPath, err)
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
		path := filepath.Join(dnsmasqDirPath, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsed := parseDnsmasqConfig(string(b))
		if parsed.Managed && parsed.Domain != "" {
			out = append(out, parsed.Domain)
		}
	}

	sort.Strings(out)
	return out, nil
}

func dnsmasqResolverPath(domain string) string {
	return filepath.Join(dnsmasqDirPath, fmt.Sprintf("dnsvard-%s.conf", domain))
}

func renderDnsmasqConfig(domain string, nameserver string, port string) string {
	return fmt.Sprintf("%s\n# domain: %s\nserver=/%s/%s#%s\n", managedByComment, domain, domain, strings.TrimSpace(nameserver), strings.TrimSpace(port))
}

type parsedDnsmasq struct {
	Managed    bool
	Domain     string
	Nameserver string
	Port       string
}

func parseDnsmasqConfig(v string) parsedDnsmasq {
	out := parsedDnsmasq{}
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
		if strings.HasPrefix(strings.ToLower(line), "server=/") {
			rest := strings.TrimPrefix(line, "server=/")
			parts := strings.Split(rest, "/")
			if len(parts) < 2 {
				continue
			}
			out.Domain = normalizeDomain(parts[0])
			target := parts[1]
			hostPort := strings.SplitN(target, "#", 2)
			if len(hostPort) == 2 {
				out.Nameserver = strings.TrimSpace(hostPort[0])
				out.Port = strings.TrimSpace(hostPort[1])
			}
		}
	}
	return out
}

func restartDnsmasq() error {
	cmd := exec.Command("systemctl", "restart", "dnsmasq")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	cmdNM := exec.Command("systemctl", "restart", "NetworkManager")
	nmOut, nmErr := cmdNM.CombinedOutput()
	if nmErr == nil {
		return nil
	}

	return fmt.Errorf(
		"restart dns resolver backend failed: systemctl restart dnsmasq -> %v (%s); systemctl restart NetworkManager -> %v (%s)",
		err,
		strings.TrimSpace(string(out)),
		nmErr,
		strings.TrimSpace(string(nmOut)),
	)
}
