package identity

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var invalidLabelChars = regexp.MustCompile(`[^a-z0-9-]+`)

type ProjectInput struct {
	RemoteURL string
	RepoBase  string
}

type WorkspaceInput struct {
	WorktreeBase string
	Branch       string
	CwdBase      string
}

type HostnameInput struct {
	Domain    string
	Project   string
	Workspace string
	Service   string
	Pattern   string
}

type HostnameSet struct {
	ProjectFQDN   string
	WorkspaceFQDN string
	ServiceFQDN   string
}

const DefaultHostPattern = "service-workspace-project-tld"

var allowedPatternTokens = map[string]struct{}{
	"service":   {},
	"workspace": {},
	"project":   {},
	"tld":       {},
}

func DeriveProjectLabel(in ProjectInput) (string, error) {
	raw := projectRawName(in.RemoteURL)
	if raw == "" {
		raw = in.RepoBase
	}
	label := NormalizeLabel(raw)
	if label == "" {
		label = "project"
	}
	return label, nil
}

func DeriveWorkspaceLabel(in WorkspaceInput) (string, error) {
	candidates := []string{in.WorktreeBase, in.Branch, in.CwdBase}
	for _, c := range candidates {
		if label := NormalizeLabel(c); label != "" {
			return label, nil
		}
	}
	return "default", nil
}

func Hostnames(in HostnameInput) (HostnameSet, error) {
	domain := normalizeDomain(in.Domain)
	if domain == "" {
		return HostnameSet{}, errors.New("domain is required")
	}
	project := NormalizeLabel(in.Project)
	workspace := NormalizeLabel(in.Workspace)
	if project == "" || workspace == "" {
		return HostnameSet{}, errors.New("project and workspace are required")
	}
	pattern, err := ParseHostPattern(in.Pattern)
	if err != nil {
		return HostnameSet{}, err
	}

	set := HostnameSet{
		ProjectFQDN:   fmt.Sprintf("%s.%s", project, domain),
		WorkspaceFQDN: renderPatternHost(patternHostInput{Tokens: pattern.WithoutService(), Project: project, Workspace: workspace, Domain: domain}),
	}

	if service := NormalizeLabel(in.Service); service != "" && pattern.HasService {
		set.ServiceFQDN = renderPatternHost(patternHostInput{Tokens: pattern.Tokens, Project: project, Workspace: workspace, Service: service, Domain: domain})
	}

	return set, nil
}

type HostPattern struct {
	Tokens     []string
	HasService bool
}

func (p HostPattern) WithoutService() []string {
	out := make([]string, 0, len(p.Tokens))
	for _, token := range p.Tokens {
		if token == "service" {
			continue
		}
		out = append(out, token)
	}
	return out
}

func ParseHostPattern(raw string) (HostPattern, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		v = DefaultHostPattern
	}

	parts := strings.Split(v, "-")
	if len(parts) < 2 {
		return HostPattern{}, fmt.Errorf("host_pattern %q is invalid", raw)
	}

	seen := map[string]struct{}{}
	hasWorkspace := false
	hasTLD := false
	hasService := false

	for _, part := range parts {
		if _, ok := allowedPatternTokens[part]; !ok {
			return HostPattern{}, fmt.Errorf("host_pattern %q contains unknown token %q", raw, part)
		}
		if _, ok := seen[part]; ok {
			return HostPattern{}, fmt.Errorf("host_pattern %q contains duplicate token %q", raw, part)
		}
		seen[part] = struct{}{}
		switch part {
		case "workspace":
			hasWorkspace = true
		case "tld":
			hasTLD = true
		case "service":
			hasService = true
		}
	}

	if !hasWorkspace {
		return HostPattern{}, fmt.Errorf("host_pattern %q must include workspace", raw)
	}
	if !hasTLD {
		return HostPattern{}, fmt.Errorf("host_pattern %q must include tld", raw)
	}
	if parts[len(parts)-1] != "tld" {
		return HostPattern{}, fmt.Errorf("host_pattern %q must end with tld", raw)
	}

	return HostPattern{Tokens: parts, HasService: hasService}, nil
}

type patternHostInput struct {
	Tokens    []string
	Project   string
	Workspace string
	Service   string
	Domain    string
}

func renderPatternHost(input patternHostInput) string {
	tokens := input.Tokens
	project := input.Project
	workspace := input.Workspace
	service := input.Service
	domain := input.Domain

	labels := make([]string, 0, len(tokens))
	for _, token := range tokens {
		switch token {
		case "service":
			if service == "" {
				continue
			}
			labels = append(labels, service)
		case "workspace":
			labels = append(labels, workspace)
		case "project":
			labels = append(labels, project)
		case "tld":
			labels = append(labels, domain)
		}
	}
	return strings.Join(labels, ".")
}

func NormalizeLabel(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.TrimSuffix(v, ".git")
	v = invalidLabelChars.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-")
	if len(v) > 63 {
		v = strings.Trim(v[:63], "-")
	}
	return v
}

func WorkspaceID(path string) string {
	h := sha1.Sum([]byte(path))
	return hex.EncodeToString(h[:])[:8]
}

func ResolveDefaultWorkspace(workspaces []string) string {
	if len(workspaces) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(workspaces))
	seen := map[string]struct{}{}
	for _, ws := range workspaces {
		n := NormalizeLabel(ws)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}

	for _, preferred := range []string{"master", "main"} {
		for _, ws := range normalized {
			if ws == preferred {
				return ws
			}
		}
	}

	return normalized[0]
}

func projectRawName(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}

	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err == nil {
			return path.Base(u.Path)
		}
	}

	if idx := strings.LastIndex(remote, ":"); idx >= 0 {
		remote = remote[idx+1:]
	}
	return path.Base(remote)
}

func normalizeDomain(domain string) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.Trim(d, ".")
	return d
}
