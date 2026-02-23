package main

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestClassifyLocalNetworkProbeErrorDeniedBySyscall(t *testing.T) {
	t.Parallel()

	err := &net.OpError{Err: &os.SyscallError{Err: syscall.EPERM}}
	status, detail := classifyLocalNetworkProbeError(err)
	if status != localNetworkDenied {
		t.Fatalf("status = %q", status)
	}
	if detail == "" {
		t.Fatal("expected detail")
	}
}

func TestClassifyLocalNetworkProbeErrorDeniedByMessage(t *testing.T) {
	t.Parallel()

	status, _ := classifyLocalNetworkProbeError(errors.New("operation not permitted"))
	if status != localNetworkDenied {
		t.Fatalf("status = %q", status)
	}
}

func TestClassifyLocalNetworkProbeErrorUnknown(t *testing.T) {
	t.Parallel()

	status, _ := classifyLocalNetworkProbeError(errors.New("connection timed out"))
	if status != localNetworkUnknown {
		t.Fatalf("status = %q", status)
	}
}
