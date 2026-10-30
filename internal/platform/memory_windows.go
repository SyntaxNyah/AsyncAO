package platform

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx is Win32's MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func totalMemory() (uint64, bool) {
	status := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	if ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 0, false
	}
	return status.totalPhys, true
}
