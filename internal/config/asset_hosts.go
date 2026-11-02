package config

import (
	"net/url"
	"slices"
	"strings"
)

// AssetHostCap bounds a server's extra asset hosts (hard rule 4).
const AssetHostCap = 8

// NormalizeAssetHost turns typed input into an origin ending in a slash, or "" when it isn't an http or https URL.
func NormalizeAssetHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	out := u.String()
	if !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}

// CleanAssetHosts normalizes, drops what isn't a URL or repeats, and caps the list, keeping its order.
func CleanAssetHosts(in []string) []string {
	var out []string
	for _, h := range in {
		if h = NormalizeAssetHost(h); h != "" && !slices.Contains(out, h) && len(out) < AssetHostCap {
			out = append(out, h)
		}
	}
	return out
}

// ServerAssetHosts returns a copy of a server's extra asset hosts, in the order they are asked.
func (s *Settings) ServerAssetHosts(key string) []string {
	return CleanAssetHosts(s.ServerWarm[key].AssetHosts)
}

// SetServerAssetHosts replaces a server's extra asset hosts.
func (p *AssetPreferences) SetServerAssetHosts(key string, hosts []string) {
	clean := CleanAssetHosts(hosts)
	p.rememberServer(key, func(w *ServerWarmInfo) { w.AssetHosts = clean })
}
