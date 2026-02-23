package routes

import (
	"net/netip"
	"testing"
)

func TestMergeHonorsSourcePrecedence(t *testing.T) {
	t.Parallel()

	runtimeIP := netip.MustParseAddr("127.90.0.10")
	dockerIP := netip.MustParseAddr("127.90.0.11")

	result := Merge(
		Entry{Hostname: "api.master.project.test", IP: dockerIP, Source: SourceDocker},
		Entry{Hostname: "api.master.project.test", IP: runtimeIP, Source: SourceRuntime},
	)

	if len(result.Entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(result.Entries))
	}
	if result.Entries[0].IP != runtimeIP {
		t.Fatalf("entry ip = %s, want %s", result.Entries[0].IP, runtimeIP)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warning for override")
	}
}
