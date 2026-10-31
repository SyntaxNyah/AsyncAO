package network

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// DefaultMasterServerURL is the AO master list endpoint, the same JSON API webAO and AO2-Client use.
const DefaultMasterServerURL = "https://servers.aceattorneyonline.com/servers"

// DefaultMasterServerURLs are polled and merged; DefaultMasterServerURL stays first for single-endpoint callers.
var DefaultMasterServerURLs = []string{
	DefaultMasterServerURL,
	"https://servers.umineko.online/servers",
}

const masterFetchTimeout = 10 * time.Second

const maxMasterResponseBytes = 4 << 20

// ServerSecurity classifies how a listed server can be reached; raw TCP is not supported at all.
type ServerSecurity int

const (
	// SecurityWSS has a TLS WebSocket port; green in the lobby.
	SecurityWSS ServerSecurity = iota
	// SecurityWS has plain WebSocket only; yellow in the lobby, joinable but unencrypted.
	SecurityWS
	// SecurityLegacyTCP has only a raw TCP port; black, pinned to the bottom, and not joinable.
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

	// Favorite pins the entry to the top; favorites missing from the master list come from DirectEntry.
	Favorite bool `json:"-"`
}

// DirectEntry synthesizes an entry for a server not on the master list.
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

// Security classifies the entry per the lobby's tiers.
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

// WebSocketURL prefers wss over ws; empty for legacy TCP-only entries.
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

// ParseDirectAddress keeps ws:// and wss://, and gives "host:port" ws:// (wss:// when secure); the port is
// required, as AO servers have no conventional default.
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

// A no-change lobby refresh costs a 304 with the ETag validator; one URL in practice, so a tiny capped table.
type cachedMasterList struct {
	etag string
	body []byte
}

const masterETagCacheCap = 4

var (
	masterETagMu    sync.Mutex
	masterETagCache = map[string]cachedMasterList{} // listURL → last validated payload
)

func FetchServerList(ctx context.Context, sockets *netproxy.Factory, listURL string) ([]ServerEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, masterFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("network: building master request: %w", err)
	}
	req.Header.Set("User-Agent", clientUserAgent)
	masterETagMu.Lock()
	prev, hasPrev := masterETagCache[listURL]
	masterETagMu.Unlock()
	if hasPrev && prev.etag != "" {
		req.Header.Set("If-None-Match", prev.etag)
	}
	resp, err := sockets.Client(0).Do(req)
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

func ParseServerList(data []byte) ([]ServerEntry, error) {
	var entries []ServerEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("network: parsing server list: %w", err)
	}
	return entries, nil
}

// MergeServerLists drops later duplicates, keyed by WebSocketURL or ip:port for legacy TCP-only entries.
func MergeServerLists(lists ...[]ServerEntry) []ServerEntry {
	var merged []ServerEntry
	seen := make(map[string]struct{})
	for _, list := range lists {
		for _, e := range list {
			key := e.WebSocketURL()
			if key == "" {
				if e.IP == "" {
					// No usable identity; real master JSON never has this.
					continue
				}
				key = fmt.Sprintf("%s:%d", e.IP, e.Port)
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, e)
		}
	}
	return merged
}

// FetchServerLists merges every endpoint, so a partial outage still shows a lobby; errors only if nothing came.
func FetchServerLists(ctx context.Context, sockets *netproxy.Factory, urls []string) ([]ServerEntry, error) {
	var lists [][]ServerEntry
	var errs []error
	for _, u := range urls {
		entries, err := FetchServerList(ctx, sockets, u)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		lists = append(lists, entries)
	}
	merged := MergeServerLists(lists...)
	if len(merged) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return merged, nil
}

// ProbeLatency measures connect time for the lobby's sort: it undercounts wss:// (no TLS), the first dial includes DNS,
// and through a proxy it includes the proxy, because a direct probe would show the user's address to every server.
func ProbeLatency(ctx context.Context, sockets *netproxy.Factory, addr string) (time.Duration, error) {
	start := time.Now()
	conn, err := sockets.DialContext(ctx, "tcp", addr)
	rtt := time.Since(start)
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		return 0, err
	}
	return rtt, nil
}

// DialTarget is the host:port the lobby would connect to (wss preferred), empty for unjoinable entries.
func (e ServerEntry) DialTarget() string {
	switch e.Security() {
	case SecurityWSS:
		return net.JoinHostPort(e.IP, strconv.Itoa(e.WSSPort))
	case SecurityWS:
		return net.JoinHostPort(e.IP, strconv.Itoa(e.WSPort))
	}
	return ""
}

// SortServers puts favorites first, then joinable servers by player count and name, and legacy TCP last.
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

// MergeFavorites flags phone book entries and appends missing favorites; the live description wins.
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
		// A favorite may use the plain ws URL while the list now advertises wss too, so match the ws form as well.
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
