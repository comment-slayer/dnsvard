package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type runAdapter struct {
	Name        string
	ExtendEnv   func(env []string, publicHost string) []string
	AdjustCmd   func(cmd []string, runtimePort int) []string
	RewriteLine func(line string, runtimePort int, publicURL string) string
}

var runAdapterByName = map[string]runAdapter{
	"vite": {
		Name:        "vite",
		ExtendEnv:   extendViteAllowedHostsEnv,
		RewriteLine: rewriteDevServerBannerLine,
	},
	"next": {
		Name:        "next",
		RewriteLine: rewriteDevServerBannerLine,
	},
	"nuxt": {
		Name:        "nuxt",
		RewriteLine: rewriteDevServerBannerLine,
	},
	"astro": {
		Name:        "astro",
		ExtendEnv:   extendViteAllowedHostsEnv,
		AdjustCmd:   adjustAstroCommand,
		RewriteLine: rewriteDevServerBannerLine,
	},
	"svelte": {
		Name:        "svelte",
		ExtendEnv:   extendViteAllowedHostsEnv,
		RewriteLine: rewriteDevServerBannerLine,
	},
	"webpack": {
		Name:        "webpack",
		RewriteLine: rewriteDevServerBannerLine,
	},
}

var runAdapterNames = []string{"vite", "next", "nuxt", "astro", "svelte", "webpack"}

var runAdapterByFlag = map[string]string{
	"--vite":    "vite",
	"--next":    "next",
	"--nuxt":    "nuxt",
	"--astro":   "astro",
	"--svelte":  "svelte",
	"--webpack": "webpack",
}

func selectRunAdapter(name string) *runAdapter {
	if name == "" {
		return nil
	}
	adapter, ok := runAdapterByName[name]
	if !ok {
		return nil
	}
	return &adapter
}

func isKnownRunAdapter(name string) bool {
	_, ok := runAdapterByName[name]
	return ok
}

func adapterNameForRunFlag(flag string) (string, bool) {
	name, ok := runAdapterByFlag[flag]
	return name, ok
}

func listRunAdapterNames() []string {
	return append([]string(nil), runAdapterNames...)
}

func unknownAdapterError(name string) error {
	return usageError(fmt.Sprintf("run: unknown adapter %q (known: %s)", name, strings.Join(listRunAdapterNames(), ", ")))
}

func adjustAstroCommand(cmd []string, runtimePort int) []string {
	if len(cmd) == 0 || runtimePort <= 0 {
		return cmd
	}
	if hasCommandPortFlag(cmd) {
		return cmd
	}
	if !isAstroCommand(cmd) {
		return cmd
	}

	adjusted := append([]string(nil), cmd...)
	adjusted = append(adjusted, "--port", strconv.Itoa(runtimePort))
	return adjusted
}

func hasCommandPortFlag(cmd []string) bool {
	for _, token := range cmd {
		if token == "--port" || token == "-p" {
			return true
		}
		if strings.HasPrefix(token, "--port=") {
			return true
		}
		if strings.HasPrefix(token, "-p") && len(token) > 2 {
			return true
		}
	}
	return false
}

func isAstroCommand(cmd []string) bool {
	if len(cmd) == 0 {
		return false
	}

	first := normalizeExecutableName(cmd[0])
	if first == "astro" {
		return true
	}

	switch first {
	case "bunx", "npx", "pnpx":
		for i := 1; i < len(cmd); i++ {
			token := strings.TrimSpace(cmd[i])
			if token == "" {
				continue
			}
			if strings.HasPrefix(token, "-") {
				continue
			}
			return normalizeExecutableName(token) == "astro"
		}
	}

	return false
}

func normalizeExecutableName(v string) string {
	name := strings.ToLower(strings.TrimSpace(filepath.Base(v)))
	name = strings.TrimSuffix(name, ".cmd")
	name = strings.TrimSuffix(name, ".exe")
	return name
}

type runPackageManifest struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func detectRunAdapter(cmd []string, cwd string) (string, string) {
	if adapter, reason := detectRunAdapterFromCommand(cmd); adapter != "" {
		return adapter, reason
	}
	if adapter, reason := detectRunAdapterFromPackageJSON(cmd, cwd); adapter != "" {
		return adapter, reason
	}
	return "", ""
}

func likelyFrontendRunCommand(cmd []string) bool {
	if len(cmd) == 0 {
		return false
	}
	if adapter, _ := detectRunAdapterFromCommand(cmd); adapter != "" {
		return true
	}
	name, ok := packageScriptNameForCommand(cmd)
	if !ok {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "dev" || name == "start" || name == "serve" || name == "preview"
}

func detectRunAdapterFromCommand(cmd []string) (string, string) {
	if len(cmd) == 0 {
		return "", ""
	}
	first := normalizeExecutableName(cmd[0])
	if adapter := adapterFromExecutableToken(first); adapter != "" {
		return adapter, fmt.Sprintf("command executable %q", first)
	}

	switch first {
	case "bunx", "npx", "pnpx":
		token := firstNonFlagToken(cmd[1:])
		if adapter := adapterFromExecutableToken(token); adapter != "" {
			return adapter, fmt.Sprintf("command token %q", token)
		}
	case "pnpm", "yarn", "npm", "bun":
		token := firstRuntimeToolToken(first, cmd[1:])
		if adapter := adapterFromExecutableToken(token); adapter != "" {
			return adapter, fmt.Sprintf("command token %q", token)
		}
	}

	return "", ""
}

func detectRunAdapterFromPackageJSON(cmd []string, cwd string) (string, string) {
	scriptName, ok := packageScriptNameForCommand(cmd)
	if !ok {
		return "", ""
	}
	manifest, ok := loadRunPackageManifest(cwd)
	if !ok {
		return "", ""
	}

	if scriptCommand, found := manifest.Scripts[strings.TrimSpace(scriptName)]; found {
		if adapter := adapterFromScriptCommand(scriptCommand); adapter != "" {
			return adapter, fmt.Sprintf("package.json script %q", scriptName)
		}
	}

	if adapter := adapterFromManifestDependencies(manifest); adapter != "" {
		return adapter, "package.json dependencies"
	}

	return "", ""
}

func packageScriptNameForCommand(cmd []string) (string, bool) {
	if len(cmd) < 2 {
		return "", false
	}
	first := normalizeExecutableName(cmd[0])

	scriptAfterRun := func(args []string) (string, bool) {
		for i := 0; i < len(args); i++ {
			token := strings.TrimSpace(args[i])
			if token == "" || strings.HasPrefix(token, "-") {
				continue
			}
			if token == "run" || token == "run-script" {
				continue
			}
			return token, true
		}
		return "", false
	}

	switch first {
	case "npm":
		if len(cmd) >= 3 && (cmd[1] == "run" || cmd[1] == "run-script") {
			return strings.TrimSpace(cmd[2]), true
		}
		return scriptAfterRun(cmd[1:])
	case "pnpm":
		if len(cmd) >= 3 && cmd[1] == "run" {
			return strings.TrimSpace(cmd[2]), true
		}
		if len(cmd) >= 3 && cmd[1] == "exec" {
			return "", false
		}
		if len(cmd) >= 3 && cmd[1] == "dlx" {
			return "", false
		}
		return scriptAfterRun(cmd[1:])
	case "yarn":
		if len(cmd) >= 3 && cmd[1] == "run" {
			return strings.TrimSpace(cmd[2]), true
		}
		if len(cmd) >= 2 && cmd[1] == "dlx" {
			return "", false
		}
		return scriptAfterRun(cmd[1:])
	case "bun":
		if len(cmd) >= 3 && cmd[1] == "run" {
			return strings.TrimSpace(cmd[2]), true
		}
		if len(cmd) >= 2 && (cmd[1] == "x" || cmd[1] == "bunx") {
			return "", false
		}
		return scriptAfterRun(cmd[1:])
	default:
		return "", false
	}
}

func firstRuntimeToolToken(first string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	if first == "pnpm" && (args[0] == "dlx" || args[0] == "exec") {
		return firstNonFlagToken(args[1:])
	}
	if first == "yarn" && args[0] == "dlx" {
		return firstNonFlagToken(args[1:])
	}
	if first == "npm" && args[0] == "exec" {
		return firstNonFlagToken(args[1:])
	}
	if first == "bun" && (args[0] == "x" || args[0] == "bunx") {
		return firstNonFlagToken(args[1:])
	}
	return ""
}

func firstNonFlagToken(tokens []string) string {
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" || strings.HasPrefix(token, "-") {
			continue
		}
		return normalizeExecutableName(token)
	}
	return ""
}

func adapterFromExecutableToken(token string) string {
	switch normalizeExecutableName(token) {
	case "vite":
		return "vite"
	case "next":
		return "next"
	case "nuxt", "nuxi":
		return "nuxt"
	case "astro":
		return "astro"
	case "svelte-kit", "sveltekit":
		return "svelte"
	case "webpack", "webpack-dev-server":
		return "webpack"
	default:
		return ""
	}
}

func loadRunPackageManifest(cwd string) (runPackageManifest, bool) {
	if strings.TrimSpace(cwd) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return runPackageManifest{}, false
		}
		cwd = wd
	}

	path := filepath.Join(cwd, "package.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return runPackageManifest{}, false
	}

	manifest := runPackageManifest{}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return runPackageManifest{}, false
	}
	return manifest, true
}

func adapterFromScriptCommand(command string) string {
	v := strings.ToLower(strings.TrimSpace(command))
	if v == "" {
		return ""
	}
	if strings.Contains(v, "svelte-kit") {
		return "svelte"
	}
	if strings.Contains(v, "webpack-dev-server") {
		return "webpack"
	}
	for _, token := range strings.FieldsFunc(v, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) {
		switch token {
		case "next":
			return "next"
		case "nuxt", "nuxi":
			return "nuxt"
		case "astro":
			return "astro"
		case "vite":
			return "vite"
		case "webpack":
			return "webpack"
		}
	}
	return ""
}

func adapterFromManifestDependencies(manifest runPackageManifest) string {
	candidates := map[string]struct{}{}
	add := func(dep string, adapter string) {
		if strings.TrimSpace(dep) == "" || strings.TrimSpace(adapter) == "" {
			return
		}
		if _, ok := manifest.Dependencies[dep]; ok {
			candidates[adapter] = struct{}{}
		}
		if _, ok := manifest.DevDependencies[dep]; ok {
			candidates[adapter] = struct{}{}
		}
	}

	add("next", "next")
	add("nuxt", "nuxt")
	add("nuxi", "nuxt")
	add("astro", "astro")
	add("@sveltejs/kit", "svelte")
	add("vite", "vite")
	add("webpack", "webpack")
	add("webpack-dev-server", "webpack")

	if len(candidates) != 1 {
		return ""
	}
	for adapter := range candidates {
		return adapter
	}
	return ""
}
