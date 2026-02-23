package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestResolveRuntimeHTTPPortConfiguredBusyFails(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("addr type = %T, want *net.TCPAddr", ln.Addr())
	}

	t.Setenv("DNSVARD_HTTP_PORT", strconv.Itoa(addr.Port))

	_, injected, err := resolveRuntimeHTTPPort()
	if err == nil {
		t.Fatal("expected error for busy DNSVARD_HTTP_PORT")
	}
	if injected {
		t.Fatal("expected injected=false when configured port is unavailable")
	}
	if !strings.Contains(err.Error(), "is in use") {
		t.Fatalf("error = %q, want contains %q", err.Error(), "is in use")
	}
}

func TestResolveRuntimeHTTPPortInvalidEnvFails(t *testing.T) {
	t.Setenv("DNSVARD_HTTP_PORT", "not-a-port")

	_, injected, err := resolveRuntimeHTTPPort()
	if err == nil {
		t.Fatal("expected error for invalid DNSVARD_HTTP_PORT")
	}
	if injected {
		t.Fatal("expected injected=false on invalid env")
	}
	if !strings.Contains(err.Error(), "invalid DNSVARD_HTTP_PORT") {
		t.Fatalf("error = %q, want contains %q", err.Error(), "invalid DNSVARD_HTTP_PORT")
	}
}

func TestRandomHex(t *testing.T) {
	got, err := randomHex(6)
	if err != nil {
		t.Fatalf("randomHex returned error: %v", err)
	}
	if len(got) != 12 {
		t.Fatalf("randomHex len = %d, want 12", len(got))
	}
}
