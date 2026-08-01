package netproxy

import (
	"syscall"
	"unsafe"
)

// Windows proxy discovery, using only the standard library.
//
// NO NEW DEPENDENCY, and that is a checked result rather than an assumption:
// neither golang.org/x/net nor golang.org/x/sys is in this module's go.mod or
// go.sum, so either would be a brand-new direct require. stdlib syscall already
// exports RegOpenKeyEx / RegQueryValueEx / RegCloseKey / HKEY_* / NewLazyDLL /
// UTF16PtrFromString, which is everything this needs.
//
// TWO SOURCES OF TRUTH, and they disagree. The WinINET registry values are what
// "Use a proxy server" writes; the WinHTTP API additionally reports "Automatically
// detect settings" (WPAD) and any auto-config script. Reading only the registry
// is not enough, and this dev box is the proof: ProxyEnable is 0 while the
// auto-detect flag is set, so a registry-only reader reports "no proxy" on a
// machine that is in fact configured to discover one.
//
// ⚠ DLL PLANTING. stdlib's syscall.LazyDLL has no System flag (x/sys's does), so
// syscall.NewLazyDLL("winhttp.dll") would use the default search order — which
// begins with the executable's own directory, and AsyncAO ships several dozen
// staged runtime DLLs next to the .exe. winhttp.dll is therefore loaded through
// kernel32's LoadLibraryExW with LOAD_LIBRARY_SEARCH_SYSTEM32, which is what
// x/sys does internally and what makes that search order impossible.

func init() { discoverSystem = discoverWindows }

const (
	// internetSettingsKey holds the WinINET configuration — the values
	// INTERNET_OPEN_TYPE_PRECONFIG reads, and what the Windows proxy UI writes.
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

	// loadLibrarySearchSystem32 restricts LoadLibraryExW to %SystemRoot%\System32,
	// which is the whole point of using it (see the DLL-planting note above).
	loadLibrarySearchSystem32 = 0x00000800

	// regValueMaxBytes caps one registry read. ProxyServer and ProxyOverride are
	// user-writable and externally controlled, so the buffer needs a named bound
	// (hard rule 4). 32 KiB is orders of magnitude past any real value — the
	// longest observed override list is a few hundred bytes.
	regValueMaxBytes = 32 << 10

	// autoProbeURL is the destination WPAD / a PAC script is evaluated against.
	// A script CAN return different proxies for different destinations, and one
	// probe cannot capture that; https is chosen because it is what the game
	// socket and every asset actually use, so the one answer we get is the one
	// that matters. See autoProxyPolicy for why this is resolved once.
	autoProbeURL = "https://www.example.com/"

	// winhttpAutoDetect / winhttpConfigURL are WINHTTP_AUTOPROXY_AUTO_DETECT and
	// WINHTTP_AUTOPROXY_CONFIG_URL; dhcpAndDNS is
	// WINHTTP_AUTO_DETECT_TYPE_DHCP | _DNS_A, which is the pair every browser
	// uses for WPAD.
	winhttpAutoDetect = 0x00000001
	winhttpConfigURL  = 0x00000002
	dhcpAndDNS        = 0x00000003

	// errAutodetectionFailed is ERROR_WINHTTP_AUTODETECTION_FAILED. It is NOT an
	// error condition for us: it means "no WPAD server answered on this network",
	// which is the correct, common answer on a home connection and means direct.
	errAutodetectionFailed = 12180
	// errUnrecognizedScheme is ERROR_WINHTTP_UNRECOGNIZED_SCHEME, returned for a
	// destination WinHTTP will not evaluate. Treated the same as direct.
	errUnrecognizedScheme = 12006

	// winhttpAccessTypeNoProxy is WINHTTP_ACCESS_TYPE_NO_PROXY: the session used
	// to RESOLVE the proxy must not itself go through one.
	winhttpAccessTypeNoProxy = 1
	// winhttpNoProxy is WINHTTP_PROXY_TYPE_DIRECT — the access type WinHTTP
	// reports when it resolved successfully and the answer is "connect directly".
	winhttpNoProxy = 1

	// winhttpStringMaxChars caps how far a returned WinHTTP string is walked when
	// converting it from UTF-16. The API returns NUL-terminated buffers it
	// allocated itself; the bound only exists so a corrupt pointer cannot make
	// the walk unbounded.
	winhttpStringMaxChars = 8 << 10
)

// discoverWindows reads the machine's configuration into a Policy, or returns
// nil when there is nothing configured.
func discoverWindows() *Policy {
	cfg, ok := winhttpIEConfig()
	if !ok {
		// WinHTTP is unavailable (a stripped container, a service context). The
		// registry is still worth reading — it is the same data minus the
		// auto-detect flag.
		return registryPolicy(false)
	}
	// The registry remains the source for the proxy itself: WinHTTP reports the
	// current CONNECTION's settings, and the two agree for a desktop LAN user
	// while the registry is the one that survives WinHTTP being unavailable.
	if p := registryPolicy(cfg.autoDetect || cfg.autoConfigURL != ""); p != nil {
		return p
	}
	if !cfg.autoDetect && cfg.autoConfigURL == "" {
		return nil
	}
	// No explicit proxy, but the machine is set to find one automatically. This
	// must NOT be assumed to mean "a proxy exists that we cannot reach": automatic
	// detection is ON by default on Windows, including on the overwhelming
	// majority of home machines that have no proxy at all, and it means "look for
	// a WPAD server on this network; if there is none, connect directly". Failing
	// closed on the flag alone would break every one of those users on first
	// launch, which is a far worse outcome than the leak this exists to prevent.
	//
	// So we ask Windows to actually resolve it. WinHttpGetProxyForUrl performs the
	// WPAD discovery and evaluates any auto-config script in-process — no
	// JavaScript engine of our own, which is the reason this is worth the syscall
	// surface. It answers definitively: a proxy, or "detection failed", which on a
	// home network is the correct and common answer and means direct.
	return autoProxyPolicy(cfg)
}

// autoProxyPolicy resolves WPAD / a PAC script into a concrete policy, ONCE.
//
// Once, and not per request, because each call is a network round trip on the
// first use and a syscall thereafter — squarely the kind of work hard rule 2
// keeps off a request path, on a transport that fires thousands of requests per
// cold load. The cost is that a script returning DIFFERENT proxies for different
// destinations is collapsed to the answer for one representative destination.
// That is a real approximation and it is recorded in the policy's source string
// rather than hidden: scripts of that shape are rare, and the alternative —
// a syscall on the asset hot path — is not available to this client.
func autoProxyPolicy(cfg ieConfig) *Policy {
	bypass := parseBypass(cfg.proxyBypass)
	if bypass.dropped == 0 && len(bypass.hosts)+len(bypass.nets)+len(bypass.ips) == 0 && !bypass.noDot {
		// WinHTTP reports no bypass for an auto-configured machine; the registry
		// list is still the user's own and still applies.
		bypass = parseBypass(regString(internetSettingsKey, "ProxyOverride"))
	}
	via, status := winhttpProxyForURL(autoProbeURL, cfg)
	switch status {
	case autoProxyDirect:
		// Resolved, and the answer is "no proxy on this network". This is the
		// common case on a home machine and it is a real result, not a failure —
		// report nothing so the caller falls through to "no proxy configured".
		return nil
	case autoProxyFound:
		src := "Windows automatic proxy detection"
		if cfg.autoConfigURL != "" {
			src = "Windows automatic proxy script"
		}
		httpVia, httpsVia := parseProxyServer(via)
		if httpVia == nil && httpsVia == nil {
			break
		}
		return &Policy{Mode: ModeSystem, Source: src, HTTP: httpVia, HTTPS: httpsVia, Bypass: bypass}
	}
	// The script or the discovery is configured but could not be evaluated. This
	// is the case the fail-closed rule is for: something IS set up, we cannot say
	// where traffic should go, and guessing "direct" would send it somewhere the
	// user did not agree to.
	return &Policy{
		Mode:       ModeSystem,
		Source:     "Windows automatic proxy configuration could not be evaluated",
		AutoScript: true,
		Bypass:     bypass,
	}
}

// registryPolicy reads ProxyEnable / ProxyServer / ProxyOverride.
func registryPolicy(auto bool) *Policy {
	if regDWORD(internetSettingsKey, "ProxyEnable") == 0 {
		return nil
	}
	httpVia, httpsVia := parseProxyServer(regString(internetSettingsKey, "ProxyServer"))
	if httpVia == nil && httpsVia == nil {
		return nil
	}
	src := "Windows proxy settings"
	if auto {
		src += " (automatic detection is also on)"
	}
	return &Policy{
		Mode:   ModeSystem,
		Source: src,
		HTTP:   httpVia,
		HTTPS:  httpsVia,
		Bypass: parseBypass(regString(internetSettingsKey, "ProxyOverride")),
	}
}

// --- registry ---------------------------------------------------------------

// openSettings opens the Internet Settings key for reading. HKEY_CURRENT_USER
// only: the per-machine variant applies when a group policy sets
// ProxySettingsPerUser=0, which is a managed-fleet configuration where WinHTTP —
// read above — already reports the effective answer.
func openSettings() (syscall.Handle, bool) {
	path, err := syscall.UTF16PtrFromString(internetSettingsKey)
	if err != nil {
		return 0, false
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, path, 0, syscall.KEY_QUERY_VALUE, &h); err != nil {
		return 0, false
	}
	return h, true
}

// regString reads a REG_SZ value, or "" when it is absent or unreadable. An
// absent value is the NORMAL case for every one of these names — a machine with
// no proxy simply does not have them — so a missing key is never an error here.
func regString(key, name string) string {
	h, ok := openSettings()
	if !ok {
		return ""
	}
	defer syscall.RegCloseKey(h)
	valName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	var typ uint32
	size := uint32(regValueMaxBytes)
	buf := make([]byte, regValueMaxBytes)
	if err := syscall.RegQueryValueEx(h, valName, nil, &typ, &buf[0], &size); err != nil {
		return ""
	}
	if typ != syscall.REG_SZ && typ != syscall.REG_EXPAND_SZ {
		return ""
	}
	return utf16BytesToString(buf[:size])
}

// regDWORD reads a REG_DWORD value, or 0 when it is absent.
func regDWORD(key, name string) uint32 {
	h, ok := openSettings()
	if !ok {
		return 0
	}
	defer syscall.RegCloseKey(h)
	valName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	var typ uint32
	var val uint32
	size := uint32(unsafe.Sizeof(val))
	if err := syscall.RegQueryValueEx(h, valName, nil, &typ, (*byte)(unsafe.Pointer(&val)), &size); err != nil {
		return 0
	}
	if typ != syscall.REG_DWORD {
		return 0
	}
	return val
}

// utf16BytesToString converts a REG_SZ byte buffer to a Go string, stopping at
// the first NUL. Written by hand rather than reinterpreting the slice as
// []uint16 because the buffer's alignment is not guaranteed by []byte.
func utf16BytesToString(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := uint16(b[i]) | uint16(b[i+1])<<8
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return syscall.UTF16ToString(u)
}

// --- WinHTTP ----------------------------------------------------------------

// ieProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG.
//
// ⚠ autoDetect is int32, NOT bool. The C field is a BOOL, which is a 32-bit int;
// declaring it as a Go bool (as one widely-used library does) reads only its low
// byte and happens to work on amd64 by luck of the struct layout. int32 is what
// the ABI actually says.
// The three strings are kept as *uint16 rather than uintptr all the way through.
// A uintptr is just a number to the compiler and the garbage collector, so
// deriving one and converting it back to a pointer is the pattern `go vet` flags
// as a possible misuse — correctly, even though this particular memory is
// allocated by Windows and not by Go.
type ieProxyConfig struct {
	autoDetect    int32
	_             int32 // explicit padding to the pointer alignment that follows
	autoConfigURL *uint16
	proxy         *uint16
	proxyBypass   *uint16
}

type ieConfig struct {
	autoDetect    bool
	autoConfigURL string
	proxy         string
	proxyBypass   string
}

// winhttpIEConfig calls WinHttpGetIEProxyConfigForCurrentUser. Reports ok=false
// when winhttp.dll or the call is unavailable, which is a normal outcome in a
// service or a stripped image and not an error worth surfacing.
func winhttpIEConfig() (ieConfig, bool) {
	proc, ok := system32Proc("winhttp.dll", "WinHttpGetIEProxyConfigForCurrentUser")
	if !ok {
		return ieConfig{}, false
	}
	var raw ieProxyConfig
	r, _, _ := syscall.SyscallN(proc, uintptr(unsafe.Pointer(&raw)))
	if r == 0 {
		return ieConfig{}, false
	}
	// The three strings are allocated by the API and MUST be released by the
	// caller. A long session refreshing its policy would otherwise leak a little
	// each time, which is the kind of bug that only shows up after hours.
	defer globalFree(raw.autoConfigURL)
	defer globalFree(raw.proxy)
	defer globalFree(raw.proxyBypass)
	return ieConfig{
		autoDetect:    raw.autoDetect != 0,
		autoConfigURL: utf16PtrToString(raw.autoConfigURL),
		proxy:         utf16PtrToString(raw.proxy),
		proxyBypass:   utf16PtrToString(raw.proxyBypass),
	}, true
}

// autoProxyStatus is the three-way outcome of asking Windows to resolve an
// automatic configuration. Three and not two because "resolved: go direct" and
// "could not resolve" must lead to opposite decisions — collapsing them is
// exactly how a fail-closed client breaks every home machine, or how a leaky one
// ignores a real proxy.
type autoProxyStatus int

const (
	autoProxyFailed autoProxyStatus = iota
	autoProxyDirect
	autoProxyFound
)

// winhttpProxyInfo is WINHTTP_PROXY_INFO.
type winhttpProxyInfo struct {
	accessType  uint32
	_           uint32 // padding to the pointer alignment
	proxy       *uint16
	proxyBypass *uint16
}

// winhttpAutoproxyOptions is WINHTTP_AUTOPROXY_OPTIONS.
type winhttpAutoproxyOptions struct {
	flags                uint32
	autoDetectFlags      uint32
	autoConfigURL        *uint16
	reserved1            uintptr
	reserved2            uint32
	autoLogonIfChallenge int32
}

// winhttpProxyForURL asks Windows where a request to target should go, running
// WPAD discovery and evaluating any auto-config script in-process.
func winhttpProxyForURL(target string, cfg ieConfig) (string, autoProxyStatus) {
	open, ok := system32Proc("winhttp.dll", "WinHttpOpen")
	if !ok {
		return "", autoProxyFailed
	}
	getProxy, ok := system32Proc("winhttp.dll", "WinHttpGetProxyForUrl")
	if !ok {
		return "", autoProxyFailed
	}
	closeHandle, ok := system32Proc("winhttp.dll", "WinHttpCloseHandle")
	if !ok {
		return "", autoProxyFailed
	}
	agent, err := syscall.UTF16PtrFromString(userAgentForWPAD)
	if err != nil {
		return "", autoProxyFailed
	}
	// The session that RESOLVES the proxy must not itself be proxied, or a
	// misconfiguration becomes a loop.
	session, _, _ := syscall.SyscallN(open,
		uintptr(unsafe.Pointer(agent)), winhttpAccessTypeNoProxy, 0, 0, 0)
	if session == 0 {
		return "", autoProxyFailed
	}
	defer syscall.SyscallN(closeHandle, session)

	opts := winhttpAutoproxyOptions{autoLogonIfChallenge: 1}
	if cfg.autoDetect {
		opts.flags |= winhttpAutoDetect
		opts.autoDetectFlags = dhcpAndDNS
	}
	if cfg.autoConfigURL != "" {
		wide, err := syscall.UTF16PtrFromString(cfg.autoConfigURL)
		if err != nil {
			return "", autoProxyFailed
		}
		opts.flags |= winhttpConfigURL
		opts.autoConfigURL = wide
	}
	url16, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return "", autoProxyFailed
	}
	var info winhttpProxyInfo
	r, _, callErr := syscall.SyscallN(getProxy, session,
		uintptr(unsafe.Pointer(url16)), uintptr(unsafe.Pointer(&opts)), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		switch uintptr(callErr) {
		case errAutodetectionFailed, errUnrecognizedScheme:
			// Resolved, and the answer is "nothing to go through".
			return "", autoProxyDirect
		}
		return "", autoProxyFailed
	}
	defer globalFree(info.proxy)
	defer globalFree(info.proxyBypass)
	if info.accessType == winhttpNoProxy || info.proxy == nil {
		return "", autoProxyDirect
	}
	return utf16PtrToString(info.proxy), autoProxyFound
}

// userAgentForWPAD is sent on the request that FETCHES an auto-config script.
// Named because it reaches a network the user's administrator controls, and
// naming the client honestly there is the same courtesy the asset fetches pay.
const userAgentForWPAD = "AsyncAO"

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procLoadLibraryEx = kernel32.NewProc("LoadLibraryExW")
	procGetProcAddr   = kernel32.NewProc("GetProcAddress")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
)

// system32Proc resolves one export of a DLL loaded from System32 ONLY.
//
// kernel32 itself is safe to name directly — it is a KnownDLL, so Windows always
// resolves it from the system image regardless of search order. Everything else
// goes through LoadLibraryExW with LOAD_LIBRARY_SEARCH_SYSTEM32, because the
// default search order starts with the executable's directory and AsyncAO ships
// a directory full of DLLs beside the .exe.
func system32Proc(dll, name string) (uintptr, bool) {
	wide, err := syscall.UTF16PtrFromString(dll)
	if err != nil {
		return 0, false
	}
	h, _, _ := procLoadLibraryEx.Call(uintptr(unsafe.Pointer(wide)), 0, loadLibrarySearchSystem32)
	if h == 0 {
		return 0, false
	}
	// Deliberately NOT FreeLibrary'd: the handle is reused across refreshes, the
	// module is a system DLL that stays mapped anyway, and freeing it while
	// another goroutine holds a resolved proc address would be a use-after-free.
	cname, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, false
	}
	proc, _, _ := procGetProcAddr.Call(h, uintptr(unsafe.Pointer(cname)))
	return proc, proc != 0
}

func globalFree(p *uint16) {
	if p != nil {
		_, _, _ = procGlobalFree.Call(uintptr(unsafe.Pointer(p)))
	}
}

// utf16PtrToString reads a NUL-terminated UTF-16 string the API allocated.
// Bounded by winhttpStringMaxChars so a corrupt pointer cannot walk forever.
//
// unsafe.Add rather than pointer arithmetic on a uintptr: it is the sanctioned
// form for stepping through memory the Go heap does not own, and it keeps the
// value a pointer the whole way so nothing can be collected out from under it.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var u []uint16
	for i := 0; i < winhttpStringMaxChars; i++ {
		c := *(*uint16)(unsafe.Add(unsafe.Pointer(p), i*2))
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return syscall.UTF16ToString(u)
}
