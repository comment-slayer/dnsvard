package linux

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSystemdServiceUnit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wanted string
	}{
		{name: "bullet status line", input: "● dev.dnsvard.daemon.service - dnsvard local routing daemon", wanted: "dev.dnsvard.daemon.service"},
		{name: "plain status line", input: "dnsvard.service - dnsvard local routing daemon", wanted: "dnsvard.service"},
		{name: "empty", input: "", wanted: ""},
		{name: "no unit token", input: "dnsvard local routing daemon", wanted: ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseSystemdServiceUnit(tc.input); got != tc.wanted {
				t.Fatalf("parseSystemdServiceUnit() = %q, want %q", got, tc.wanted)
			}
		})
	}
}

func TestActiveUserDaemonUnitSelection(t *testing.T) {
	original := runSystemctlUserStatusOutput
	t.Cleanup(func() {
		runSystemctlUserStatusOutput = original
	})

	runSystemctlUserStatusOutput = func(unit string) (string, error) {
		switch unit {
		case userDaemonUnitName:
			return "● dev.dnsvard.daemon.service - dnsvard local routing daemon\n", nil
		default:
			return "", fmt.Errorf("not found")
		}
	}

	unit, err := ActiveUserDaemonUnit()
	if err != nil {
		t.Fatalf("ActiveUserDaemonUnit returned error: %v", err)
	}
	if unit != userDaemonUnitName {
		t.Fatalf("unit = %q, want %q", unit, userDaemonUnitName)
	}

	runSystemctlUserStatusOutput = func(unit string) (string, error) {
		if unit == "dnsvard.service" {
			return "dnsvard.service - dnsvard local routing daemon\n", nil
		}
		return "", fmt.Errorf("not found")
	}

	unit, err = ActiveUserDaemonUnit()
	if err != nil {
		t.Fatalf("ActiveUserDaemonUnit fallback returned error: %v", err)
	}
	if unit != "dnsvard.service" {
		t.Fatalf("fallback unit = %q, want dnsvard.service", unit)
	}

	runSystemctlUserStatusOutput = func(string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	_, err = ActiveUserDaemonUnit()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not active") {
		t.Fatalf("expected not active error, got: %v", err)
	}
}
