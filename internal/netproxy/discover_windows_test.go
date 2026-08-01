package netproxy

import (
	"testing"
)

// These exercise the real Windows APIs against whatever this machine is actually
// configured with, so they assert INVARIANTS rather than values — a dev box, a
// CI runner and a corporate laptop all give different answers and all three must
// be handled without a crash.
//
// The parsing and matching that turn these readings into a decision are covered
// by the platform-neutral tests, which is where the logic deliberately lives:
// this file's job is to prove the syscall layer does not fault, does not hang
// and does not lie about the shape of what it returns.

// TestWinhttpIEConfigDoesNotFault is the load-bearing one. It exercises the
// hand-rolled struct layout, the System32-only DLL load, the UTF-16 walk and the
// GlobalFree of three API-allocated strings. A layout mistake here is an access
// violation, not a wrong answer.
func TestWinhttpIEConfigDoesNotFault(t *testing.T) {
	cfg, ok := winhttpIEConfig()
	if !ok {
		t.Skip("WinHttpGetIEProxyConfigForCurrentUser unavailable in this environment")
	}
	t.Logf("autoDetect=%v autoConfigURL=%q proxy=%q bypass=%q",
		cfg.autoDetect, cfg.autoConfigURL, cfg.proxy, cfg.proxyBypass)
	// Run it repeatedly: the strings are freed on every call, so a double-free or
	// a missed free shows up here rather than after hours of a real session.
	for i := 0; i < 32; i++ {
		if _, ok := winhttpIEConfig(); !ok {
			t.Fatalf("call %d failed after an earlier success — a string was freed twice or the handle was released", i)
		}
	}
}

// TestSystem32ProcRefusesAMissingExport pins the failure path of the DLL loader,
// which is the branch that must not crash on a stripped image.
func TestSystem32ProcRefusesAMissingExport(t *testing.T) {
	if _, ok := system32Proc("winhttp.dll", "WinHttpNoSuchFunctionExists"); ok {
		t.Error("resolved an export that does not exist")
	}
	if _, ok := system32Proc("asyncao-not-a-real-dll.dll", "Anything"); ok {
		t.Error("loaded a DLL that does not exist — and would have searched the exe's own directory to do it")
	}
}

// TestRegistryReadsAreTotal: every one of these values is ABSENT on a machine
// with no proxy, which is the common case. A missing value must read as "not
// configured" and never as an error or a panic.
func TestRegistryReadsAreTotal(t *testing.T) {
	t.Logf("ProxyEnable=%d ProxyServer=%q ProxyOverride=%q AutoConfigURL=%q",
		regDWORD(internetSettingsKey, "ProxyEnable"),
		regString(internetSettingsKey, "ProxyServer"),
		regString(internetSettingsKey, "ProxyOverride"),
		regString(internetSettingsKey, "AutoConfigURL"))
	// A name that certainly does not exist, and a type mismatch in both
	// directions — reading a REG_SZ as a DWORD and the reverse.
	if got := regString(internetSettingsKey, "AsyncAONoSuchValue"); got != "" {
		t.Errorf("a missing REG_SZ read as %q", got)
	}
	if got := regDWORD(internetSettingsKey, "AsyncAONoSuchValue"); got != 0 {
		t.Errorf("a missing REG_DWORD read as %d", got)
	}
	if got := regDWORD(internetSettingsKey, "ProxyServer"); got != 0 {
		t.Errorf("a REG_SZ read as a DWORD returned %d, want 0 — the type check is missing", got)
	}
	if got := regString(internetSettingsKey, "ProxyEnable"); got != "" {
		t.Errorf("a REG_DWORD read as a string returned %q, want empty", got)
	}
}

// TestAutomaticDetectionIsRESOLVEDNotAssumed is the whole reason
// WinHttpGetProxyForUrl is worth its syscall surface.
//
// "Automatically detect settings" is ON BY DEFAULT on Windows, including on the
// overwhelming majority of home machines that have no proxy whatsoever — this
// dev box among them. Treating the flag as "a proxy exists that we cannot
// evaluate" and failing closed on it would break every one of those users on
// first launch, which is a far worse outcome than the leak the fail-closed rule
// exists to prevent. The flag means "look for a WPAD server; if there is none,
// connect directly", so the only correct move is to make Windows answer it.
func TestAutomaticDetectionIsRESOLVEDNotAssumed(t *testing.T) {
	cfg, ok := winhttpIEConfig()
	if !ok {
		t.Skip("WinHTTP unavailable in this environment")
	}
	p := discoverWindows()
	if p == nil {
		t.Logf("resolved to DIRECT (autoDetect=%v, autoConfigURL=%q)", cfg.autoDetect, cfg.autoConfigURL)
		if regDWORD(internetSettingsKey, "ProxyEnable") != 0 {
			t.Error("ProxyEnable is set but discovery reported no proxy")
		}
		return
	}
	t.Logf("discovered: %s", p.Describe())
	if p.Mode != ModeSystem {
		t.Errorf("Mode = %v, want ModeSystem", p.Mode)
	}
	if p.Source == "" {
		t.Error("a discovered policy must name its source — the readout is the user's only view of this")
	}
	if p.HTTP == nil && p.HTTPS == nil && !p.AutoScript {
		t.Error("a policy with no proxy and no auto-script should have been nil, not a live policy")
	}
	if p.AutoScript && !cfg.autoDetect && cfg.autoConfigURL == "" {
		t.Error("fail-closed was engaged on a machine with no automatic configuration at all")
	}
}

// TestAutoDetectAloneDoesNotFailClosed states the rule above as the flat
// assertion it deserves, because getting it wrong is a total connectivity loss
// for an ordinary home user and the symptom would look like "the update broke my
// internet".
func TestAutoDetectAloneDoesNotFailClosed(t *testing.T) {
	if regDWORD(internetSettingsKey, "ProxyEnable") != 0 {
		t.Skip("this machine has an explicit proxy enabled; the auto-detect-only path is not exercised")
	}
	if url := regString(internetSettingsKey, "AutoConfigURL"); url != "" {
		t.Skipf("this machine has an explicit auto-config script (%s)", url)
	}
	cfg, ok := winhttpIEConfig()
	if !ok || !cfg.autoDetect {
		t.Skip("automatic detection is off on this machine")
	}
	// Automatic detection on, nothing else configured. Whatever comes back must
	// not be the fail-closed policy unless WPAD genuinely resolved to a proxy.
	p := discoverWindows()
	if p != nil && p.AutoScript {
		t.Errorf("auto-detect with no WPAD server failed closed (%s) — every home Windows machine would lose connectivity", p.Describe())
	}
}

// TestWinhttpProxyForURLSeparatesDirectFromFailed pins the three-way outcome.
// Collapsing "resolved: go direct" into "could not resolve" is precisely how a
// fail-closed client breaks a working machine; collapsing it the other way is
// how a leaky one ignores a real proxy.
func TestWinhttpProxyForURLSeparatesDirectFromFailed(t *testing.T) {
	cfg, ok := winhttpIEConfig()
	if !ok {
		t.Skip("WinHTTP unavailable in this environment")
	}
	via, status := winhttpProxyForURL(autoProbeURL, cfg)
	t.Logf("WinHttpGetProxyForUrl(%s) -> %q, status=%d", autoProbeURL, via, status)
	switch status {
	case autoProxyFound:
		if via == "" {
			t.Error("status says a proxy was found but none was returned")
		}
	case autoProxyDirect:
		if via != "" {
			t.Errorf("status says direct but returned %q", via)
		}
	case autoProxyFailed:
		// Legitimate: no automatic configuration is set, or WinHTTP declined.
		if via != "" {
			t.Errorf("status says failed but returned %q", via)
		}
	default:
		t.Errorf("unknown status %d", status)
	}
	// A destination WinHTTP will not evaluate must come back DIRECT rather than
	// FAILED — a scheme we cannot ask about is not a machine we cannot trust.
	if _, status := winhttpProxyForURL("not-a-url", cfg); status == autoProxyFound {
		t.Error("an unparseable target reported a proxy")
	}
}
