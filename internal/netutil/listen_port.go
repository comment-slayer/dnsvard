package netutil

import (
	"net"
	"strconv"
	"strings"
)

func ParseListenPort(listen string) (int, bool) {
	trimmed := strings.TrimSpace(listen)
	if trimmed == "" {
		return 0, false
	}
	if strings.HasPrefix(trimmed, ":") {
		v, err := strconv.Atoi(strings.TrimPrefix(trimmed, ":"))
		if err != nil || v <= 0 {
			return 0, false
		}
		return v, true
	}
	_, portStr, err := net.SplitHostPort(trimmed)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}
