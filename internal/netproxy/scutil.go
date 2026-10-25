package netproxy

import "strings"

// parseScutil reads the dictionary `scutil --proxy` prints on macOS.
//
// Platform-NEUTRAL on purpose, even though only the darwin build calls it. The
// parsing is where the bugs live, and a file named discover_darwin.go can only
// ever be compiled — let alone tested — on a machine nobody here develops on.
// Keeping the syscall (a subprocess) on one side of this line and the decision
// on the other is what makes the decision testable everywhere.
//
// The format is a block of `Key : Value` lines, with ExceptionsList rendered as
// a nested `<array> { 0 : *.local  1 : 169.254/16 }`.
func parseScutil(out string) *Policy {
	vals := map[string]string{}
	var exceptions []string
	inArray := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasSuffix(line, "<array> {"):
			inArray = strings.HasPrefix(line, "ExceptionsList")
			continue
		case line == "}":
			inArray = false
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if inArray {
			// Array entries are `<index> : <value>`; the key is just the index.
			exceptions = append(exceptions, val)
			continue
		}
		vals[key] = val
	}

	// macOS keeps the host and port after the box is unticked, so the Enable key
	// is the only thing that decides. Honouring a disabled entry would route a
	// user through a proxy they explicitly turned off.
	hostPort := func(enable, host, port string) string {
		if vals[enable] != "1" || vals[host] == "" {
			return ""
		}
		if p := vals[port]; p != "" {
			return vals[host] + ":" + p
		}
		return vals[host]
	}
	httpVia := parseProxyURL(hostPort("HTTPEnable", "HTTPProxy", "HTTPPort"))
	httpsVia := parseProxyURL(hostPort("HTTPSEnable", "HTTPSProxy", "HTTPSPort"))
	if socks := hostPort("SOCKSEnable", "SOCKSProxy", "SOCKSPort"); socks != "" {
		if u := parseProxyURL("socks5://" + socks); u != nil {
			if httpVia == nil {
				httpVia = u
			}
			if httpsVia == nil {
				httpsVia = u
			}
		}
	}

	bypass := parseBypass(strings.Join(exceptions, ","))
	// "Exclude simple hostnames" is macOS's spelling of Windows' <local> macro.
	if vals["ExcludeSimpleHostnames"] == "1" {
		bypass.noDot = true
	}

	if httpVia == nil && httpsVia == nil {
		// No explicit proxy. An auto-config script or auto-discovery IS a
		// configuration, and one this platform cannot evaluate: there is no macOS
		// equivalent of WinHttpGetProxyForUrl reachable without embedding a
		// JavaScript engine, which is not a trade this client will make against a
		// 256 MiB budget. Report it honestly and let the fail-closed rule and the
		// banner take over, rather than quietly connecting direct.
		if vals["ProxyAutoConfigEnable"] == "1" || vals["ProxyAutoDiscoveryEnable"] == "1" {
			src := "macOS automatic proxy detection, which cannot be evaluated on this platform"
			if u := vals["ProxyAutoConfigURLString"]; u != "" {
				src = "macOS proxy script (" + u + "), which cannot be evaluated on this platform"
			}
			return &Policy{Mode: ModeSystem, Source: src, AutoScript: true, Bypass: bypass}
		}
		return nil
	}
	return &Policy{
		Mode:   ModeSystem,
		Source: "macOS network settings",
		HTTP:   httpVia,
		HTTPS:  httpsVia,
		Bypass: bypass,
	}
}
