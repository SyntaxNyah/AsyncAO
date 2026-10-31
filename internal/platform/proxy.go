package platform

import "strings"

// ProxySettings is the OS's proxy configuration as raw text; netproxy decides what it means.
type ProxySettings struct {
	// Source names where this came from, for the Settings readout.
	Source string
	// Server is a bare host:port or a scheme=host:port list split by ;, "" for none.
	Server string
	// Bypass lists the destinations that go direct, split by ; or comma.
	Bypass string
	// AutoConfigURL is a proxy script the OS is told to use, "" for none.
	AutoConfigURL string
}

const macProxySource = "macOS network settings"

// parseScutil reads `scutil --proxy` output: Key : Value lines, with a nested ExceptionsList array. It is free of OS
// calls so it can be tested off macOS. Automatic discovery alone is ignored, as on Windows: it is often on with no
// proxy at all, so treating it as configured would strand those machines.
func parseScutil(out string) ProxySettings {
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
			exceptions = append(exceptions, val) // array entries are <index> : <value>
			continue
		}
		vals[key] = val
	}

	// macOS keeps host and port after the box is unticked, so only the Enable key decides.
	var servers []string
	for _, kind := range []struct{ scheme, prefix string }{{"http", "HTTP"}, {"https", "HTTPS"}, {"socks", "SOCKS"}} {
		host := vals[kind.prefix+"Proxy"]
		if vals[kind.prefix+"Enable"] != "1" || host == "" {
			continue
		}
		if port := vals[kind.prefix+"Port"]; port != "" {
			host += ":" + port
		}
		servers = append(servers, kind.scheme+"="+host)
	}
	// macOS's spelling of Windows' <local> macro.
	if vals["ExcludeSimpleHostnames"] == "1" {
		exceptions = append(exceptions, "<local>")
	}
	settings := ProxySettings{Server: strings.Join(servers, ";"), Bypass: strings.Join(exceptions, ",")}
	if vals["ProxyAutoConfigEnable"] == "1" {
		settings.AutoConfigURL = vals["ProxyAutoConfigURLString"]
	}
	if settings.Server != "" {
		settings.Source = macProxySource
	}
	return settings
}
