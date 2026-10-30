// Package platform is the platform abstraction layer: the first thing the
// client builds and the one place that may know which operating system it runs
// on. It holds no SDL; the part that talks to SDL and the hardware is added
// beside it. See docs/RESTRUCTURING_PLAN.md, Part 5g.
package platform

import (
	"fmt"
	"strings"
)

// Platform is what detection found: the environment, the hardware and what they
// support.
type Platform struct {
	Environment  Environment
	Hardware     Hardware
	Capabilities Capabilities
}

// Hardware is the machine's resources, detected once. Plain data.
type Hardware struct {
	CPUCount int
	// TotalMemoryBytes is the physical memory, 0 when unknown.
	TotalMemoryBytes uint64
	// VirtualMachine names the hypervisor when one was recognised, empty otherwise.
	// For debugging only: a guest can hide itself, so empty proves nothing and no
	// feature may depend on this.
	VirtualMachine string
}

// genericVirtualMachine is the name for a guest whose hypervisor is unknown.
const genericVirtualMachine = "virtual machine"

// virtualMachineModels maps text in a machine's vendor or product name to the
// hypervisor it belongs to.
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

// LowMemory reports whether the machine is known to have too little memory for
// the high-memory features. An unknown amount is not low: a failed reading must
// not take features away from a capable machine.
func (h Hardware) LowMemory() bool {
	return h.TotalMemoryBytes != 0 && h.TotalMemoryBytes < lowMemoryBytes
}

// Detect reads the machine through probe, once, at startup. It touches the disk,
// so it never runs on a render, decode or resolver path (hard rule 2).
func Detect(probe Probe) Platform {
	env := detectEnvironment(probe)
	hardware := Hardware{CPUCount: probe.CPUCount(), VirtualMachine: detectVirtualMachine(probe.MachineModel())}
	if total, ok := probe.TotalMemory(); ok {
		hardware.TotalMemoryBytes = total
	}
	return Platform{Environment: env, Hardware: hardware, Capabilities: capabilitiesFor(env, hardware)}
}

// Report describes the platform for the log, the About screen and the debug
// overlay: the environment, then each capability, with the reason when it is off.
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
