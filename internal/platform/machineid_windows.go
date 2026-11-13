//go:build windows

package platform

import (
	"os/user"
	"strings"
	"syscall"
)

const (
	cryptographyKey  = `SOFTWARE\Microsoft\Cryptography`
	machineGUIDValue = "MachineGuid"
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

// machineGUID reads MachineGuid from the 64-bit registry view, the one reg.exe reported when it was read that way. Keep
// the value exactly as read: it feeds hwid and thus every user's ban id, so a changed string would re-identify everyone.
func machineGUID() string {
	path, err := syscall.UTF16PtrFromString(cryptographyKey)
	if err != nil {
		return ""
	}
	var key syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, path, 0, syscall.KEY_QUERY_VALUE|syscall.KEY_WOW64_64KEY, &key) != nil {
		return ""
	}
	defer func() { _ = syscall.RegCloseKey(key) }()
	return strings.TrimSpace(regString(key, machineGUIDValue))
}
