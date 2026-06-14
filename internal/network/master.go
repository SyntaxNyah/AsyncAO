package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// DefaultMasterServerURL is the canonical AO master server list endpoint
// (same JSON API webAO and AO2-Client use).
const DefaultMasterServerURL = "https://servers.aceattorneyonline.com/servers"

// masterFetchTimeout caps the server-list request.
const masterFetchTimeout = 10 * time.Second

// maxMasterResponseBytes bounds the list response defensively.
const maxMasterResponseBytes = 4 << 20

// ServerSecurity classifies how a listed server can be reached. AsyncAO is
// WebSocket-only: legacy raw-TCP servers are not supported at all.
type ServerSecurity int

const (
	// SecurityWSS — has a TLS WebSocket port. Green in the lobby: the
	// fastest and secure (https); TLS session resumption + HTTP/2-capable
	// asset hosts tend to live here too.
	SecurityWSS ServerSecurity = iota
	// SecurityWS — plain WebSocket only. Yellow in the lobby: joinable,
	// but the connection is unencrypted http.
	SecurityWS
	// SecurityLegacyTCP — only a raw TCP port. Black in the lobby, pinned
	// to the bottom, NOT joinable: server owners should upgrade their
	// software to WebSockets if they want people to join.
	SecurityLegacyTCP
)

// String names the tier for logs and the lobby UI.
func (s ServerSecurity) String() string {
	switch s {
	case SecurityWSS:
		return "secure websocket (wss)"
	case SecurityWS:
		return "insecure websocket (ws)"
	case SecurityLegacyTCP:
		return "legacy tcp (unsupported)"
	default:
		return "unknown"
	}
}

// ServerEntry is one master-list row (fields mirror the master JSON).
type ServerEntry struct {
	IP          string `json:"ip"`
	Port        int    `json:"port"`    // legacy TCP port — never dialed
	WSPort      int    `json:"ws_port"` // plain WebSocket
	WSSPort     int    `json:"wss_port"`
	Players     int    `json:"players"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// Favorite pins the entry to the very top of the lobby list. Set by the
	// lobby when merging the master list with persisted favorites; favorite
	// entries not present on the master list (private/direct-connect
	// servers) are synthesized with DirectEntry.
	Favorite bool `json:"-"`
}

// DirectEntry synthesizes a list entry for a favorite or direct-connect
// server that is not on the master list (private servers). The phone-book
// description rides along so the lobby stays informative offline.
func DirectEntry(name, wsURL, description string) (ServerEntry, error) {
	scheme, host, port, err := splitWSURL(wsURL)
	if err != nil {
		return ServerEntry{}, err
	}
	e := ServerEntry{IP: host, Name: name, Description: description, Favorite: true}
	if scheme == "wss" {
		e.WSSPort = port
	} else {
		e.WSPort = port
	}
	return e, nil
}

// Security classifies the entry per the lobby's green/yellow/black tiers.
func (e ServerEntry) Security() ServerSecurity {
	switch {
	case e.WSSPort > 0:
		return SecurityWSS
	case e.WSPort > 0:
		return SecurityWS
	default:
		return SecurityLegacyTCP
	}
}

// Joinable reports whether AsyncAO can connect (WebSocket-only client).
func (e ServerEntry) Joinable() bool {
	return e.Security() != SecurityLegacyTCP
}

// WebSocketURL returns the connection URL, preferring wss over ws. Empty for
// legacy TCP-only entries.
func (e ServerEntry) WebSocketURL() string {
	switch e.Security() {
	case SecurityWSS:
		return "wss://" + e.IP + ":" + strconv.Itoa(e.WSSPort)
	case SecurityWS:
		return "ws://" + e.IP + ":" + strconv.Itoa(e.WSPort)
	default:
		return ""
	}
}

// UnsupportedReason is the lobby message for black-tier entries.
const UnsupportedReason = "Legacy TCP servers are not supported. Server owners should upgrade their software to WebSockets if they want people to join."

// ParseDirectAddress normalizes direct-connect input for private servers:
//
//	"wss://host:port" → unchanged
//	"ws://host:port"  → unchanged
//	"host:port"       → "ws://host:port" (or wss:// when secure is set)
//
// The port is required — AO servers have no conventional default.
func ParseDirectAddress(input string, secure bool) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("network: empty direct-connect address")
	}
	if strings.HasPrefix(input, "ws://") || strings.HasPrefix(input, "wss://") {
		if _, _, _, err := splitWSURL(input); err != nil {
			return "", err
		}
		return input, nil
	}
	if strings.Contains(input, "://") {
		return "", fmt.Errorf("network: unsupported scheme in %q (use ws:// or wss://)", input)
	}
	host, port, err := net.SplitHostPort(input)
	if err != nil || host == "" {
		return "", fmt.Errorf("network: direct connect needs ip:port or url:port, got %q", input)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("network: invalid port in %q", input)
	}
	scheme := "ws"
	if secure {
		scheme = "wss"
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// splitWSURL dissects ws://host:port or wss://host:port.
func splitWSURL(wsURL string) (scheme, host string, port int, err error) {
	rest, ok := strings.CutPrefix(wsURL, "ws://")
	scheme = "ws"
	if !ok {
		rest, ok = strings.CutPrefix(wsURL, "wss://")
		scheme = "wss"
	}
	if !ok {
		return "", "", 0, fmt.Errorf("network: %q is not a ws:// or wss:// URL", wsURL)
	}
	rest = strings.TrimSuffix(rest, "/")
	h, p, err := net.SplitHostPort(rest)
	if err != nil || h == "" {
		return "", "", 0, fmt.Errorf("network: %q needs host:port", wsURL)
	}
	portNum, err := strconv.Atoi(p)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return "", "", 0, fmt.Errorf("network: invalid port in %q", wsURL)
	}
	return scheme, h, portNum, nil
}

// FetchServerList downloads and parses the master server list.
// Master-list ETag revalidation: the lobby Refresh button re-fetches the
// full list every press; with the validator a no-change refresh costs a
// 304 and zero payload bytes. One URL in practice — a tiny capped table.
type cachedMasterList struct {
	etag string
	body []byte
}

const masterETagCacheCap = 4

var (
	masterETagMu    sync.Mutex
	masterETagCache = map[string]cachedMasterList{} // listURL → last validated payload
)

func FetchServerList(ctx context.Context, listURL string) ([]ServerEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, masterFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("network: building master request: %w", err)
	}
	masterETagMu.Lock()
	prev, hasPrev := masterETagCache[listURL]
	masterETagMu.Unlock()
	if hasPrev && prev.etag != "" {
		req.Header.Set("If-None-Match", prev.etag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network: fetching server list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && hasPrev {
		return ParseServerList(prev.body)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("network: master server returned %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMasterResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("network: reading server list: %w", err)
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		masterETagMu.Lock()
		if len(masterETagCache) >= masterETagCacheCap {
			// Tiny table; wholesale reset beats eviction bookkeeping.
			masterETagCache = map[string]cachedMasterList{}
		}
		masterETagCache[listURL] = cachedMasterList{etag: etag, body: data}
		masterETagMu.Unlock()
	}
	return ParseServerList(data)
}

// ParseServerList decodes the master JSON.
func ParseServerList(data []byte) ([]ServerEntry, error) {
	var entries []ServerEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("network: parsing server list: %w", err)
	}
	return entries, nil
}

// ProbeLatency measures the TCP connect round-trip to addr ("host:port") — a
// rough relative-latency probe for the lobby's "connect time" sort. It is NOT
// an ICMP ping and undercounts wss:// (no TLS handshake); the first dial to a
// host also folds in DNS. Honors ctx (the caller bounds it with a timeout).
func ProbeLatency(ctx context.Context, addr string) (time.Duration, error) {
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	rtt := time.Since(start)
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		return 0, err
	}
	return rtt, nil
}

// DialTarget is the host:port the lobby would connect to (wss preferred), for
// ProbeLatency. Empty for legacy/unjoinable entries.
func (e ServerEntry) DialTarget() string {
	switch e.Security() {
	case SecurityWSS:
		return net.JoinHostPort(e.IP, strconv.Itoa(e.WSSPort))
	case SecurityWS:
		return net.JoinHostPort(e.IP, strconv.Itoa(e.WSPort))
	}
	return ""
}

// SortServers orders entries for the lobby: favorites pinned to the very
// top, then joinable servers (green and yellow interleaved by player count,
// then name), legacy TCP-only entries always pinned to the bottom.
func SortServers(entries []ServerEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		aLegacy := !a.Joinable()
		bLegacy := !b.Joinable()
		if aLegacy != bLegacy {
			return bLegacy // joinable before legacy
		}
		if !aLegacy && a.Favorite != b.Favorite {
			return a.Favorite // stars float joinable favorites to the top
		}
		if a.Players != b.Players {
			return a.Players > b.Players
		}
		return a.Name < b.Name
	})
}

// MergeFavorites flags master-list entries whose connection URL is in the
// phone book and appends synthesized entries for favorites missing from the
// list (private/direct-connect servers), descriptions included. The master
// list's live description wins for servers it still carries.
func MergeFavorites(entries []ServerEntry, favs []config.FavoriteServer) []ServerEntry {
	byURL := make(map[string]config.FavoriteServer, len(favs))
	for _, f := range favs {
		byURL[f.URL] = f
	}
	seen := map[string]struct{}{}
	for i := range entries {
		url := entries[i].WebSocketURL()
		if url == "" {
			continue
		}
		if _, ok := byURL[url]; ok {
			entries[i].Favorite = true
			seen[url] = struct{}{}
		}
		// A server may be favorited via its plain-ws URL while the master
		// list now advertises wss too; match the ws form as well.
		if entries[i].WSPort > 0 {
			alt := "ws://" + entries[i].IP + ":" + strconv.Itoa(entries[i].WSPort)
			if _, ok := byURL[alt]; ok {
				entries[i].Favorite = true
				seen[alt] = struct{}{}
			}
		}
	}
	for url, fav := range byURL {
		if _, ok := seen[url]; ok {
			continue
		}
		if e, err := DirectEntry(fav.Name, url, fav.Description); err == nil {
			entries = append(entries, e)
		}
	}
	return entries
}
