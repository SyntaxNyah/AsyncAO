package platform

import "syscall"

// machineModel uses kern.hv_vm_active, because a macOS guest has no vendor string.
func machineModel() string {
	active, err := syscall.Sysctl("kern.hv_vm_active")
	if err != nil || active == "" || active[0] == 0 {
		return ""
	}
	return genericVirtualMachine
}
