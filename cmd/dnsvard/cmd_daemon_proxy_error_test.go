package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/comment-slayer/dnsvard/internal/httprouter"
)

func TestIsRouteUnreachableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		detail string
		err    error
		want   bool
	}{
		{name: "no route", err: errors.New("dial tcp 192.168.1.2:80: connect: no route to host"), want: true},
		{name: "network unreachable", err: errors.New("dial tcp 10.0.0.2:80: network is unreachable"), want: true},
		{name: "host down in detail", detail: "host is down", err: errors.New("dial failed"), want: true},
		{name: "connection refused", err: errors.New("dial tcp 127.0.0.1:80: connect: connection refused"), want: false},
		{name: "timeout", err: errors.New("dial tcp 10.0.0.2:80: i/o timeout"), want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isRouteUnreachableError(tc.detail, tc.err)
			if got != tc.want {
				t.Fatalf("isRouteUnreachableError(%q, %v) = %t, want %t", tc.detail, tc.err, got, tc.want)
			}
		})
	}
}

func TestPreserveLastHealthyHTTPRoutes(t *testing.T) {
	t.Parallel()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(healthy.Close)

	next := []httprouter.Route{{Hostname: "api.master.cs", Target: "http://127.0.0.1:65534"}}
	previous := map[string]string{"api.master.cs": healthy.URL}

	out, fallbacks := preserveLastHealthyHTTPRoutes(next, previous)
	if fallbacks != 1 {
		t.Fatalf("fallbacks = %d, want 1", fallbacks)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].Target != healthy.URL {
		t.Fatalf("target = %q, want %q", out[0].Target, healthy.URL)
	}
}
