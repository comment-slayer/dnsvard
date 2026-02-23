package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

type localNetworkStatus string

const (
	localNetworkOK      localNetworkStatus = "ok"
	localNetworkDenied  localNetworkStatus = "denied"
	localNetworkUnknown localNetworkStatus = "unknown"
)

func probeLocalNetworkAccess(timeout time.Duration) (localNetworkStatus, string) {
	targets := []string{
		"192.168.0.1:80",
		"10.0.0.1:80",
		"172.16.0.1:80",
	}

	lastDetail := "probe inconclusive"
	for _, target := range targets {
		conn, err := (&net.Dialer{Timeout: timeout}).Dial("tcp", target)
		if err == nil {
			_ = conn.Close()
			return localNetworkOK, "probe dial succeeded"
		}
		status, detail := classifyLocalNetworkProbeError(err)
		if status == localNetworkDenied {
			return status, fmt.Sprintf("%s (%s)", detail, target)
		}
		lastDetail = fmt.Sprintf("%s (%s)", detail, target)
	}

	return localNetworkUnknown, lastDetail
}

func classifyLocalNetworkProbeError(err error) (localNetworkStatus, string) {
	if err == nil {
		return localNetworkOK, "probe dial succeeded"
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errors.Is(sysErr.Err, syscall.EPERM) || errors.Is(sysErr.Err, syscall.EACCES) {
				return localNetworkDenied, "permission denied"
			}
		}
	}

	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "operation not permitted") || strings.Contains(lower, "permission denied") {
		return localNetworkDenied, "permission denied"
	}

	return localNetworkUnknown, strings.TrimSpace(err.Error())
}
