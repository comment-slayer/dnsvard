package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miekg/dns"

	"github.com/comment-slayer/dnsvard/internal/daemon"
)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

func waitForDaemonPID(stateDir string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid, err := daemon.ReadPID(stateDir)
		if err == nil && daemon.ProcessRunning(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func notifyDaemonReconcile(stateDir string) {
	pid, err := daemon.ReadPID(stateDir)
	if err != nil {
		return
	}
	if !daemon.ProcessRunning(pid) {
		return
	}
	_ = killPID(pid, syscall.SIGHUP)
}

func waitForDNSRecords(hostnames []string, dnsListen string, timeout time.Duration) error {
	if len(hostnames) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, h := range hostnames {
			ok, err := dnsHasARecord(h, dnsListen)
			if err != nil || !ok {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func dnsHasARecord(hostname string, dnsListen string) (bool, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(hostname), dns.TypeA)

	client := &dns.Client{Timeout: 300 * time.Millisecond}
	resp, _, err := client.Exchange(msg, dnsListen)
	if err != nil {
		return false, err
	}
	if resp == nil {
		return false, nil
	}
	if resp.Rcode != dns.RcodeSuccess {
		return false, nil
	}
	for _, ans := range resp.Answer {
		if _, ok := ans.(*dns.A); ok {
			return true, nil
		}
	}
	return false, nil
}

func waitForSystemDNS(hostnames []string, timeout time.Duration) error {
	if len(hostnames) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, h := range hostnames {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			addrs, err := net.DefaultResolver.LookupHost(ctx, h)
			cancel()
			if err != nil || len(addrs) == 0 {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func resolveRuntimeHTTPPort() (int, bool, error) {
	if p, ok, err := parsePortEnv("DNSVARD_HTTP_PORT"); ok || err != nil {
		if err != nil {
			return 0, false, err
		}
		if isTCPPortAvailable(p) {
			return p, true, nil
		}
		return 0, false, fmt.Errorf("configured DNSVARD_HTTP_PORT %d is in use", p)
	}

	p, err := allocateLocalPort()
	if err != nil {
		return 0, false, err
	}
	return p, true, nil
}

func parsePortEnv(keys ...string) (int, bool, error) {
	for _, key := range keys {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			continue
		}
		p, err := strconv.Atoi(v)
		if err != nil {
			return 0, true, fmt.Errorf("invalid %s=%q", key, v)
		}
		if p == 0 {
			continue
		}
		if p < 1 || p > 65535 {
			return 0, true, fmt.Errorf("invalid %s=%q", key, v)
		}
		return p, true, nil
	}
	return 0, false, nil
}

func allocateLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate local port: %w", err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return 0, errors.New("failed to allocate tcp port")
	}
	return addr.Port, nil
}

func isTCPPortAvailable(port int) bool {
	if !canListenOnce("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		return false
	}
	if !canListenOnceIPv6(port) {
		return false
	}
	return true
}

func canListenOnce(network string, addr string) bool {
	ln, err := net.Listen(network, addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func canListenOnceIPv6(port int) bool {
	addr := net.JoinHostPort("::1", strconv.Itoa(port))
	ln, err := net.Listen("tcp6", addr)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "address family not supported") || strings.Contains(errText, "no suitable address found") {
			return true
		}
		return false
	}
	_ = ln.Close()
	return true
}

func tailFile(path string, maxLines int) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read log file %s: %w", path, err)
	}
	content := string(b)
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content, nil
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n"), nil
}
