package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
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

// FetchServerList downloads and parses the master server list.
func FetchServerList(ctx context.Context, listURL string) ([]ServerEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, masterFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("network: building master request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network: fetching server list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("network: master server returned %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMasterResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("network: reading server list: %w", err)
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

// SortServers orders entries for the lobby: joinable servers first (green
// and yellow interleaved by player count, then name), legacy TCP-only
// entries always pinned to the bottom.
func SortServers(entries []ServerEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		aLegacy := !a.Joinable()
		bLegacy := !b.Joinable()
		if aLegacy != bLegacy {
			return bLegacy // joinable before legacy
		}
		if a.Players != b.Players {
			return a.Players > b.Players
		}
		return a.Name < b.Name
	})
}
