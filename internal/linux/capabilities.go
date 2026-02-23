package linux

import "os/exec"

type Capabilities struct {
	Systemctl       bool
	SystemdResolved bool
	IPTool          bool
	Dnsmasq         bool
}

func DetectCapabilities() Capabilities {
	return Capabilities{
		Systemctl:       hasCommand("systemctl"),
		SystemdResolved: hasCommand("resolvectl"),
		IPTool:          hasCommand("ip"),
		Dnsmasq:         hasCommand("dnsmasq"),
	}
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
