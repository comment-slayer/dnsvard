package logx

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	*slog.Logger
}

func New(level string) (*Logger, error) {
	var l slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		l = slog.LevelDebug
	case "info", "":
		l = slog.LevelInfo
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})
	return &Logger{Logger: slog.New(h)}, nil
}
