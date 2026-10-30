package platform

import "syscall"

func totalMemory() (uint64, bool) {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0, false
	}
	return uint64(info.Totalram) * uint64(info.Unit), true
}
