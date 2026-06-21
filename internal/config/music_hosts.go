package config

// Music-history domain allowlist (M12). The "recently played" history records a
// /play link only when its host is on this list — the point is to capture songs
// from "unique" user-hosted domains (catbox, file.garden, …) that you'd want to
// keep, NOT the server's own music library (which still plays; it's just not worth
// saving, you already have it on the server). Discord is allowlisted but only
// counts when the link is an actual audio file, since most Discord CDN links are
// images/attachments. The whole list is editable in Settings — NOTHING is
// hardcoded to a specific server.
//
// An entry is either a bare host ("catbox.moe" — records any /play link on it) or
// a host/folder ("miku.pizza/base/youtube" — records only audio files under that
// path, so a server's user-rip folder can be saved while the rest of its library
// isn't). Folder rules are opt-in: a player who wants their server's rips saved
// adds that one path themselves.
//
// All of this runs once per song (at capture) and once per Save click — never on
// a per-frame path, so it costs nothing on the render/UI hot loop.

import (
	"net/url"
	"strings"
)

// musicHostsCap bounds the editable allowlist (rule §17.4: no unbounded slices).
const musicHostsCap = 64

// musicFileExts are the link extensions that count as a real song file. Used to
// gate Discord and path-scoped entries (an image/attachment link must not record).
var musicFileExts = []string{".opus", ".mp3", ".ogg", ".oga", ".wav", ".m4a", ".flac", ".aac", ".weba", ".webm"}

// discordHosts are matched case- and subdomain-insensitively for the "Discord
// only when it's an audio file" rule.
var discordHosts = []string{"discord.com", "discordapp.com", "discordapp.net"}

// musicEntry is one allowlist rule: a host (matched including subdomains) and an
// optional path prefix. A bare-host rule records any /play link on that host; a
// path-scoped rule records only links whose URL path starts with the prefix AND
// that point at an actual audio file. Path rules carve a "songs people host here"
// folder out of a domain we otherwise skip.
type musicEntry struct {
	host string
	path string // "" = whole host; else a leading-slash prefix, audio-file only
}

func (e musicEntry) matches(host, path string) bool {
	return hostMatches(host, e.host) && (e.path == "" || strings.HasPrefix(path, e.path))
}

// label is the entry's canonical stored/display form: "host" or "host/folder"
// (no trailing slash), e.g. "catbox.moe" or "miku.pizza/base/youtube".
func (e musicEntry) label() string {
	if e.path == "" {
		return e.host
	}
	return e.host + strings.TrimSuffix(e.path, "/")
}

// defaultMusicHostList is the out-of-the-box allowlist (returned fresh so callers
// never alias a shared slice): generic public music hosts, all removable in
// Settings. Discord is here but only ever matches audio files. No server-specific
// entries — those are opt-in.
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

// cleanMusicHost lowercases, trims stray dots, and drops a leading "www.".
func cleanMusicHost(h string) string {
	h = strings.Trim(strings.ToLower(strings.TrimSpace(h)), ".")
	return strings.TrimPrefix(h, "www.")
}

// musicEntryFromInput parses a user-typed allowlist entry into a host (+ optional
// path-prefix rule). A full URL (with a scheme) yields a HOST rule — pasting a
// song link must not turn into a one-file rule. A bare "host/folder" yields a
// PATH-scoped rule. Returns a zero entry (host=="") for unparseable input.
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

// musicEntryFromStored parses a canonical allowlist string (as label() produces)
// back into a musicEntry.
func musicEntryFromStored(s string) musicEntry {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return musicEntry{host: s[:i], path: "/" + strings.Trim(s[i+1:], "/") + "/"}
	}
	return musicEntry{host: s}
}

// sanitizeMusicHosts normalizes, dedups, drops empties, and caps a loaded list
// (each entry parsed to its canonical host-or-host/folder label).
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

// urlHostPath extracts a /play link's lowercased host and path (no port), or
// ("","") if it isn't a real URL (a bare server-music name has no host and is
// never recorded). The path feeds path-scoped allowlist rules.
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

// hasMusicFileExt reports whether a link points at an audio file (extension
// check, query/fragment stripped).
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

// MusicHostList returns a copy of the editable allowlist (insertion order) for
// the settings UI.
func (p *AssetPreferences) MusicHostList() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.MusicHosts) == 0 {
		return nil
	}
	out := make([]string, len(p.MusicHosts))
	copy(out, p.MusicHosts)
	return out
}

// AddMusicHost normalizes and appends a host (or host/folder) to the allowlist
// (deduped, bounded). Reports whether it changed.
func (p *AssetPreferences) AddMusicHost(input string) bool {
	e := musicEntryFromInput(input)
	if e.host == "" {
		return false
	}
	lbl := e.label()
	p.mu.Lock()
	if len(p.MusicHosts) >= musicHostsCap {
		p.mu.Unlock()
		return false
	}
	for _, h := range p.MusicHosts {
		if h == lbl {
			p.mu.Unlock()
			return false
		}
	}
	p.MusicHosts = append(p.MusicHosts, lbl)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// RemoveMusicHost drops an entry from the allowlist by its stored label (what the
// settings list shows). Reports whether it changed.
func (p *AssetPreferences) RemoveMusicHost(label string) bool {
	p.mu.Lock()
	for i, h := range p.MusicHosts {
		if h == label {
			p.MusicHosts = append(p.MusicHosts[:i], p.MusicHosts[i+1:]...)
			p.mu.Unlock()
			p.markDirty()
			return true
		}
	}
	p.mu.Unlock()
	return false
}

// MusicURLAllowed reports whether a /play link should be recorded into the music
// history (and thus be Save-able): its host (or host/folder) must be on the
// allowlist, and a Discord link OR a path-scoped entry additionally has to be an
// actual audio file. A bare track name (no host) or a song on a non-listed domain
// returns false — it still plays, it just isn't recorded. Once per song, never per
// frame.
func (p *AssetPreferences) MusicURLAllowed(rawURL string) bool {
	host, path := urlHostPath(rawURL)
	if host == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.MusicHosts {
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

// MusicURLDomain returns the normalized label of a /play link for grouping (e.g.
// "files.catbox.moe" -> "catbox.moe", or a folder entry -> "miku.pizza/base/youtube"
// so those rips group apart from the rest of the host), else the bare host. "" when
// it isn't a URL.
func (p *AssetPreferences) MusicURLDomain(rawURL string) string {
	host, path := urlHostPath(rawURL)
	if host == "" {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.MusicHosts {
		e := musicEntryFromStored(s)
		if e.matches(host, path) {
			return e.label()
		}
	}
	return host
}
