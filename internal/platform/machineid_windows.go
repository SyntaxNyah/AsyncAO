//go:build windows

package platform

import (
	"os/exec"
	"os/user"
	"strings"
	"syscall"
)

// MachineIdentity returns the machine's stable identity values, strongest first; nil when none is readable. They are
// private: hwid hashes them, and nothing may send or log them.
// Windows: the account SID (what AO2-Client keys its HDID on) and the per-install MachineGuid; a failed read is skipped.
func MachineIdentity() []string {
	var out []string
	if u, err := user.Current(); err == nil {
		if sid := strings.TrimSpace(u.Uid); sid != "" {
			out = append(out, "sid="+sid) // on Windows, user.Uid is the SID string
		}
	}
	if g := machineGUID(); g != "" {
		out = append(out, "machineguid="+g)
	}
	return out
}

// machineGUID shells out to reg.exe for this one HKLM value (the package reads other registry
// values through syscall directly). Keep reading it exactly this way: the string feeds hwid and
// thus every user's ban id, so a change that altered the value would re-identify everyone.
func machineGUID() string {
	cmd := exec.Command("reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid")
	// HideWindow stops reg.exe flashing a console in a -H windowsgui build.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Matching line: "    MachineGuid    REG_SZ    <guid>".
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "REG_SZ"); i >= 0 {
			return strings.TrimSpace(line[i+len("REG_SZ"):])
		}
	}
	return ""
}
