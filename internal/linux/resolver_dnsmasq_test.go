package linux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderParseDnsmasqConfig(t *testing.T) {
	t.Parallel()

	raw := renderDnsmasqConfig("example.test", "127.0.0.1", "1053")
	parsed := parseDnsmasqConfig(raw)
	if !parsed.Managed {
		t.Fatal("expected managed marker")
	}
	if parsed.Domain != "example.test" {
		t.Fatalf("domain = %q, want example.test", parsed.Domain)
	}
	if parsed.Nameserver != "127.0.0.1" {
		t.Fatalf("nameserver = %q, want 127.0.0.1", parsed.Nameserver)
	}
	if parsed.Port != "1053" {
		t.Fatalf("port = %q, want 1053", parsed.Port)
	}
}

func TestEnsureDnsmasqResolverRejectsUnmanagedConflict(t *testing.T) {
	tmp := t.TempDir()
	prevDir := dnsmasqDirPath
	prevRestart := restartDnsmasqFn
	dnsmasqDirPath = tmp
	restartDnsmasqFn = func() error { return nil }
	t.Cleanup(func() {
		dnsmasqDirPath = prevDir
		restartDnsmasqFn = prevRestart
	})

	path := dnsmasqResolverPath("example.test")
	if err := os.WriteFile(path, []byte("server=/example.test/127.0.0.1#1053\n"), 0o644); err != nil {
		t.Fatalf("seed conflict file: %v", err)
	}

	err := EnsureDnsmasqResolver(ResolverSpec{Domain: "example.test", Nameserver: "127.0.0.1", Port: "1053"})
	if err == nil {
		t.Fatal("expected unmanaged conflict error")
	}
	if !strings.Contains(err.Error(), "not managed by dnsvard") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureAndRemoveDnsmasqResolver(t *testing.T) {
	tmp := t.TempDir()
	prevDir := dnsmasqDirPath
	prevRestart := restartDnsmasqFn
	dnsmasqDirPath = tmp
	restartDnsmasqFn = func() error { return nil }
	t.Cleanup(func() {
		dnsmasqDirPath = prevDir
		restartDnsmasqFn = prevRestart
	})

	spec := ResolverSpec{Domain: "example.test", Nameserver: "127.0.0.1", Port: "1053"}
	if err := EnsureDnsmasqResolver(spec); err != nil {
		t.Fatalf("EnsureDnsmasqResolver returned error: %v", err)
	}

	ok, err := DnsmasqResolverMatches(spec)
	if err != nil {
		t.Fatalf("DnsmasqResolverMatches returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected resolver match")
	}

	if err := RemoveDnsmasqResolver(spec); err != nil {
		t.Fatalf("RemoveDnsmasqResolver returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmp, "dnsvard-example.test.conf")); !os.IsNotExist(err) {
		t.Fatalf("expected resolver file removed, stat err=%v", err)
	}
}

func TestListManagedDnsmasqResolvers(t *testing.T) {
	tmp := t.TempDir()
	prevDir := dnsmasqDirPath
	dnsmasqDirPath = tmp
	t.Cleanup(func() {
		dnsmasqDirPath = prevDir
	})

	files := map[string]string{
		"dnsvard-zeta.test.conf":  renderDnsmasqConfig("zeta.test", "127.0.0.1", "1053"),
		"dnsvard-alpha.test.conf": renderDnsmasqConfig("alpha.test", "127.0.0.1", "1053"),
		"other.conf":              "server=/other.test/127.0.0.1#1053\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := ListManagedDnsmasqResolvers()
	if err != nil {
		t.Fatalf("ListManagedDnsmasqResolvers returned error: %v", err)
	}
	want := []string{"alpha.test", "zeta.test"}
	if len(got) != len(want) {
		t.Fatalf("resolver count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolver[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
