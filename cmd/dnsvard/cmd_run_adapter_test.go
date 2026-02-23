package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseRunArgsAdapters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantAdapter string
		wantErr     string
	}{
		{name: "vite flag", args: []string{"--vite", "--", "bun", "dev"}, wantAdapter: "vite"},
		{name: "svelte flag", args: []string{"--svelte", "--", "bun", "dev"}, wantAdapter: "svelte"},
		{name: "webpack flag", args: []string{"--webpack", "--", "bun", "dev"}, wantAdapter: "webpack"},
		{name: "named adapter", args: []string{"--adapter", "nuxt", "--", "npm", "run", "dev"}, wantAdapter: "nuxt"},
		{name: "inline adapter", args: []string{"--adapter=astro", "--", "pnpm", "dev"}, wantAdapter: "astro"},
		{name: "conflicting adapters", args: []string{"--vite", "--next", "--", "bun", "dev"}, wantErr: "multiple adapters"},
		{name: "unknown adapter", args: []string{"--adapter", "unknown", "--", "bun", "dev"}, wantErr: "unknown adapter"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseRunArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRunArgs returned error: %v", err)
			}
			if got.Adapter != tt.wantAdapter {
				t.Fatalf("adapter = %q, want %q", got.Adapter, tt.wantAdapter)
			}
		})
	}
}

func TestSelectRunAdapterKnown(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"vite", "next", "nuxt", "astro", "svelte", "webpack"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			adapter := selectRunAdapter(name)
			if adapter == nil {
				t.Fatalf("selectRunAdapter(%q) returned nil", name)
			}
			if adapter.Name != name {
				t.Fatalf("adapter.Name = %q, want %q", adapter.Name, name)
			}
		})
	}
}

func TestRewriteDevServerBannerLine(t *testing.T) {
	t.Parallel()

	line := "  -> Local: http://127.0.0.1:5173/"
	got := rewriteDevServerBannerLine(line, 5173, "http://www.dnsvard.test")
	if !strings.Contains(got, "http://www.dnsvard.test/") {
		t.Fatalf("rewritten line = %q", got)
	}
}

func TestExtendViteAllowedHostsEnv(t *testing.T) {
	t.Setenv("__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS", "existing.test")
	env := extendViteAllowedHostsEnv([]string{"FOO=bar"}, "www.dnsvard.test")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=existing.test,www.dnsvard.test") {
		t.Fatalf("env did not include merged allowed hosts: %v", env)
	}
}

func TestAdjustAstroCommandAddsPort(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "astro direct",
			in:   []string{"astro", "dev"},
			want: []string{"astro", "dev", "--port", "58470"},
		},
		{
			name: "astro via bunx",
			in:   []string{"bunx", "astro", "dev"},
			want: []string{"bunx", "astro", "dev", "--port", "58470"},
		},
		{
			name: "astro via npx",
			in:   []string{"npx", "astro", "dev"},
			want: []string{"npx", "astro", "dev", "--port", "58470"},
		},
		{
			name: "non astro command unchanged",
			in:   []string{"bun", "dev"},
			want: []string{"bun", "dev"},
		},
		{
			name: "existing long port left unchanged",
			in:   []string{"astro", "dev", "--port", "4321"},
			want: []string{"astro", "dev", "--port", "4321"},
		},
		{
			name: "existing short port left unchanged",
			in:   []string{"astro", "dev", "-p", "4321"},
			want: []string{"astro", "dev", "-p", "4321"},
		},
		{
			name: "bunx flags still detect astro",
			in:   []string{"bunx", "--bun", "astro", "dev"},
			want: []string{"bunx", "--bun", "astro", "dev", "--port", "58470"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := adjustAstroCommand(tt.in, 58470)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("adjustAstroCommand(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectRunAdapterFromCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  []string
		want string
	}{
		{name: "direct next", cmd: []string{"next", "dev"}, want: "next"},
		{name: "bunx astro", cmd: []string{"bunx", "astro", "dev"}, want: "astro"},
		{name: "pnpm dlx vite", cmd: []string{"pnpm", "dlx", "vite"}, want: "vite"},
		{name: "npm exec webpack", cmd: []string{"npm", "exec", "webpack", "serve"}, want: "webpack"},
		{name: "non frontend", cmd: []string{"go", "test", "./..."}, want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, _ := detectRunAdapterFromCommand(tt.cmd)
			if got != tt.want {
				t.Fatalf("detectRunAdapterFromCommand(%v) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestDetectRunAdapterFromPackageJSON(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	manifest := `{
  "scripts": {
    "dev": "next dev"
  },
  "dependencies": {
    "next": "15.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	got, reason := detectRunAdapterFromPackageJSON([]string{"npm", "run", "dev"}, tmp)
	if got != "next" {
		t.Fatalf("detectRunAdapterFromPackageJSON returned %q, want next", got)
	}
	if !strings.Contains(reason, "package.json script") {
		t.Fatalf("reason = %q, want script reason", reason)
	}
}

func TestLikelyFrontendRunCommand(t *testing.T) {
	t.Parallel()

	if !likelyFrontendRunCommand([]string{"bun", "dev"}) {
		t.Fatalf("expected bun dev to be treated as likely frontend command")
	}
	if likelyFrontendRunCommand([]string{"go", "test", "./..."}) {
		t.Fatalf("expected go test to be treated as non-frontend command")
	}
}
