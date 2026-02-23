//go:build darwin || linux

package main

import (
	"os/exec"
	"syscall"
)

func killPID(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroupImpl(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}
