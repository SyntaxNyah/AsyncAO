package platform

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	biosRegistryKey = `HARDWARE\DESCRIPTION\System\BIOS`
	// registryValueChars bounds one BIOS string; longer ones are not read.
	registryValueChars = 256
)

func machineModel() string {
	keyName, err := syscall.UTF16PtrFromString(biosRegistryKey)
	if err != nil {
		return ""
	}
	var key syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, keyName, 0, syscall.KEY_READ, &key) != nil {
		return ""
	}
	defer func() { _ = syscall.RegCloseKey(key) }()
	return strings.Join([]string{biosValue(key, "SystemManufacturer"), biosValue(key, "SystemProductName")}, " ")
}

func biosValue(key syscall.Handle, name string) string {
	valueName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	var value [registryValueChars]uint16
	size := uint32(unsafe.Sizeof(value))
	if syscall.RegQueryValueEx(key, valueName, nil, nil, (*byte)(unsafe.Pointer(&value[0])), &size) != nil {
		return ""
	}
	return syscall.UTF16ToString(value[:])
}
