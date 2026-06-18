package config

// Music-history domain allowlist (M12). The "recently played" history records a
// /play link only when its host is on this list — the point is to capture songs
// from "unique" user-hosted domains (catbox, file.garden, …) that you'd want to
// keep, NOT the server's own music library (which still plays; it's just not
// worth saving, you already have it on the server). Discord is allowlisted but
// only counts when the link is an actual audio file, since most Discord CDN
// links are images/attachments. The list is editable in Settings.
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
// gate Discord links (an image/attachment CDN link must not be recorded).
var musicFileExts = []string{".opus", ".mp3", ".ogg", ".oga", ".wav", ".m4a", ".flac", ".aac", ".weba", ".webm"}

// discordHosts are matched case- and subdomain-insensitively for the "Discord
// only when it's an audio file" rule.
var discordHosts = []string{"discord.com", "discordapp.com", "discordapp.net"}

// musicEntry is one allowlist rule: a host (matched including subdomains) and an
// optional path prefix. A bare-host rule records any /play link on that host; a
// path-scoped rule records only links whose URL path starts with the prefix AND
// that point at an actual audio file. Path rules carve a "songs people host
// here" folder out of a domain we otherwise skip.
type musicEntry struct {
	host string
	path string // "" = whole host; else a leading-slash prefix, audio-file only
}

func (e musicEntry) matches(host, path string) bool {
	return hostMatches(host, e.host) && (e.path == "" || strings.HasPrefix(path, e.path))
}

// label is the entry's grouping/display form, e.g. "miku.pizza/base/youtube".
func (e musicEntry) label() string {
	if e.path == "" {
		return e.host
	}
	return e.host + strings.TrimSuffix(e.path, "/")
}

// builtinMusicEntries are curated, always-on path rules that do NOT live in the
// editable list — so they apply even to a user who already has a saved allowlist
// (a new editable default would never reach them: an on-disk list overrides it).
// miku.pizza/base/youtube/ is Skrapegropen's folder of user-hosted YouTube rips
// (e.g. .../base/youtube/Song.opus): the rest of miku.pizza is the server's own
// library — it plays but isn't worth saving — so we record only that song path.
func builtinMusicEntries() []musicEntry {
	return []musicEntry{
		{host: "miku.pizza", path: "/base/youtube/"},
	}
}

// BuiltinMusicHostLabels lists the always-on curated rules for the Settings UI
// (display only — they aren't removable). Shown under the editable allowlist.
func BuiltinMusicHostLabels() []string {
	es := builtinMusicEntries()
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.label()
	}
	return out
}

// defaultMusicHostList is the out-of-the-box allowlist (returned fresh so callers
// never alias a shared slice). Discord is here but only ever matches audio files.
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

// musicHostFromInput normalizes a user-typed allowlist entry to a bare host:
// "https://www.Catbox.moe/x" -> "catbox.moe". Returns "" if there's no host.
func musicHostFromInput(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	// Parse as a URL when it looks like one; otherwise treat the text as a host.
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			s = u.Hostname()
		}
	}
	// Strip any leftover path/port and a leading "www.".
	if i := strings.IndexAny(s, "/:?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "www.")
	return strings.Trim(s, ".")
}

// sanitizeMusicHosts normalizes, dedups, drops empties, and caps a host list.
func sanitizeMusicHosts(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, h := range in {
		host := musicHostFromInput(h)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
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

// AddMusicHost normalizes and appends a domain to the allowlist (deduped,
// bounded). Reports whether it changed.
func (p *AssetPreferences) AddMusicHost(input string) bool {
	host := musicHostFromInput(input)
	if host == "" {
		return false
	}
	p.mu.Lock()
	if len(p.MusicHosts) >= musicHostsCap {
		p.mu.Unlock()
		return false
	}
	for _, h := range p.MusicHosts {
		if h == host {
			p.mu.Unlock()
			return false
		}
	}
	p.MusicHosts = append(p.MusicHosts, host)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// RemoveMusicHost drops a domain from the allowlist. Reports whether it changed.
func (p *AssetPreferences) RemoveMusicHost(host string) bool {
	host = musicHostFromInput(host)
	p.mu.Lock()
	for i, h := range p.MusicHosts {
		if h == host {
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
// history (and thus be Save-able): its host must be on the allowlist, and a
// Discord link additionally has to be an actual audio file. A bare track name
// (no host) or a server-hosted song on a non-listed domain returns false — it
// still plays, it just isn't recorded. Called once per song, never per frame.
func (p *AssetPreferences) MusicURLAllowed(rawURL string) bool {
	host, path := urlHostPath(rawURL)
	if host == "" {
		return false
	}
	// Curated built-in path rules (always on, audio-file only by construction)
	// so they apply even when a saved editable list predates them.
	for _, e := range builtinMusicEntries() {
		if e.matches(host, path) {
			return hasMusicFileExt(rawURL)
		}
	}
	p.mu.RLock()
	allowed := false
	for _, d := range p.MusicHosts {
		if hostMatches(host, d) {
			allowed = true
			break
		}
	}
	p.mu.RUnlock()
	if !allowed {
		return false
	}
	if isDiscordHost(host) {
		return hasMusicFileExt(rawURL) // Discord: audio files only
	}
	return true
}

// MusicURLDomain returns the normalized host of a /play link for grouping/labels
// (e.g. "files.catbox.moe" -> "catbox.moe" against the allowlist, else the bare
// host). A built-in path rule labels by its folder (miku.pizza/base/youtube) so
// those rips group apart from the rest of the host. "" when it isn't a URL.
func (p *AssetPreferences) MusicURLDomain(rawURL string) string {
	host, path := urlHostPath(rawURL)
	if host == "" {
		return ""
	}
	for _, e := range builtinMusicEntries() {
		if e.matches(host, path) {
			return e.label()
		}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, d := range p.MusicHosts {
		if hostMatches(host, d) {
			return d // collapse subdomains onto the listed domain
		}
	}
	return host
}
