package macos

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const AgentLabel = "dev.dnsvard.daemon"
const LoopbackAgentLabel = "dev.dnsvard.loopback"

type LaunchAgentSpec struct {
	BinaryPath string
	ConfigPath string
	StateDir   string
	WorkingDir string
}

type LoopbackAgentSpec struct {
	BinaryPath        string
	StateFile         string
	ResolverStateFile string
	CIDR              string
}

func InstallOrUpdateLaunchAgent(spec LaunchAgentSpec) (string, error) {
	if err := ensureExecutableBinary(spec.BinaryPath); err != nil {
		return "", err
	}

	ctx, err := launchUserContext()
	if err != nil {
		return "", err
	}

	launchAgentsDir := filepath.Join(ctx.homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0o755); err != nil {
		return "", fmt.Errorf("create launchagents dir: %w", err)
	}
	if err := os.MkdirAll(spec.StateDir, 0o755); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}

	plistPath := launchAgentPath(ctx)
	plist := renderPlist(spec)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return "", fmt.Errorf("write launch agent plist: %w", err)
	}
	if os.Geteuid() == 0 && ctx.uid != 0 {
		if err := os.Chown(plistPath, ctx.uid, ctx.gid); err != nil {
			return "", fmt.Errorf("set launch agent ownership: %w", err)
		}
	}

	target := "gui/" + strconv.Itoa(ctx.uid)
	service := target + "/" + AgentLabel

	if isLaunchServiceLoaded(service) {
		if out, err := exec.Command("launchctl", "bootout", service).CombinedOutput(); err != nil {
			return "", fmt.Errorf("launchctl bootout failed: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		if err := exec.Command("launchctl", "bootstrap", target, plistPath).Run(); err != nil {
			return "", fmt.Errorf("launchctl bootstrap failed: %w", err)
		}
		if err := exec.Command("launchctl", "kickstart", service).Run(); err != nil {
			return "", fmt.Errorf("launchctl kickstart failed: %w", err)
		}
		return plistPath, nil
	}

	if err := exec.Command("launchctl", "bootstrap", target, plistPath).Run(); err != nil {
		return "", fmt.Errorf("launchctl bootstrap failed: %w", err)
	}
	if err := exec.Command("launchctl", "kickstart", service).Run(); err != nil {
		return "", fmt.Errorf("launchctl kickstart failed: %w", err)
	}

	return plistPath, nil
}

func StopLaunchAgent() error {
	ctx, err := launchUserContext()
	if err != nil {
		return err
	}
	service := "gui/" + strconv.Itoa(ctx.uid) + "/" + AgentLabel
	if err := exec.Command("launchctl", "bootout", service).Run(); err != nil {
		return fmt.Errorf("launchctl bootout failed: %w", err)
	}
	return nil
}

func UninstallLaunchAgent() error {
	ctx, err := launchUserContext()
	if err != nil {
		return err
	}
	service := "gui/" + strconv.Itoa(ctx.uid) + "/" + AgentLabel
	_ = exec.Command("launchctl", "bootout", service).Run()
	if err := os.Remove(launchAgentPath(ctx)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove launch agent plist failed: %w", err)
	}
	return nil
}

func LaunchAgentStatus() (string, error) {
	ctx, err := launchUserContext()
	if err != nil {
		return "", err
	}
	service := "gui/" + strconv.Itoa(ctx.uid) + "/" + AgentLabel
	cmd := exec.Command("launchctl", "print", service)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return "", fmt.Errorf("%s", trimmed)
	}
	return string(out), nil
}

func LoopbackAgentStatus() (string, error) {
	service := "system/" + LoopbackAgentLabel
	cmd := exec.Command("launchctl", "print", service)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return "", fmt.Errorf("%s", trimmed)
	}
	return string(out), nil
}

func launchAgentPath(ctx userContext) string {
	return filepath.Join(ctx.homeDir, "Library", "LaunchAgents", AgentLabel+".plist")
}

func renderPlist(spec LaunchAgentSpec) string {
	args := []string{spec.BinaryPath}
	if strings.TrimSpace(spec.ConfigPath) != "" {
		args = append(args, "-c", spec.ConfigPath)
	}
	args = append(args, "daemon", "start", "--foreground")

	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")
	b.WriteString("  <key>Label</key>\n")
	b.WriteString("  <string>" + AgentLabel + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range args {
		b.WriteString("    <string>" + xmlEscape(arg) + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n")
	b.WriteString("  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n")
	b.WriteString("  <true/>\n")
	b.WriteString("  <key>StandardOutPath</key>\n")
	b.WriteString("  <string>" + xmlEscape(filepath.Join(spec.StateDir, "daemon.log")) + "</string>\n")
	b.WriteString("  <key>StandardErrorPath</key>\n")
	b.WriteString("  <string>" + xmlEscape(filepath.Join(spec.StateDir, "daemon.log")) + "</string>\n")
	if strings.TrimSpace(spec.WorkingDir) != "" {
		b.WriteString("  <key>WorkingDirectory</key>\n")
		b.WriteString("  <string>" + xmlEscape(spec.WorkingDir) + "</string>\n")
	}
	b.WriteString("  <key>EnvironmentVariables</key>\n")
	b.WriteString("  <dict>\n")
	b.WriteString("    <key>PATH</key>\n")
	b.WriteString("    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>\n")
	b.WriteString("  </dict>\n")
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func mustHomeDir() string {
	u, err := user.Current()
	if err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	return "."
}

type userContext struct {
	uid     int
	gid     int
	homeDir string
}

func launchUserContext() (userContext, error) {
	if os.Geteuid() == 0 {
		sudoUID := strings.TrimSpace(os.Getenv("SUDO_UID"))
		sudoGID := strings.TrimSpace(os.Getenv("SUDO_GID"))
		if sudoUID != "" {
			uid, err := strconv.Atoi(sudoUID)
			if err != nil {
				return userContext{}, fmt.Errorf("parse SUDO_UID: %w", err)
			}
			gid := 0
			if sudoGID != "" {
				if parsedGID, err := strconv.Atoi(sudoGID); err == nil {
					gid = parsedGID
				}
			}
			u, err := user.LookupId(sudoUID)
			if err != nil {
				return userContext{}, fmt.Errorf("lookup sudo user %s: %w", sudoUID, err)
			}
			if gid == 0 {
				if parsedGID, err := strconv.Atoi(u.Gid); err == nil {
					gid = parsedGID
				}
			}
			return userContext{uid: uid, gid: gid, homeDir: u.HomeDir}, nil
		}
	}

	home := mustHomeDir()
	return userContext{uid: os.Getuid(), gid: os.Getgid(), homeDir: home}, nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func isLaunchServiceLoaded(service string) bool {
	return exec.Command("launchctl", "print", service).Run() == nil
}

func InstallOrUpdateLoopbackAgent(spec LoopbackAgentSpec) (string, error) {
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("installing system loopback agent requires sudo")
	}
	if err := ensureExecutableBinary(spec.BinaryPath); err != nil {
		return "", err
	}

	plistDir := "/Library/LaunchDaemons"
	plistPath := filepath.Join(plistDir, LoopbackAgentLabel+".plist")
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", plistDir, err)
	}

	plist := renderLoopbackPlist(spec)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return "", fmt.Errorf("write loopback plist: %w", err)
	}

	service := "system/" + LoopbackAgentLabel
	target := "system"

	if isLaunchServiceLoaded(service) {
		if err := launchctlBootoutBestEffort(service, target, plistPath); err != nil {
			return "", fmt.Errorf("launchctl bootout loopback agent failed: %w", err)
		}
	}
	_ = exec.Command("launchctl", "enable", service).Run()

	if err := launchctlBootstrapWithRetry("loopback agent", target, service, plistPath); err != nil {
		return "", err
	}
	if err := exec.Command("launchctl", "kickstart", "-k", service).Run(); err != nil {
		return "", fmt.Errorf("launchctl kickstart loopback agent failed: %w", err)
	}

	return plistPath, nil
}

func ensureExecutableBinary(path string) error {
	binaryPath := filepath.Clean(strings.TrimSpace(path))
	if binaryPath == "" {
		return fmt.Errorf("binary path is required")
	}
	st, err := os.Stat(binaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("binary path does not exist: %s", binaryPath)
		}
		return fmt.Errorf("read binary path %s: %w", binaryPath, err)
	}
	if st.IsDir() {
		return fmt.Errorf("binary path is a directory, expected executable file: %s", binaryPath)
	}
	if st.Mode()&0o111 == 0 {
		return fmt.Errorf("binary path is not executable: %s", binaryPath)
	}
	return nil
}

func launchctlBootstrapWithRetry(label string, target string, service string, plistPath string) error {
	out, err := exec.Command("launchctl", "bootstrap", target, plistPath).CombinedOutput()
	if err == nil {
		return nil
	}
	firstErr := strings.TrimSpace(string(out))

	_ = launchctlBootoutBestEffort(service, target, plistPath)
	outRetry, retryErr := exec.Command("launchctl", "bootstrap", target, plistPath).CombinedOutput()
	if retryErr == nil {
		return nil
	}
	retryText := strings.TrimSpace(string(outRetry))

	msg := fmt.Sprintf("launchctl bootstrap %s failed: %v (%s)", label, retryErr, retryText)
	if firstErr != "" && firstErr != retryText {
		msg += fmt.Sprintf("; first failure: %s", firstErr)
	}
	msg += fmt.Sprintf("\nfix: sudo launchctl bootout %s || true", service)
	msg += fmt.Sprintf("\nfix: sudo launchctl bootout %s %s || true", target, plistPath)
	msg += fmt.Sprintf("\nfix: sudo launchctl bootstrap %s %s", target, plistPath)
	return fmt.Errorf("%s", msg)
}

func launchctlBootoutBestEffort(service string, target string, plistPath string) error {
	if out, err := exec.Command("launchctl", "bootout", service).CombinedOutput(); err != nil {
		if !isLaunchctlBootoutIgnorableError(out, err) {
			return fmt.Errorf("service %s: %w (%s)", service, err, strings.TrimSpace(string(out)))
		}
	}

	if strings.TrimSpace(target) == "" || strings.TrimSpace(plistPath) == "" {
		return nil
	}
	if out, err := exec.Command("launchctl", "bootout", target, plistPath).CombinedOutput(); err != nil {
		if !isLaunchctlBootoutIgnorableError(out, err) {
			return fmt.Errorf("domain %s plist %s: %w (%s)", target, plistPath, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func isLaunchctlBootoutIgnorableError(out []byte, err error) bool {
	combined := strings.ToLower(strings.TrimSpace(string(out) + " " + err.Error()))
	if strings.Contains(combined, "could not find service") {
		return true
	}
	if strings.Contains(combined, "no such process") {
		return true
	}
	if strings.Contains(combined, "service not loaded") {
		return true
	}
	if strings.Contains(combined, "not loaded") {
		return true
	}
	if strings.Contains(combined, "input/output error") {
		return true
	}
	return false
}

func UninstallLoopbackAgent() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstalling system loopback agent requires sudo")
	}
	service := "system/" + LoopbackAgentLabel
	_ = exec.Command("launchctl", "bootout", service).Run()
	plistPath := filepath.Join("/Library/LaunchDaemons", LoopbackAgentLabel+".plist")
	if err := os.Remove(plistPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove loopback plist failed: %w", err)
	}
	return nil
}

func renderLoopbackPlist(spec LoopbackAgentSpec) string {
	args := []string{spec.BinaryPath, "daemon", "loopback-sync", "--state-file", spec.StateFile, "--cidr", spec.CIDR}
	if strings.TrimSpace(spec.ResolverStateFile) != "" {
		args = append(args, "--resolver-state-file", spec.ResolverStateFile)
	}

	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")
	b.WriteString("  <key>Label</key>\n")
	b.WriteString("  <string>" + LoopbackAgentLabel + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range args {
		b.WriteString("    <string>" + xmlEscape(arg) + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n")
	b.WriteString("  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n")
	b.WriteString("  <true/>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n")
	b.WriteString("  <dict>\n")
	b.WriteString("    <key>PATH</key>\n")
	b.WriteString("    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>\n")
	b.WriteString("  </dict>\n")
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}
