package dnsserver

import (
	"net/netip"
	"testing"
)

func TestTableSetAndLookup(t *testing.T) {
	t.Parallel()

	table := NewTable("test")
	ip := netip.MustParseAddr("127.90.1.10")
	if err := table.Set([]Record{{Hostname: "api.master.comment-slayer.test", IP: ip}}); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}

	got, ok := table.Lookup("api.master.comment-slayer.test.")
	if !ok {
		t.Fatal("Lookup returned !ok")
	}
	if got != ip {
		t.Fatalf("Lookup ip = %s want %s", got, ip)
	}
}

func TestTableRejectsDuplicateHostnameDifferentIP(t *testing.T) {
	t.Parallel()

	table := NewTable("test")
	err := table.Set([]Record{
		{Hostname: "api.master.comment-slayer.test", IP: netip.MustParseAddr("127.90.1.10")},
		{Hostname: "api.master.comment-slayer.test", IP: netip.MustParseAddr("127.90.1.11")},
	})
	if err == nil {
		t.Fatal("expected error for duplicate hostname with different ips")
	}
}

func TestTableSetZoneReplacesZoneAndClearsRecords(t *testing.T) {
	t.Parallel()

	table := NewTable("test")
	ip := netip.MustParseAddr("127.90.1.10")
	if err := table.Set([]Record{{Hostname: "master.cool-name.test", IP: ip}}); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}

	table.SetZone("cs")
	if table.Zone() != "cs." {
		t.Fatalf("Zone = %q", table.Zone())
	}
	if _, ok := table.Lookup("master.cool-name.test"); ok {
		t.Fatal("expected records to be cleared on zone change")
	}
	if err := table.Set([]Record{{Hostname: "master.cool-name.cs", IP: ip}}); err != nil {
		t.Fatalf("Set after SetZone returned error: %v", err)
	}
}

func TestTableManagedZonesAllowLookupScope(t *testing.T) {
	t.Parallel()

	table := NewTable(".")
	table.SetManagedZones([]string{"test", "internal"})

	if !table.Allows("api.foo.test") {
		t.Fatal("expected api.foo.test to be allowed")
	}
	if !table.Allows("svc.internal") {
		t.Fatal("expected svc.internal to be allowed")
	}
	if table.Allows("google.com") {
		t.Fatal("expected google.com to be refused")
	}
	if got := table.ZoneForName("svc.internal"); got != "internal." {
		t.Fatalf("ZoneForName = %q, want %q", got, "internal.")
	}
}
