// Package platform is the one place that may know which operating system it runs on.
package platform

import (
	"fmt"
	"strings"
)

// Platform is what detection found.
type Platform struct {
	Environment  Environment
	Hardware     Hardware
	Capabilities Capabilities
}

// Hardware is the machine's resources.
type Hardware struct {
	CPUCount int
	// TotalMemoryBytes is the physical memory, 0 when unknown.
	TotalMemoryBytes uint64
	// VirtualMachine is for debugging only: a guest can hide itself, so no feature
	// may depend on it.
	VirtualMachine string
}

// genericVirtualMachine is the name for a guest whose hypervisor is unknown.
const genericVirtualMachine = "virtual machine"

// virtualMachineModels maps text in a machine model to its hypervisor.
var virtualMachineModels = []struct{ text, name string }{
	{"vmware", "VMware"},
	{"virtualbox", "VirtualBox"},
	{"innotek", "VirtualBox"},
	{"qemu", "QEMU"},
	{"kvm", "KVM"},
	{"xen", "Xen"},
	{"parallels", "Parallels"},
	{"bochs", "Bochs"},
	{"microsoft corporation virtual machine", "Hyper-V"},
	{genericVirtualMachine, genericVirtualMachine},
}

func detectVirtualMachine(model string) string {
	model = strings.ToLower(model)
	for _, known := range virtualMachineModels {
		if strings.Contains(model, known.text) {
			return known.name
		}
	}
	return ""
}

// LowMemory reports whether memory is known to be low. Unknown is not low, so a
// failed reading cannot take features from a capable machine.
func (h Hardware) LowMemory() bool {
	return h.TotalMemoryBytes != 0 && h.TotalMemoryBytes < lowMemoryBytes
}

// Detect reads the machine once at startup; it touches the disk (hard rule 2).
func Detect(probe Probe) Platform {
	env := detectEnvironment(probe)
	hardware := Hardware{CPUCount: probe.CPUCount(), VirtualMachine: detectVirtualMachine(probe.MachineModel())}
	if total, ok := probe.TotalMemory(); ok {
		hardware.TotalMemoryBytes = total
	}
	return Platform{Environment: env, Hardware: hardware, Capabilities: capabilitiesFor(env, hardware)}
}

// Report describes the platform for the log, About screen and debug overlay.
func (p Platform) Report() string {
	env := p.Environment
	var b strings.Builder
	fmt.Fprintf(&b, "platform: %s/%s, %s, %s, %s input, %s loop",
		env.OS, env.Arch, env.FormFactor, env.Packaging, env.Input, env.LoopStyle)
	if env.Sandboxed {
		b.WriteString(", sandboxed")
	}
	if env.CaseInsensitivePaths {
		b.WriteString(", case-insensitive paths")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "hardware: %d CPUs, ", p.Hardware.CPUCount)
	if p.Hardware.TotalMemoryBytes == 0 {
		b.WriteString("unknown memory")
	} else {
		fmt.Fprintf(&b, "%.1f GiB memory", float64(p.Hardware.TotalMemoryBytes)/bytesPerGiB)
	}
	if p.Hardware.VirtualMachine != "" {
		fmt.Fprintf(&b, ", virtual machine: %s", p.Hardware.VirtualMachine)
	}
	b.WriteByte('\n')
	for c := Capability(0); c < capabilityCount; c++ {
		if p.Capabilities.Has(c) {
			fmt.Fprintf(&b, "%s: yes\n", c)
			continue
		}
		fmt.Fprintf(&b, "%s: off (%s)\n", c, p.Capabilities.Reason(c))
	}
	return b.String()
}
