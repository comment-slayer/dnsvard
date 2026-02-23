package ownership

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	getEUIDFn        = os.Geteuid
	lookupUserByIDFn = user.LookupId
)

func ChownPathToSudoInvoker(path string) error {
	uid, gid, ok, err := sudoInvokerOwnership()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("set owner on %s: %w", path, err)
	}
	return nil
}

func ChownPathAndParentToSudoInvoker(path string) error {
	uid, gid, ok, err := sudoInvokerOwnership()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	parent := filepath.Dir(path)
	if err := os.Chown(parent, uid, gid); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("set owner on %s: %w", parent, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("set owner on %s: %w", path, err)
	}
	return nil
}

func sudoInvokerOwnership() (int, int, bool, error) {
	if getEUIDFn() != 0 {
		return 0, 0, false, nil
	}
	uidStr := strings.TrimSpace(os.Getenv("SUDO_UID"))
	if uidStr == "" {
		return 0, 0, false, nil
	}
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse SUDO_UID %q: %w", uidStr, err)
	}
	if uid <= 0 {
		return 0, 0, false, fmt.Errorf("parse SUDO_UID %q: uid must be > 0", uidStr)
	}
	if gidStr := strings.TrimSpace(os.Getenv("SUDO_GID")); gidStr != "" {
		gid, parseErr := strconv.Atoi(gidStr)
		if parseErr != nil {
			return 0, 0, false, fmt.Errorf("parse SUDO_GID %q: %w", gidStr, parseErr)
		}
		if gid <= 0 {
			return 0, 0, false, fmt.Errorf("parse SUDO_GID %q: gid must be > 0", gidStr)
		}
		return uid, gid, true, nil
	}
	u, lookupErr := lookupUserByIDFn(uidStr)
	if lookupErr != nil {
		return 0, 0, false, lookupErr
	}
	gid, parseErr := strconv.Atoi(strings.TrimSpace(u.Gid))
	if parseErr != nil {
		return 0, 0, false, fmt.Errorf("parse sudo user gid %q: %w", u.Gid, parseErr)
	}
	if gid <= 0 {
		return 0, 0, false, fmt.Errorf("parse sudo user gid %q: gid must be > 0", u.Gid)
	}
	return uid, gid, true, nil
}
