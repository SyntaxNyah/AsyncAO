package config

// Music-history allowlist: only /play links on these hosts are recorded, not a server's own library.
// An entry is a host (any link) or host/folder (audio files only); Discord counts only for audio files.

import (
	"net/url"
	"strings"
)

// musicHostsCap bounds the editable allowlist (hard rule 4).
const musicHostsCap = 64

// musicFileExts are the extensions that count as a song; they restrict Discord and path-scoped entries.
var musicFileExts = []string{".opus", ".mp3", ".ogg", ".oga", ".wav", ".m4a", ".flac", ".aac", ".weba", ".webm"}

// discordHosts are matched ignoring case and subdomains for the audio-file-only rule.
var discordHosts = []string{"discord.com", "discordapp.com", "discordapp.net"}

// musicEntry is one allowlist rule: a host, with subdomains, and an optional path prefix requiring an audio file.
type musicEntry struct {
	host string
	path string // "" = whole host; else a leading-slash prefix, audio-file only
}

func (e musicEntry) matches(host, path string) bool {
	return hostMatches(host, e.host) && (e.path == "" || strings.HasPrefix(path, e.path))
}

// label is the canonical stored form: "host" or "host/folder" without a trailing slash.
func (e musicEntry) label() string {
	if e.path == "" {
		return e.host
	}
	return e.host + strings.TrimSuffix(e.path, "/")
}

// defaultMusicHostList returns a fresh slice of public hosts; server-specific entries are opt-in.
func defaultMusicHostList() []string {
	return []string{
		"catbox.moe",
		"file.garden",
		"youtube.com",
		"youtu.be",
		"discordapp.com",
		"cdn.discordapp.com",
	}
}

// cleanMusicHost lowercases, trims stray dots and drops a leading "www.".
func cleanMusicHost(h string) string {
	h = strings.Trim(strings.ToLower(strings.TrimSpace(h)), ".")
	return strings.TrimPrefix(h, "www.")
}

// musicEntryFromInput parses a typed entry; a full URL yields a host rule so a song link can't be a one-file rule.
func musicEntryFromInput(s string) musicEntry {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return musicEntry{}
	}
	if strings.Contains(s, "://") { // a pasted URL → a HOST rule
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return musicEntry{}
		}
		return musicEntry{host: cleanMusicHost(u.Hostname())}
	}
	host, path := s, ""
	if i := strings.IndexByte(s, '/'); i >= 0 {
		host, path = s[:i], s[i+1:]
	}
	if j := strings.IndexAny(host, ":?#"); j >= 0 {
		host = host[:j]
	}
	host = cleanMusicHost(host)
	if host == "" {
		return musicEntry{}
	}
	if path = strings.Trim(path, "/"); path != "" {
		path = "/" + path + "/" // leading + trailing slash for prefix matching
	}
	return musicEntry{host: host, path: path}
}

// musicEntryFromStored parses a label() string back into a musicEntry.
func musicEntryFromStored(s string) musicEntry {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return musicEntry{host: s[:i], path: "/" + strings.Trim(s[i+1:], "/") + "/"}
	}
	return musicEntry{host: s}
}

// sanitizeMusicHosts normalizes, dedups, drops empties and caps a loaded list.
func sanitizeMusicHosts(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, h := range in {
		e := musicEntryFromInput(h)
		if e.host == "" {
			continue
		}
		lbl := e.label()
		if seen[lbl] {
			continue
		}
		seen[lbl] = true
		out = append(out, lbl)
		if len(out) >= musicHostsCap {
			break
		}
	}
	return out
}

// urlHostPath returns a link's lowercased host and path, or empty strings; a bare music name is never recorded.
func urlHostPath(rawURL string) (host, path string) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.Contains(rawURL, "://") {
		return "", "" // a bare track name, not a link
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", ""
	}
	return strings.ToLower(u.Hostname()), strings.ToLower(u.Path)
}

// hostMatches reports whether host is, or is a subdomain of, domain.
func hostMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// hasMusicFileExt checks the link's extension with query and fragment stripped.
func hasMusicFileExt(rawURL string) bool {
	p := rawURL
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.ToLower(p)
	for _, e := range musicFileExts {
		if strings.HasSuffix(p, e) {
			return true
		}
	}
	return false
}

func isDiscordHost(host string) bool {
	for _, d := range discordHosts {
		if hostMatches(host, d) {
			return true
		}
	}
	return false
}

// MusicHostList returns a copy of the allowlist in insertion order.
func (s *Settings) MusicHostList() []string {

	if len(s.MusicHosts) == 0 {
		return nil
	}
	out := make([]string, len(s.MusicHosts))
	copy(out, s.MusicHosts)
	return out
}

// AddMusicHost normalizes and appends a host or host/folder; reports whether it changed.
func (p *AssetPreferences) AddMusicHost(input string) bool {
	e := musicEntryFromInput(input)
	if e.host == "" {
		return false
	}
	lbl := e.label()
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.AddMusicHost(lbl)
	})
	return changed
}

// RemoveMusicHost drops an entry by its stored label; reports whether it changed.
func (p *AssetPreferences) RemoveMusicHost(label string) bool {
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveMusicHost(label)
	})
	return changed
}

// MusicURLAllowed reports whether a /play link is recorded; others still play.
func (s *Settings) MusicURLAllowed(rawURL string) bool {

	host, path := urlHostPath(rawURL)
	if host == "" {
		return false
	}

	for _, s := range s.MusicHosts {
		e := musicEntryFromStored(s)
		if !e.matches(host, path) {
			continue
		}
		if e.path != "" || isDiscordHost(host) {
			return hasMusicFileExt(rawURL) // a folder rule or Discord: audio files only
		}
		return true
	}
	return false
}

// MusicURLDomain returns a link's allowlist label for grouping, else the bare host, or "" if it isn't a URL.
func (s *Settings) MusicURLDomain(rawURL string) string {

	host, path := urlHostPath(rawURL)
	if host == "" {
		return ""
	}

	for _, s := range s.MusicHosts {
		e := musicEntryFromStored(s)
		if e.matches(host, path) {
			return e.label()
		}
	}
	return host
}
