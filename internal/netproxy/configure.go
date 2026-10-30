package netproxy

import (
	"os"
	"sync/atomic"
)

// Configure is the only entry point that reads the OS, never per request (hard rule 2). It runs in main before
// sdl.Init, where a stall costs boot latency, not frames; main is already locked to the render thread by then.

// currentPolicy is read from every transport goroutine, so the read path uses an atomic pointer instead of a lock.
var currentPolicy atomic.Pointer[Policy]

// discoverSystem is the platform's proxy configuration, nil if none; a variable so tests can drive every branch.
var discoverSystem = func() *Policy { return nil }

// Configure installs and returns the policy. ModeSystem reads the environment first: a variable is an
// explicit per-launch instruction, while the OS setting is ambient.
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
		// An unusable manual URL must not become direct, which would leak: fail closed and let the readout say why.
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

// SetPolicy publishes p and points the resolver at it.
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

// Reset returns the package to its pre-Configure state, for tests.
func Reset() {
	currentPolicy.Store(nil)
	SetResolver(nil)
}
