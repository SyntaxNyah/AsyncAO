package netproxy

import "strings"

// parseScutil reads `scutil --proxy` output: `Key : Value` lines, with a nested ExceptionsList `<array>`.
// It is platform-neutral so the parsing can be tested off macOS.
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

	// macOS keeps host and port after the box is unticked, so only the Enable key decides.
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
	// macOS's spelling of Windows' <local> macro.
	if vals["ExcludeSimpleHostnames"] == "1" {
		bypass.noDot = true
	}

	if httpVia == nil && httpsVia == nil {
		// This platform can't evaluate a script without a JavaScript engine, so fail closed rather than go direct.
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
