package netproxy

import (
	"os"
	"sync/atomic"
)

// Configure is how the app tells this package what the user chose. It is the
// only entry point that reads the operating system, and it is called at boot and
// again when the setting changes — never per request.
//
// THREADING. Discovery is a registry read or a subprocess depending on the
// platform, so it must not sit on a hot path (hard rule 2). It does not need its
// own goroutine either: the caller is `main` before sdl.Init, where a stall
// costs boot latency rather than frames. Note the justification is "before
// sdl.Init", NOT "before the render loop exists" — runtime.LockOSThread is the
// first statement of main, so by the time this runs the main goroutine is
// already pinned to the render thread.

// currentPolicy is the published snapshot. atomic.Pointer because it is read
// from every transport goroutine while Configure may replace it, and the read
// path must not take a lock.
var currentPolicy atomic.Pointer[Policy]

// discoverSystem is the platform's own proxy configuration, or nil when the
// platform has none to give. Replaced per-GOOS; the variable rather than a
// direct call is what lets the platform-neutral tests drive every branch.
var discoverSystem = func() *Policy { return nil }

// Configure resolves mode into a policy, installs it, and returns it so the
// caller can show the user what happened.
//
// ModeSystem consults the environment FIRST and the operating system second.
// That order is deliberate: an environment variable is an explicit, per-launch
// instruction from whoever started the program, while the OS setting is ambient.
// Someone who runs AsyncAO with HTTPS_PROXY set has said something more specific
// than their control panel has.
func Configure(mode Mode, manualURL string) *Policy {
	p := resolvePolicy(mode, manualURL)
	SetPolicy(p)
	return p
}

func resolvePolicy(mode Mode, manualURL string) *Policy {
	switch mode {
	case ModeDirect:
		return direct
	case ModeManual:
		if p := manualPolicy(manualURL); p != nil {
			return p
		}
		// A manual mode with an unusable URL must NOT silently become direct:
		// the user asked for a proxy, and going direct instead is the leak this
		// package exists to close. Fail closed and let the readout say why.
		return &Policy{
			Mode:       ModeManual,
			Source:     "the proxy address in Settings could not be read",
			AutoScript: true,
			Bypass:     parseBypass(""),
		}
	default:
		if p := envPolicy(os.Getenv); p != nil {
			return p
		}
		if p := discoverSystem(); p != nil {
			return p
		}
		return &Policy{Mode: ModeSystem, Source: "no proxy configured on this machine"}
	}
}

// SetPolicy publishes p and points the resolver at it. Exported for tests and
// for any caller that has built a policy by other means.
func SetPolicy(p *Policy) {
	if p == nil {
		p = direct
	}
	currentPolicy.Store(p)
	SetResolver(policyProxy(p))
}

// Current is the policy in force, never nil.
func Current() *Policy {
	if p := currentPolicy.Load(); p != nil {
		return p
	}
	return direct
}

// Reset returns the package to its pre-Configure state: no policy, resolver back
// to the environment default. For tests, so one that installs a policy cannot
// affect the next.
func Reset() {
	currentPolicy.Store(nil)
	SetResolver(nil)
}
