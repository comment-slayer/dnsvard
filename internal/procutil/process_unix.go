//go:build darwin || linux

package procutil

import "syscall"

func Running(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
