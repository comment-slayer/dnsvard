package macos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListManagedResolvers(t *testing.T) {
	tmp := t.TempDir()
	original := resolverDirPath
	resolverDirPath = tmp
	t.Cleanup(func() { resolverDirPath = original })

	spec := ResolverSpec{Domain: "cs", Nameserver: "127.0.0.1", Port: "1053"}
	if err := EnsureResolver(spec); err != nil {
		t.Fatalf("EnsureResolver returned error: %v", err)
	}

	managed, err := ListManagedResolvers()
	if err != nil {
		t.Fatalf("ListManagedResolvers returned error: %v", err)
	}
	if len(managed) != 1 || managed[0] != "cs" {
		t.Fatalf("managed resolvers = %#v", managed)
	}
}

func TestRemoveResolverRefusesUnmanagedFile(t *testing.T) {
	tmp := t.TempDir()
	original := resolverDirPath
	resolverDirPath = tmp
	t.Cleanup(func() { resolverDirPath = original })

	path := filepath.Join(tmp, "test")
	if err := os.WriteFile(path, []byte("nameserver 127.0.0.1\nport 1053\n"), 0o644); err != nil {
		t.Fatalf("write unmanaged resolver: %v", err)
	}

	err := RemoveResolver(ResolverSpec{Domain: "test"})
	if err == nil {
		t.Fatal("expected unmanaged resolver removal error")
	}
}
