package ui

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// lobbyState owns lobby state.
type lobbyState struct {
	env *env
	// --- lobby state ---
	masterEntries []network.ServerEntry // raw master list (no favorites)
	servers       []network.ServerEntry
	lobbyStatus   string
	lobbyFetching bool
	lobbyResult   chan lobbyFetch
	// lastLobbyRefreshAt is when the last master-list fetch STARTED (stamped in
	// RefreshServers, past its dedup gate, so every path arms it). App-global,
	// not sessionState: the lobby is a cross-tab surface. It feeds the on-open
	// auto-refresh cap — see lobbyAutoRefreshDue / noteScreenTransition.
	lastLobbyRefreshAt time.Time
	directInput        string
	directSecure       bool
	directSave         bool
	lobbyScroll        int32
	// AsyncAO Server Phone Book: a dedicated lobby page over the user's saved
	// (favorite) servers — manual add + connect, persisted in Favorites (survives
	// Reset, cleared by Wipe), with its own export/import.
	phoneBookPage bool
	// "Phone Fanat" chip (phone-book page only): a dim bottom-right button that
	// pops a quip when clicked. phoneFanatLine holds the currently-shown line and
	// phoneFanatShownAt stamps the click so it lingers for fanatShowDuration.
	phoneFanatLine    string
	phoneFanatShownAt time.Time
	// click-to-expand selection: first click opens the full description
	// under the row, a second click on the same row joins.
	selServer  int
	descLines  []string
	descLinks  []string // clickable URLs found in the description
	helpScroll int32    // ScreenServerHelp scroll position
	// Ping-sort (M7, opt-in via the lobby "Ping" button): pings holds each
	// joinable server's TCP-connect RTT by URL (0 = unprobed, <0 = unreachable);
	// pingMode sorts the lobby by it; pinging gates a re-probe; pingGen drops
	// stale results after a re-probe. A bounded worker pool feeds pingResult.
	pings    map[string]time.Duration
	pingRes  chan pingResult
	pingMode bool
	pinging  bool
	pingGen  int
	// #128 connection-quality chip (optional, off by default). A single background goroutine
	// pings the ACTIVE conn and stores the round-trip time; the frame loop (updatePingLoop)
	pingRTT     atomic.Int64       // round-trip nanoseconds (0 = unknown); goroutine→render
	pingConn    *protocol.Conn     // the conn the live loop targets (nil = no loop)
	pingCancel  context.CancelFunc // stops the live loop
	pingLabel   string             // cached tooltip text, rebuilt only when the ms bucket changes
	pingLabelMs int                // the ms the cached label was built for (-1 = none)
}
