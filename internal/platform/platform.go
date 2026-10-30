// Package platform is the platform abstraction layer: the first thing the
// client builds and the one place that may know which operating system it runs
// on. It holds no SDL; the part that talks to SDL and the hardware is added
// beside it. See docs/RESTRUCTURING_PLAN.md, Part 5g.
package platform

import (
	"fmt"
	"strings"
)

// Platform is what detection found: the environment and what it supports.
type Platform struct {
	Environment  Environment
	Capabilities Capabilities
}

// Detect reads the machine through probe, once, at startup. It touches the disk,
// so it never runs on a render, decode or resolver path (hard rule 2).
func Detect(probe Probe) Platform {
	env := detectEnvironment(probe)
	return Platform{Environment: env, Capabilities: capabilitiesFor(env)}
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
	for c := Capability(0); c < capabilityCount; c++ {
		if p.Capabilities.Has(c) {
			fmt.Fprintf(&b, "%s: yes\n", c)
			continue
		}
		fmt.Fprintf(&b, "%s: off (%s)\n", c, p.Capabilities.Reason(c))
	}
	return b.String()
}
