package main

import (
	"path/filepath"
	"strings"
)

func isGoRunExecutable(path string) bool {
	clean := filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	if clean == "" {
		return false
	}
	if strings.Contains(clean, "/go-build") && strings.Contains(clean, "/exe/") {
		return true
	}
	if strings.Contains(clean, "/tmp/go-build") {
		return true
	}
	if strings.Contains(clean, "/var/folders/") && strings.Contains(clean, "go-build") {
		return true
	}
	return false
}
