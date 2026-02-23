package linux

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const userDaemonUnitName = "dev.dnsvard.daemon.service"

var runSystemctlUserStatusOutput = func(unit string) (string, error) {
	cmd := exec.Command("systemctl", "--user", "status", "--no-pager", "--lines=0", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("systemctl --user status %s: %w (%s)", unit, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func InstallOrUpdateUserDaemon(binaryPath string, configPath string, stateDir string, workingDir string) (string, error) {
	if strings.TrimSpace(binaryPath) == "" {
		return "", fmt.Errorf("binary path is required")
	}
	ctx, err := resolveSystemdUserContext()
	if err != nil {
		return "", err
	}
	home := ctx.Home
	targetUser := ctx.TargetUser
	targetUID := ctx.UID
	targetGID := ctx.GID
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return "", fmt.Errorf("create user systemd dir %s: %w", unitDir, err)
	}
	unitPath := filepath.Join(unitDir, userDaemonUnitName)
	unitContent := renderUserDaemonUnit(binaryPath, configPath, stateDir, workingDir)
	if err := os.WriteFile(unitPath, []byte(unitContent), 0o644); err != nil {
		return "", fmt.Errorf("write user daemon unit %s: %w", unitPath, err)
	}
	if targetUser != "" {
		if err := os.Chown(unitDir, targetUID, targetGID); err != nil {
			return "", fmt.Errorf("set owner on %s: %w", unitDir, err)
		}
		if err := os.Chown(unitPath, targetUID, targetGID); err != nil {
			return "", fmt.Errorf("set owner on %s: %w", unitPath, err)
		}
	}

	if err := runSystemctlUser("daemon-reload"); err != nil {
		return "", err
	}
	if err := runSystemctlUser("enable", "--now", userDaemonUnitName); err != nil {
		return "", err
	}
	return unitPath, nil
}

func StopUserDaemon() error {
	return runSystemctlUserAllowNoop("stop", userDaemonUnitName)
}

func UninstallUserDaemon() error {
	_ = runSystemctlUserAllowNoop("disable", "--now", userDaemonUnitName)
	ctx, err := resolveSystemdUserContext()
	if err != nil {
		return err
	}
	home := ctx.Home
	unitPath := filepath.Join(home, ".config", "systemd", "user", userDaemonUnitName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove user daemon unit %s: %w", unitPath, err)
	}
	if err := runSystemctlUserAllowNoop("daemon-reload"); err != nil {
		return err
	}
	return nil
}

func UserDaemonStatus() (string, error) {
	return runSystemctlUserStatusOutput(userDaemonUnitName)
}

func ActiveUserDaemonUnit() (string, error) {
	candidates := []string{userDaemonUnitName, "dnsvard.service"}
	for _, unit := range candidates {
		status, err := runSystemctlUserStatusOutput(unit)
		if err != nil {
			continue
		}
		if parsed := parseSystemdServiceUnit(status); parsed != "" {
			return parsed, nil
		}
		return unit, nil
	}
	return "", fmt.Errorf("dnsvard systemd user unit not active")
}

func parseSystemdServiceUnit(status string) string {
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "●")
		line = strings.TrimSpace(line)
		for _, field := range strings.Fields(line) {
			if strings.HasSuffix(strings.TrimSpace(field), ".service") {
				return strings.TrimSpace(field)
			}
		}
		break
	}
	return ""
}

func runSystemctlUser(args ...string) error {
	targetUser := strings.TrimSpace(os.Getenv("SUDO_USER"))
	full := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", full...)
	cmd.Env = append([]string{}, os.Environ()...)
	if os.Geteuid() == 0 && targetUser != "" {
		u, err := user.Lookup(targetUser)
		if err == nil {
			cmd = exec.Command("sudo", append([]string{"-u", targetUser, "systemctl"}, full...)...)
			cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR=/run/user/"+u.Uid)
		}
	} else if os.Geteuid() != 0 && os.Getenv("XDG_RUNTIME_DIR") == "" {
		if current, err := user.Current(); err == nil && strings.TrimSpace(current.Uid) != "" {
			cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR=/run/user/"+current.Uid)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w (%s)", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runSystemctlUserAllowNoop(args ...string) error {
	err := runSystemctlUser(args...)
	if err == nil {
		return nil
	}
	errText := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(errText, "failed to connect to bus") {
		return nil
	}
	if strings.Contains(errText, "unit ") && strings.Contains(errText, " not found") {
		return nil
	}
	if strings.Contains(errText, "could not be found") {
		return nil
	}
	return err
}

type systemdUserContext struct {
	Home       string
	TargetUser string
	UID        int
	GID        int
}

func resolveSystemdUserContext() (systemdUserContext, error) {
	targetUser := ""
	if os.Geteuid() == 0 {
		targetUser = strings.TrimSpace(os.Getenv("SUDO_USER"))
	}
	if targetUser == "" {
		home, err := os.UserHomeDir()
		return systemdUserContext{Home: home, UID: -1, GID: -1}, err
	}
	u, err := user.Lookup(targetUser)
	if err != nil {
		return systemdUserContext{}, fmt.Errorf("lookup sudo user %s: %w", targetUser, err)
	}
	uid, err := strconv.Atoi(strings.TrimSpace(u.Uid))
	if err != nil {
		return systemdUserContext{}, fmt.Errorf("parse uid for %s: %w", targetUser, err)
	}
	gid, err := strconv.Atoi(strings.TrimSpace(u.Gid))
	if err != nil {
		return systemdUserContext{}, fmt.Errorf("parse gid for %s: %w", targetUser, err)
	}
	return systemdUserContext{Home: u.HomeDir, TargetUser: targetUser, UID: uid, GID: gid}, nil
}

func renderUserDaemonUnit(binaryPath string, configPath string, stateDir string, workingDir string) string {
	args := []string{"daemon", "start", "--foreground"}
	if strings.TrimSpace(configPath) != "" {
		args = append([]string{"-c", configPath}, args...)
	}
	execStart := shellQuote(binaryPath)
	for _, arg := range args {
		execStart += " " + shellQuote(arg)
	}

	lines := []string{
		"[Unit]",
		"Description=dnsvard local routing daemon",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + execStart,
		"Restart=on-failure",
		"RestartSec=1",
	}
	if strings.TrimSpace(workingDir) != "" {
		lines = append(lines, "WorkingDirectory="+workingDir)
	}
	if strings.TrimSpace(stateDir) != "" {
		lines = append(lines, "Environment=DNSVARD_STATE_DIR="+stateDir)
	}
	lines = append(lines,
		"",
		"[Install]",
		"WantedBy=default.target",
	)
	return strings.Join(lines, "\n") + "\n"
}

func shellQuote(v string) string {
	if v == "" {
		return "''"
	}
	if !strings.ContainsAny(v, " \t\n'\"\\") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'"
}
