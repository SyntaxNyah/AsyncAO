package platform

import (
	"os"
	"runtime"
)

// Probe is how detection reads the machine, so tests can describe any platform.
type Probe interface {
	GOOS() string
	GOARCH() string
	Getenv(key string) string
	// Executable returns the running binary's path.
	Executable() (string, error)
	PathExists(path string) bool
	CPUCount() int
	// TotalMemory returns the machine's physical memory in bytes, false when unknown.
	TotalMemory() (uint64, bool)
}

// SystemProbe reads the machine the process runs on.
type SystemProbe struct{}

var _ Probe = SystemProbe{}

// GOOS returns the operating system the binary was built for.
func (SystemProbe) GOOS() string { return runtime.GOOS }

// GOARCH returns the architecture the binary was built for.
func (SystemProbe) GOARCH() string { return runtime.GOARCH }

// Getenv returns an environment variable, empty when unset.
func (SystemProbe) Getenv(key string) string { return os.Getenv(key) }

// Executable returns the running binary's path.
func (SystemProbe) Executable() (string, error) { return os.Executable() }

// CPUCount returns the number of logical CPUs.
func (SystemProbe) CPUCount() int { return runtime.NumCPU() }

// TotalMemory returns the machine's physical memory in bytes, false when unknown.
func (SystemProbe) TotalMemory() (uint64, bool) { return totalMemory() }

// PathExists reports whether a file or folder exists at path.
func (SystemProbe) PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
