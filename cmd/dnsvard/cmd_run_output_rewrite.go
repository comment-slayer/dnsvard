package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var devServerLoopbackURLPattern = regexp.MustCompile(`http://(?:127\.0\.0\.1|localhost|\[::1\]):([0-9]{1,5})(/[^\s]*)?`)

func extendViteAllowedHostsEnv(env []string, publicHost string) []string {
	viteAdditionalAllowedHost := publicHost
	if existingAllowedHosts := os.Getenv("__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS"); strings.TrimSpace(existingAllowedHosts) != "" {
		viteAdditionalAllowedHost = existingAllowedHosts + "," + publicHost
	}
	return append(env, fmt.Sprintf("__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=%s", viteAdditionalAllowedHost))
}

type lineRewriteWriter struct {
	dst     io.Writer
	rewrite func(string) string

	mu  sync.Mutex
	buf []byte
}

func newLineRewriteWriter(dst io.Writer, rewrite func(string) string) *lineRewriteWriter {
	return &lineRewriteWriter{dst: dst, rewrite: rewrite}
}

func (w *lineRewriteWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	for {
		nl := bytes.IndexByte(w.buf, '\n')
		if nl < 0 {
			break
		}
		line := string(w.buf[:nl])
		rewritten := w.rewrite(line) + "\n"
		if _, err := io.WriteString(w.dst, rewritten); err != nil {
			return 0, err
		}
		w.buf = w.buf[nl+1:]
	}

	return len(p), nil
}

func (w *lineRewriteWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.buf) == 0 {
		return nil
	}
	_, err := io.WriteString(w.dst, w.rewrite(string(w.buf)))
	w.buf = w.buf[:0]
	return err
}

func rewriteDevServerBannerLine(line string, runtimePort int, publicURL string) string {
	if !strings.Contains(line, "Local") && !strings.Contains(line, "Network") {
		return line
	}

	trimmedPublicURL := strings.TrimRight(publicURL, "/")
	publicWithPort := trimmedPublicURL + ":" + strconv.Itoa(runtimePort)
	line = strings.ReplaceAll(line, publicWithPort+"/", trimmedPublicURL+"/")
	line = strings.ReplaceAll(line, publicWithPort, trimmedPublicURL)

	return devServerLoopbackURLPattern.ReplaceAllStringFunc(line, func(match string) string {
		port := extractURLPort(match)
		if port != runtimePort {
			return match
		}

		suffix := "/"
		if tail := match[len("http://"):]; strings.Contains(tail, "/") {
			i := strings.Index(tail, "/")
			suffix = tail[i:]
		}
		return trimmedPublicURL + suffix
	})
}

func extractURLPort(v string) int {
	marker := strings.LastIndex(v, ":")
	if marker < 0 {
		return 0
	}
	rest := v[marker+1:]
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	p, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return p
}
