//go:build windows

package hwid

import (
	"os/exec"
	"os/user"
	"strings"
	"syscall"
)

// roots returns Windows identity roots, strongest first: the per-account SID
// (what AO2-Client keys its HDID on) and the per-OS-install MachineGuid. Neither
// is exposed in any settings UI; a SID changes only with a new Windows account,
// MachineGuid only with an OS reinstall. Each is best-effort — a read failure is
// skipped, never fatal (compute() uses the hostname only if BOTH miss).
func roots() []string {
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

// machineGUID reads HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid via reg.exe.
// The standard library has no registry access and we deliberately avoid adding a
// dependency for one value; reg.exe is a signed in-box tool, run once per connect
// (cold path). A missing or blocked reg.exe simply drops this root.
func machineGUID() string {
	cmd := exec.Command("reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid")
	// HideWindow stops reg.exe flashing a console window when AsyncAO is linked as
	// a GUI app (-H windowsgui); harmless on a console build.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// A matching line looks like: "    MachineGuid    REG_SZ    <guid>".
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "REG_SZ"); i >= 0 {
			return strings.TrimSpace(line[i+len("REG_SZ"):])
		}
	}
	return ""
}
