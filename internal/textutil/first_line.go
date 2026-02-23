package textutil

import "strings"

func FirstLine(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	idx := strings.IndexByte(v, '\n')
	if idx < 0 {
		return v
	}
	return strings.TrimSpace(v[:idx])
}
