package httprouter

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Route struct {
	Hostname string
	Target   string
}

type Router struct {
	mu      sync.RWMutex
	routes  map[string]*httputil.ReverseProxy
	targets map[string]string
	onError func(hostname string, target string, err error)
	breaker map[string]breakerState

	server *http.Server
}

type breakerState struct {
	failures  int
	openUntil time.Time
}

func New() *Router {
	return &Router{
		routes:  map[string]*httputil.ReverseProxy{},
		targets: map[string]string{},
		breaker: map[string]breakerState{},
	}
}

func (r *Router) SetProxyErrorHandler(handler func(hostname string, target string, err error)) {
	r.mu.Lock()
	r.onError = handler
	r.mu.Unlock()
}

func (r *Router) SetRoutes(routes []Route) error {
	nextRoutes := map[string]*httputil.ReverseProxy{}
	nextTargets := map[string]string{}

	for _, route := range routes {
		host := canonicalHost(route.Hostname)
		if host == "" {
			return errors.New("http route hostname is required")
		}
		target := strings.TrimSpace(route.Target)
		if target == "" {
			return fmt.Errorf("http route target is required for %s", host)
		}
		u, err := url.Parse(target)
		if err != nil {
			return fmt.Errorf("parse route target for %s: %w", host, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("unsupported route scheme %q for %s", u.Scheme, host)
		}
		hostname := host
		targetURL := target
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.ModifyResponse = func(_ *http.Response) error {
			r.recordSuccess(hostname)
			return nil
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			r.recordFailure(hostname)
			r.mu.RLock()
			onError := r.onError
			r.mu.RUnlock()
			if onError != nil {
				onError(hostname, targetURL, err)
			}
			http.Error(w, "dnsvard: upstream unavailable", http.StatusBadGateway)
		}
		nextRoutes[host] = proxy
		nextTargets[host] = target
	}

	r.mu.Lock()
	r.routes = nextRoutes
	r.targets = nextTargets
	for host := range r.breaker {
		if _, ok := nextRoutes[host]; !ok {
			delete(r.breaker, host)
		}
	}
	for host := range nextRoutes {
		delete(r.breaker, host)
	}
	r.mu.Unlock()
	return nil
}

func (r *Router) Start(listenAddr string) error {
	r.server = &http.Server{Addr: listenAddr, Handler: http.HandlerFunc(r.handle)}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen http router on %s: %w", listenAddr, err)
	}

	go func() {
		_ = r.server.Serve(ln)
	}()
	return nil
}

func (r *Router) Stop() error {
	if r.server == nil {
		return nil
	}
	return r.server.Close()
}

func (r *Router) Snapshot() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.targets))
	for k, v := range r.targets {
		out[k] = v
	}
	return out
}

func (r *Router) handle(w http.ResponseWriter, req *http.Request) {
	host := canonicalHost(req.Host)
	if open, retryAfter := r.isBreakerOpen(host); open {
		if retryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		}
		http.Error(w, "dnsvard: upstream temporarily degraded", http.StatusServiceUnavailable)
		return
	}
	r.mu.RLock()
	proxy, ok := r.routes[host]
	r.mu.RUnlock()
	if !ok {
		http.Error(w, "dnsvard: no route for host", http.StatusServiceUnavailable)
		return
	}
	proxy.ServeHTTP(w, req)
}

func (r *Router) recordSuccess(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.breaker[host]
	st.failures = 0
	st.openUntil = time.Time{}
	r.breaker[host] = st
}

func (r *Router) recordFailure(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.breaker[host]
	st.failures++
	if st.failures >= 5 {
		st.openUntil = time.Now().Add(10 * time.Second)
	}
	r.breaker[host] = st
}

func (r *Router) isBreakerOpen(host string) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.breaker[host]
	if !ok || st.openUntil.IsZero() {
		return false, 0
	}
	now := time.Now()
	if now.After(st.openUntil) {
		st.openUntil = time.Time{}
		st.failures = 0
		r.breaker[host] = st
		return false, 0
	}
	retryAfter := int(st.openUntil.Sub(now).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	return true, retryAfter
}

func canonicalHost(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimSuffix(v, ".")
	if i := strings.Index(v, ":"); i >= 0 {
		v = v[:i]
	}
	return v
}
