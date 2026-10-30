package platform

import "syscall"

// machineModel reports macOS's own answer, since a guest has no vendor string to
// read: kern.hv_vm_active is non-zero inside a virtual machine.
func machineModel() string {
	active, err := syscall.Sysctl("kern.hv_vm_active")
	if err != nil || active == "" || active[0] == 0 {
		return ""
	}
	return genericVirtualMachine
}
