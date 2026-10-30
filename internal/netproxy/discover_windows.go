package netproxy

import (
	"syscall"
	"unsafe"
)

func init() { discoverSystem = discoverWindows }

const (
	// internetSettingsKey holds the WinINET configuration that the Windows proxy UI writes.
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

	// loadLibrarySearchSystem32 restricts LoadLibraryExW to %SystemRoot%\System32, to prevent DLL planting.
	loadLibrarySearchSystem32 = 0x00000800

	// regValueMaxBytes caps one registry read, as these values are externally controlled (hard rule 4).
	regValueMaxBytes = 32 << 10

	// autoProbeURL is what a PAC script is evaluated against; https because the game socket and assets use it.
	autoProbeURL = "https://www.example.com/"

	// winhttpConfigURL is WINHTTP_AUTOPROXY_CONFIG_URL; AUTO_DETECT is deliberately not used, see discoverWindows.
	winhttpConfigURL = 0x00000002

	// errAutodetectionFailed is ERROR_WINHTTP_AUTODETECTION_FAILED: no WPAD server answered, which means direct.
	errAutodetectionFailed = 12180
	// errUnrecognizedScheme is ERROR_WINHTTP_UNRECOGNIZED_SCHEME, treated as direct.
	errUnrecognizedScheme = 12006

	// winhttpAccessTypeNoProxy is WINHTTP_ACCESS_TYPE_NO_PROXY: the session resolving the proxy must not use one.
	winhttpAccessTypeNoProxy = 1
	// winhttpNoProxy is WINHTTP_PROXY_TYPE_DIRECT: resolved, and the answer is connect directly.
	winhttpNoProxy = 1

	// winhttpStringMaxChars bounds the UTF-16 walk so a corrupt pointer can't make it unbounded.
	winhttpStringMaxChars = 8 << 10
)

// discoverWindows returns the machine's proxy configuration, or nil when nothing is configured.
func discoverWindows() *Policy {
	cfg, ok := winhttpIEConfig()
	if !ok {
		// WinHTTP is unavailable (a stripped container, a service); the registry has the same minus auto-detect.
		return registryPolicy()
	}
	// The registry stays the source for the proxy itself, because it survives WinHTTP being unavailable.
	if p := registryPolicy(); p != nil {
		return p
	}
	if cfg.autoConfigURL == "" {
		return nil
	}
	// Only an explicit PAC URL is honoured. WPAD is deliberately not: it is on by default, and a misbehaving
	// server or an ISP's captive redirect would route every asset through it and could take the client down.
	return autoProxyPolicy(cfg)
}

// autoProxyPolicy resolves a PAC script once: each call is a network round trip then a syscall, which hard
// rule 2 keeps off a request path. A script with per-destination proxies collapses to one answer.
func autoProxyPolicy(cfg ieConfig) *Policy {
	bypass := parseBypass(cfg.proxyBypass)
	if bypass.dropped == 0 && len(bypass.hosts)+len(bypass.nets)+len(bypass.ips) == 0 && !bypass.noDot {
		// WinHTTP reports no bypass for an auto-configured machine; the user's registry list still applies.
		bypass = parseBypass(regString(internetSettingsKey, "ProxyOverride"))
	}
	via, status := winhttpProxyForURL(autoProbeURL, cfg)
	switch status {
	case autoProxyDirect:
		// A real result, common at home, not a failure: report no proxy configured.
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
	// Configured but not evaluable, so fail closed: guessing direct would send traffic where the user did not agree to.
	return &Policy{
		Mode:       ModeSystem,
		Source:     "Windows automatic proxy configuration could not be evaluated",
		AutoScript: true,
		Bypass:     bypass,
	}
}

func registryPolicy() *Policy {
	if regDWORD(internetSettingsKey, "ProxyEnable") == 0 {
		return nil
	}
	httpVia, httpsVia := parseProxyServer(regString(internetSettingsKey, "ProxyServer"))
	if httpVia == nil && httpsVia == nil {
		return nil
	}
	return &Policy{
		Mode:   ModeSystem,
		Source: "Windows proxy settings",
		HTTP:   httpVia,
		HTTPS:  httpsVia,
		Bypass: parseBypass(regString(internetSettingsKey, "ProxyOverride")),
	}
}

// openSettings uses HKEY_CURRENT_USER only; under the per-machine group policy, WinHTTP has the real answer.
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

// regString reads a REG_SZ value, or "" when absent, which is the normal case on a machine with no proxy.
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

// utf16BytesToString decodes by hand because []byte alignment isn't guaranteed for a []uint16 cast.
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

// ieProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG.
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

// winhttpIEConfig calls WinHttpGetIEProxyConfigForCurrentUser; ok=false is normal in a service or stripped image.
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
	// The API allocates these strings and the caller must release them.
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

// autoProxyStatus has three outcomes because "go direct" and "could not resolve" need opposite decisions.
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

// winhttpProxyForURL evaluates an explicit PAC URL for target; WPAD auto-detection is deliberately not requested.
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
	// The session resolving the proxy must not itself be proxied, or a misconfiguration becomes a loop.
	session, _, _ := syscall.SyscallN(open,
		uintptr(unsafe.Pointer(agent)), winhttpAccessTypeNoProxy, 0, 0, 0)
	if session == 0 {
		return "", autoProxyFailed
	}
	defer syscall.SyscallN(closeHandle, session)

	opts := winhttpAutoproxyOptions{autoLogonIfChallenge: 1}
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

// userAgentForWPAD identifies the client on the request that fetches an auto-config script.
const userAgentForWPAD = "AsyncAO"

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procLoadLibraryEx = kernel32.NewProc("LoadLibraryExW")
	procGetProcAddr   = kernel32.NewProc("GetProcAddress")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
)

// system32Proc resolves one export of a DLL loaded from System32 only.
func system32Proc(dll, name string) (uintptr, bool) {
	wide, err := syscall.UTF16PtrFromString(dll)
	if err != nil {
		return 0, false
	}
	h, _, _ := procLoadLibraryEx.Call(uintptr(unsafe.Pointer(wide)), 0, loadLibrarySearchSystem32)
	if h == 0 {
		return 0, false
	}
	// Deliberately never freed: another goroutine may hold a resolved proc address.
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

// utf16PtrToString reads a NUL-terminated string the API allocated, bounded by winhttpStringMaxChars.
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
