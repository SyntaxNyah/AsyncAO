package platform

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

const (
	// internetSettingsKey holds the configuration the Windows proxy UI writes, for the current user.
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	// regValueMaxBytes caps one registry read, as these values are externally controlled (hard rule 4).
	regValueMaxBytes  = 32 << 10
	windowsProxyName  = "Windows proxy settings"
	utf16BytesPerUnit = 2
	bitsPerByte       = 8
	// connectionsSubkey holds DefaultConnectionSettings, whose flags DWORD at pacFlagsOffset says
	// which proxy modes are on; pacEnabledFlag (PROXY_TYPE_AUTO_PROXY_URL) marks the auto-config script.
	connectionsSubkey   = "Connections"
	defaultConnSettings = "DefaultConnectionSettings"
	pacFlagsOffset      = 8
	pacFlagBytes        = 4
	pacEnabledFlag      = 0x04
)

// SystemProxySettings reads the registry. Automatic detection alone is ignored: it is on by default on machines with no
// proxy at all, so treating it as configured would strand them.
func SystemProxySettings() ProxySettings {
	var key syscall.Handle
	path, err := syscall.UTF16PtrFromString(internetSettingsKey)
	if err != nil {
		return ProxySettings{}
	}
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, path, 0, syscall.KEY_QUERY_VALUE, &key); err != nil {
		return ProxySettings{}
	}
	defer syscall.RegCloseKey(key)

	settings := ProxySettings{Bypass: regString(key, "ProxyOverride")}
	if regDWORD(key, "ProxyEnable") != 0 {
		settings.Server = regString(key, "ProxyServer")
		settings.Source = windowsProxyName
	}
	// Honor the auto-config (PAC) URL only when its flag is set: a leftover AutoConfigURL from a
	// since-disabled script must not pass as configured and block every connection.
	if autoConfigEnabled(key) {
		settings.AutoConfigURL = regString(key, "AutoConfigURL")
	}
	return settings
}

// autoConfigEnabled reports whether the PAC flag is set in the DefaultConnectionSettings blob,
// so an AutoConfigURL left behind by a since-disabled script is ignored rather than honored.
func autoConfigEnabled(parent syscall.Handle) bool {
	name, err := syscall.UTF16PtrFromString(connectionsSubkey)
	if err != nil {
		return false
	}
	var conn syscall.Handle
	if err := syscall.RegOpenKeyEx(parent, name, 0, syscall.KEY_QUERY_VALUE, &conn); err != nil {
		return false
	}
	defer syscall.RegCloseKey(conn)
	return pacURLEnabled(regBinary(conn, defaultConnSettings))
}

// pacURLEnabled reads the PAC flag out of a DefaultConnectionSettings blob; false for a short or
// missing blob, so an AutoConfigURL is honored only when the blob positively confirms it.
func pacURLEnabled(blob []byte) bool {
	if len(blob) < pacFlagsOffset+pacFlagBytes {
		return false
	}
	flags := binary.LittleEndian.Uint32(blob[pacFlagsOffset : pacFlagsOffset+pacFlagBytes])
	return flags&pacEnabledFlag != 0
}

// regBinary reads a REG_BINARY value, nil when absent or of another type.
func regBinary(key syscall.Handle, name string) []byte {
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil
	}
	var kind uint32
	size := uint32(regValueMaxBytes)
	buf := make([]byte, regValueMaxBytes)
	if err := syscall.RegQueryValueEx(key, value, nil, &kind, &buf[0], &size); err != nil {
		return nil
	}
	if kind != syscall.REG_BINARY {
		return nil
	}
	return buf[:size]
}

// regString reads a text value, "" when absent, the normal case on a machine with no proxy.
func regString(key syscall.Handle, name string) string {
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	var kind uint32
	size := uint32(regValueMaxBytes)
	buf := make([]byte, regValueMaxBytes)
	if err := syscall.RegQueryValueEx(key, value, nil, &kind, &buf[0], &size); err != nil {
		return ""
	}
	if kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ {
		return ""
	}
	return utf16BytesToString(buf[:size])
}

func regDWORD(key syscall.Handle, name string) uint32 {
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	var kind, out uint32
	size := uint32(unsafe.Sizeof(out))
	if err := syscall.RegQueryValueEx(key, value, nil, &kind, (*byte)(unsafe.Pointer(&out)), &size); err != nil {
		return 0
	}
	if kind != syscall.REG_DWORD {
		return 0
	}
	return out
}

// utf16BytesToString decodes by hand because []byte alignment isn't guaranteed for a []uint16 cast.
func utf16BytesToString(b []byte) string {
	units := make([]uint16, 0, len(b)/utf16BytesPerUnit)
	for i := 0; i+1 < len(b); i += utf16BytesPerUnit {
		unit := uint16(b[i]) | uint16(b[i+1])<<bitsPerByte
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return syscall.UTF16ToString(units)
}
