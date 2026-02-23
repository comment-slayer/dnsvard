package dnsserver

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

type Server struct {
	table *Table
	ttl   uint32

	udp *dns.Server
	tcp *dns.Server

	mu      sync.Mutex
	started bool
}

func New(listenAddr string, table *Table, ttl uint32) *Server {
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		handleDNS(w, r, table, ttl)
	})

	return &Server{
		table: table,
		ttl:   ttl,
		udp:   &dns.Server{Addr: listenAddr, Net: "udp", Handler: handler},
		tcp:   &dns.Server{Addr: listenAddr, Net: "tcp", Handler: handler},
	}
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}

	udpConn, err := net.ListenPacket("udp", s.udp.Addr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	tcpListener, err := net.Listen("tcp", s.tcp.Addr)
	if err != nil {
		_ = udpConn.Close()
		return fmt.Errorf("listen tcp: %w", err)
	}

	s.udp.PacketConn = udpConn
	s.tcp.Listener = tcpListener

	go func() { _ = s.udp.ActivateAndServe() }()
	go func() { _ = s.tcp.ActivateAndServe() }()

	s.started = true
	return nil
}

func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	s.mu.Unlock()

	if err := s.udp.Shutdown(); err != nil {
		return err
	}
	if err := s.tcp.Shutdown(); err != nil {
		return err
	}
	return nil
}

func handleDNS(w dns.ResponseWriter, r *dns.Msg, table *Table, ttl uint32) {
	msg := new(dns.Msg)
	msg.SetReply(r)
	msg.Authoritative = true

	for _, q := range r.Question {
		qName := strings.ToLower(q.Name)
		if !table.Allows(qName) {
			msg.SetRcode(r, dns.RcodeRefused)
			_ = w.WriteMsg(msg)
			return
		}
		zone := table.ZoneForName(qName)

		ip, ok := table.Lookup(qName)
		if !ok {
			if rr := unresolvedNameLoopbackRR(q, ttl); rr != nil {
				msg.Answer = append(msg.Answer, rr...)
				continue
			}
			msg.SetRcode(r, dns.RcodeNameError)
			msg.Ns = append(msg.Ns, soaRecord(zone, ttl))
			_ = w.WriteMsg(msg)
			return
		}

		switch q.Qtype {
		case dns.TypeA, dns.TypeANY:
			if ip.Is4() {
				msg.Answer = append(msg.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    ttl,
					},
					A: net.ParseIP(ip.String()),
				})
			}
		case dns.TypeAAAA:
			msg.Answer = append(msg.Answer, &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET,
					Ttl:    ttl,
				},
				AAAA: net.ParseIP("::1"),
			})
		default:
			msg.Ns = append(msg.Ns, soaRecord(zone, ttl))
		}
	}

	_ = w.WriteMsg(msg)
}

func soaRecord(zone string, ttl uint32) *dns.SOA {
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: ttl},
		Ns:      "ns1." + zone,
		Mbox:    "hostmaster." + zone,
		Serial:  1,
		Refresh: 60,
		Retry:   60,
		Expire:  600,
		Minttl:  1,
	}
}

// unresolvedNameLoopbackRR returns synthetic loopback answers for unknown names
// in managed zones to avoid transient negative-caching during startup races.
func unresolvedNameLoopbackRR(q dns.Question, ttl uint32) []dns.RR {
	switch q.Qtype {
	case dns.TypeA:
		return []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
			A:   net.ParseIP("127.0.0.1"),
		}}
	case dns.TypeAAAA:
		return []dns.RR{&dns.AAAA{
			Hdr:  dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: ttl},
			AAAA: net.ParseIP("::1"),
		}}
	case dns.TypeANY:
		return []dns.RR{
			&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: net.ParseIP("127.0.0.1")},
			&dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: ttl}, AAAA: net.ParseIP("::1")},
		}
	default:
		return nil
	}
}

func (s *Server) Summary() string {
	return fmt.Sprintf("zone=%s ttl=%d records=%d", s.table.Zone(), s.ttl, len(s.table.Snapshot()))
}
