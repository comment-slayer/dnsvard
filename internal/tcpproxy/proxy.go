package tcpproxy

import (
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Route struct {
	ListenIP   string
	ListenPort int
	TargetIP   string
	TargetPort int
}

type listenerState struct {
	listener net.Listener
	stop     chan struct{}
	route    Route
}

type Proxy struct {
	mu        sync.Mutex
	listeners map[string]listenerState
}

func New() *Proxy {
	return &Proxy{listeners: map[string]listenerState{}}
}

func (p *Proxy) SetRoutes(routes []Route) error {
	desired := map[string]Route{}
	for _, r := range routes {
		if r.ListenPort <= 0 || r.TargetPort <= 0 {
			continue
		}
		key := listenerKey(r.ListenIP, r.ListenPort)
		desired[key] = r
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for key, state := range p.listeners {
		if _, ok := desired[key]; ok {
			continue
		}
		close(state.stop)
		_ = state.listener.Close()
		delete(p.listeners, key)
	}

	for key, route := range desired {
		if state, ok := p.listeners[key]; ok {
			if sameRoute(state.route, route) {
				continue
			}
			close(state.stop)
			_ = state.listener.Close()
			delete(p.listeners, key)
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(route.ListenIP, strconv.Itoa(route.ListenPort)))
		if err != nil {
			return fmt.Errorf("listen tcp proxy %s:%d: %w", route.ListenIP, route.ListenPort, err)
		}
		state := listenerState{listener: ln, stop: make(chan struct{}), route: route}
		p.listeners[key] = state
		go serveListener(ln, route, state.stop)
	}

	return nil
}

func (p *Proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, state := range p.listeners {
		close(state.stop)
		_ = state.listener.Close()
		delete(p.listeners, key)
	}
}

func (p *Proxy) Snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.listeners))
	for key := range p.listeners {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func serveListener(ln net.Listener, route Route, stop <-chan struct{}) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-stop:
				return
			default:
			}
			continue
		}
		go handleConn(conn, route)
	}
}

func handleConn(src net.Conn, route Route) {
	defer func() { _ = src.Close() }()
	dst, err := net.DialTimeout("tcp", net.JoinHostPort(route.TargetIP, strconv.Itoa(route.TargetPort)), 2*time.Second)
	if err != nil {
		return
	}
	defer func() { _ = dst.Close() }()

	go func() {
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
	}()
	_, _ = io.Copy(src, dst)
}

func listenerKey(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}

func sameRoute(a Route, b Route) bool {
	return a.ListenIP == b.ListenIP &&
		a.ListenPort == b.ListenPort &&
		a.TargetIP == b.TargetIP &&
		a.TargetPort == b.TargetPort
}
