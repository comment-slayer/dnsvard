package httprouter

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRouterProxiesByHost(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(backend.Close)

	r := New()
	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: backend.URL}}); err != nil {
		t.Fatalf("SetRoutes returned error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := r.Start(addr); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "api.master.project.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("response body = %q, want %q", string(b), "ok")
	}
}

func TestRouterProxyErrorHandler(t *testing.T) {
	t.Parallel()

	r := New()
	var mu sync.Mutex
	called := 0
	host := ""
	target := ""
	r.SetProxyErrorHandler(func(h string, t string, _ error) {
		mu.Lock()
		called++
		host = h
		target = t
		mu.Unlock()
	})

	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: "http://127.0.0.1:65534"}}); err != nil {
		t.Fatalf("SetRoutes returned error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := r.Start(addr); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "api.master.project.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}

	mu.Lock()
	defer mu.Unlock()
	if called == 0 {
		t.Fatal("expected proxy error handler to be called")
	}
	if host != "api.master.project.test" {
		t.Fatalf("host = %q", host)
	}
	if target != "http://127.0.0.1:65534" {
		t.Fatalf("target = %q", target)
	}
}

func TestRouterCircuitBreakerOpensAfterFailures(t *testing.T) {
	t.Parallel()

	r := New()
	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: "http://127.0.0.1:65534"}}); err != nil {
		t.Fatalf("SetRoutes returned error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := r.Start(addr); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
		req.Host = "api.master.project.test"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
		_ = resp.Body.Close()
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "api.master.project.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("breaker request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestRouterRecoversAfterRouteUpdate(t *testing.T) {
	t.Parallel()

	r := New()
	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: "http://127.0.0.1:65534"}}); err != nil {
		t.Fatalf("SetRoutes returned error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := r.Start(addr); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "api.master.project.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	_ = resp.Body.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(backend.Close)

	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: backend.URL}}); err != nil {
		t.Fatalf("SetRoutes update returned error: %v", err)
	}

	req2, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req2.Host = "api.master.project.test"
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("request after route update failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestRouterRefreshClearsBreakerImmediately(t *testing.T) {
	t.Parallel()

	r := New()
	target := "http://127.0.0.1:65534"
	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: target}}); err != nil {
		t.Fatalf("SetRoutes returned error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := r.Start(addr); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop() })

	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
		req.Host = "api.master.project.test"
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			t.Fatalf("request %d failed: %v", i+1, reqErr)
		}
		_ = resp.Body.Close()
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "api.master.project.test"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("breaker request failed: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status before refresh = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	_ = resp.Body.Close()

	if err := r.SetRoutes([]Route{{Hostname: "api.master.project.test", Target: target}}); err != nil {
		t.Fatalf("SetRoutes refresh returned error: %v", err)
	}

	req2, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req2.Host = "api.master.project.test"
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("request after refresh failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadGateway {
		t.Fatalf("status after refresh = %d, want %d", resp2.StatusCode, http.StatusBadGateway)
	}
}
