package dnsserver

import (
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestServerAnswersAAndLoopbackForUnknown(t *testing.T) {
	t.Parallel()

	table := NewTable("test")
	ip := netip.MustParseAddr("127.90.1.10")
	if err := table.Set([]Record{{Hostname: "api.master.comment-slayer.test", IP: ip}}); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}

	srv := New("127.0.0.1:0", table, 5)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	addr := srv.udp.PacketConn.LocalAddr().String()
	client := &dns.Client{Timeout: 2 * time.Second}

	q1 := new(dns.Msg)
	q1.SetQuestion("api.master.comment-slayer.test.", dns.TypeA)
	r1, _, err := client.Exchange(q1, addr)
	if err != nil {
		t.Fatalf("Exchange A returned error: %v", err)
	}
	if r1.Rcode != dns.RcodeSuccess {
		t.Fatalf("A rcode = %d", r1.Rcode)
	}
	if len(r1.Answer) != 1 {
		t.Fatalf("A answers len = %d, want 1", len(r1.Answer))
	}

	q2 := new(dns.Msg)
	q2.SetQuestion("missing.master.comment-slayer.test.", dns.TypeA)
	r2, _, err := client.Exchange(q2, addr)
	if err != nil {
		t.Fatalf("Exchange unknown-name returned error: %v", err)
	}
	if r2.Rcode != dns.RcodeSuccess {
		t.Fatalf("unknown-name rcode = %d, want %d", r2.Rcode, dns.RcodeSuccess)
	}
	if len(r2.Answer) != 1 {
		t.Fatalf("unknown-name answers len = %d, want 1", len(r2.Answer))
	}
}

func TestServerRefusesOutsideManagedDomains(t *testing.T) {
	t.Parallel()

	table := NewTable(".")
	table.SetManagedZones([]string{"test"})

	srv := New("127.0.0.1:0", table, 5)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	addr := srv.udp.PacketConn.LocalAddr().String()
	client := &dns.Client{Timeout: 2 * time.Second}

	outside := new(dns.Msg)
	outside.SetQuestion("google.com.", dns.TypeA)
	rOutside, _, err := client.Exchange(outside, addr)
	if err != nil {
		t.Fatalf("Exchange outside-domain returned error: %v", err)
	}
	if rOutside.Rcode != dns.RcodeRefused {
		t.Fatalf("outside-domain rcode = %d, want %d", rOutside.Rcode, dns.RcodeRefused)
	}

	inside := new(dns.Msg)
	inside.SetQuestion("missing.test.", dns.TypeA)
	rInside, _, err := client.Exchange(inside, addr)
	if err != nil {
		t.Fatalf("Exchange inside-domain returned error: %v", err)
	}
	if rInside.Rcode != dns.RcodeSuccess {
		t.Fatalf("inside-domain rcode = %d, want %d", rInside.Rcode, dns.RcodeSuccess)
	}
	if len(rInside.Answer) != 1 {
		t.Fatalf("inside-domain answers len = %d, want 1", len(rInside.Answer))
	}
}
