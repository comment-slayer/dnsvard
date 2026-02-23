package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/comment-slayer/dnsvard/internal/procutil"
)

func PIDPath(stateDir string) string {
	return filepath.Join(stateDir, "daemon.pid")
}

func LogPath(stateDir string) string {
	return filepath.Join(stateDir, "daemon.log")
}

func WritePID(stateDir string, pid int) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	return os.WriteFile(PIDPath(stateDir), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

func RemovePID(stateDir string) error {
	err := os.Remove(PIDPath(stateDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ReadPID(stateDir string) (int, error) {
	b, err := os.ReadFile(PIDPath(stateDir))
	if err != nil {
		return 0, err
	}
	v := strings.TrimSpace(string(b))
	pid, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("parse pid file: %w", err)
	}
	return pid, nil
}

func ProcessRunning(pid int) bool {
	return procutil.Running(pid)
}
