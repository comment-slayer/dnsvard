package ownership

import (
	"os/user"
	"strings"
	"testing"
)

func TestSudoInvokerOwnershipHostileEnv(t *testing.T) {
	originalGetEUID := getEUIDFn
	originalLookup := lookupUserByIDFn

	getEUIDFn = func() int { return 0 }
	lookupUserByIDFn = func(uid string) (*user.User, error) {
		return &user.User{Uid: uid, Gid: "20"}, nil
	}
	t.Cleanup(func() {
		getEUIDFn = originalGetEUID
		lookupUserByIDFn = originalLookup
	})

	tests := []struct {
		name    string
		sudoUID string
		sudoGID string
		wantErr string
	}{
		{name: "invalid uid text", sudoUID: "abc", sudoGID: "20", wantErr: "parse SUDO_UID"},
		{name: "non-positive uid", sudoUID: "0", sudoGID: "20", wantErr: "uid must be > 0"},
		{name: "invalid gid text", sudoUID: "501", sudoGID: "oops", wantErr: "parse SUDO_GID"},
		{name: "non-positive gid", sudoUID: "501", sudoGID: "0", wantErr: "gid must be > 0"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUDO_UID", tc.sudoUID)
			t.Setenv("SUDO_GID", tc.sudoGID)
			_, _, _, err := sudoInvokerOwnership()
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
