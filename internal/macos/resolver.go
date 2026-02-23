package macos

import (
	"bufio"
	"errors"
	"fmt"
	"os"
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

var resolverDirPath = "/etc/resolver"

func EnsureResolver(spec ResolverSpec) error {
	if strings.TrimSpace(spec.Domain) == "" {
		return errors.New("resolver domain is required")
	}
	if strings.TrimSpace(spec.Nameserver) == "" {
		return errors.New("resolver nameserver is required")
	}
	if strings.TrimSpace(spec.Port) == "" {
		return errors.New("resolver port is required")
	}

	resolverPath := filepath.Join(resolverDirPath, spec.Domain)
	want := renderResolver(spec)

	current, err := os.ReadFile(resolverPath)
	if err == nil {
		cur := parseResolverFile(string(current))
		wantParsed := parseResolverFile(want)
		if cur.Nameserver == wantParsed.Nameserver && cur.Port == wantParsed.Port {
			if cur.Managed {
				return nil
			}
			if err := os.WriteFile(resolverPath, []byte(want), 0o644); err != nil {
				if errors.Is(err, os.ErrPermission) {
					return fmt.Errorf("writing %s requires elevated permissions; re-run with sudo", resolverPath)
				}
				return fmt.Errorf("write resolver file: %w", err)
			}
			return nil
		}
		if cur.Managed {
			if err := os.WriteFile(resolverPath, []byte(want), 0o644); err != nil {
				if errors.Is(err, os.ErrPermission) {
					return fmt.Errorf("writing %s requires elevated permissions; re-run with sudo", resolverPath)
				}
				return fmt.Errorf("write resolver file: %w", err)
			}
			return nil
		}
		return fmt.Errorf("resolver file conflict at %s; expected:\n%s\nfound:\n%s\nset another domain in config (domain: yourzone) or update/remove conflicting resolver", resolverPath, strings.TrimSpace(want), strings.TrimSpace(string(current)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read resolver file %s: %w", resolverPath, err)
	}

	if err := os.MkdirAll(resolverDirPath, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("creating %s requires elevated permissions; re-run with sudo", resolverDirPath)
		}
		return fmt.Errorf("create resolver dir: %w", err)
	}

	if err := os.WriteFile(resolverPath, []byte(want), 0o644); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("writing %s requires elevated permissions; re-run with sudo", resolverPath)
		}
		return fmt.Errorf("write resolver file: %w", err)
	}

	return nil
}

func ResolverMatches(spec ResolverSpec) (bool, error) {
	resolverPath := filepath.Join(resolverDirPath, spec.Domain)
	b, err := os.ReadFile(resolverPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	cur := parseResolverFile(string(b))
	if !cur.Managed {
		return false, nil
	}
	want := parseResolverFile(renderResolver(spec))
	return cur.Nameserver == want.Nameserver && cur.Port == want.Port, nil
}

func RemoveResolver(spec ResolverSpec) error {
	if strings.TrimSpace(spec.Domain) == "" {
		return errors.New("resolver domain is required")
	}
	resolverPath := filepath.Join(resolverDirPath, spec.Domain)
	b, err := os.ReadFile(resolverPath)
	if err == nil {
		if !parseResolverFile(string(b)).Managed {
			return fmt.Errorf("resolver file %s is not managed by dnsvard", resolverPath)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read resolver file %s: %w", resolverPath, err)
	}
	if err := os.Remove(resolverPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("removing %s requires elevated permissions; re-run with sudo", resolverPath)
		}
		return fmt.Errorf("remove resolver file %s: %w", resolverPath, err)
	}
	return nil
}

func renderResolver(spec ResolverSpec) string {
	return fmt.Sprintf("%s\n# domain: %s\nnameserver %s\nport %s\n", managedByComment, strings.TrimSpace(spec.Domain), spec.Nameserver, spec.Port)
}

func ListManagedResolvers() ([]string, error) {
	entries, err := os.ReadDir(resolverDirPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read resolver dir %s: %w", resolverDirPath, err)
	}
	out := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(resolverDirPath, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if parseResolverFile(string(b)).Managed {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

type parsedResolver struct {
	Managed    bool
	Nameserver string
	Port       string
}

func parseResolverFile(v string) parsedResolver {
	out := parsedResolver{}
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
		if strings.HasPrefix(strings.ToLower(line), "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		switch strings.ToLower(parts[0]) {
		case "nameserver":
			out.Nameserver = strings.TrimSpace(parts[1])
		case "port":
			out.Port = strings.TrimSpace(parts[1])
		}
	}
	return out
}
