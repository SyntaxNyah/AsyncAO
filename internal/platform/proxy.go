package platform

import (
	"os"
	"strings"
)

// ProxySettings is the machine's proxy configuration; netproxy decides how to reach the server it names.
type ProxySettings struct {
	// Source names where this came from, for the Settings readout.
	Source string
	// Server is a host:port, a URL or a scheme=host:port list split by ;, "" for none.
	Server string
	// Bypass is the destinations the machine sends direct.
	Bypass *ProxyBypass
}

// SystemProxySettings reads the machine's proxy configuration. It may start a process, so never call it on the render
// thread.
func SystemProxySettings() ProxySettings { return proxySettings(os.Getenv, osProxySettings) }

// proxySettings puts the environment first: a variable is an explicit per-launch instruction, the OS setting is ambient.
func proxySettings(getenv func(string) string, readOS func() ProxySettings) ProxySettings {
	for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"} {
		server := strings.TrimSpace(getenv(name))
		if server == "" {
			continue
		}
		noProxy := getenv("no_proxy")
		if noProxy == "" {
			noProxy = getenv("NO_PROXY")
		}
		return ProxySettings{Source: "environment variable " + name, Server: server, Bypass: ParseProxyBypass(noProxy)}
	}
	return readOS()
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
	for _, kind := range []struct{ scheme, prefix string }{{"http", "HTTP"}, {"https", "HTTPS"}} {
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
	settings := ProxySettings{Server: strings.Join(servers, ";"), Bypass: ParseProxyBypass(strings.Join(exceptions, ","))}
	if settings.Server != "" {
		settings.Source = macProxySource
	}
	return settings
}
