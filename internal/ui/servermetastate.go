package ui

import "time"

// serverMetaState owns srvMeta state.
type serverMetaState struct {
	amICMNow   bool // cached amICM() — refreshed on ARUP/PU events, read per-frame by the
	serverName string
	serverKey  string // ws URL: keys the per-server warm state in prefs
	// logSession is THIS TAB's transcript identity: a process-unique ordinal minted
	// by resetSessionState, so two tabs are two sessions even when they are the same
	// server. Detailed logging keyed on serverName alone, which is why two tabs on
	// one server interleaved into ONE file (the reported defect) — and, worse, two
	// tabs on the SAME server had no way to tell their lines apart afterwards.
	// It lives on sessionState, not App, because park/activate MOVE the whole struct
	// between the live slot and the tab: the id then follows its tab automatically,
	// and a tab that is backgrounded and brought back keeps writing to the same file
	logSession   int
	connAt       time.Time // session start (Rich Presence elapsed timer)
	curArea      string    // last area WE clicked (Rich Presence, best-effort)
	presenceInit bool      // false until the first lobby presence push (so "Playing AsyncAO" shows on launch, not only in-court)
	// Per-area IC scrollback (opt-in): areaLogs holds each visited area's saved
	// icLog, areaLogOrder is the visit order for bounded FIFO eviction
	// (areaLogCacheMax). Driven by the area-click switch; both park per tab.
	areaLogs     map[string][]icEntry
	areaLogOrder []string
	lastICSend   time.Time // chat_ratelimit window
	// manifestFor is a RE-SEED SIGNAL for the CURRENT session origin: Settings
	// writes "" to it to force fetchManifestAsync to re-fetch the connected
	// server's extensions.json right after a profile / auto-detect toggle change.
	manifestFor string
	// manifestSeeded records every origin whose extensions.json has already been
	// fetched + seeded this session, so the session connect path AND the two
	// diagnostic paths (content report, export loader) each seed a given origin
	// at most once. Bounded by the number of DISTINCT asset origins visited in a
	// session — a handful in practice (§17.4). Replaces the old single-string
	// dedupe, which could only remember the ONE session origin and so never
	// deduped a recording's (different) origin.
	manifestSeeded map[string]struct{}
	// manifestStatus records, per asset HOST, how that host's extensions.json
	// probe ended (applied / absent / failed). Settings → Formats reads it to say
	// out loud whether the server publishes a format manifest — the single fact
	// that decides whether a player needs to touch anything at all. Same bound and
	// same render-thread-only rule as manifestSeeded above.
	manifestStatus map[string]seedStatus
	// themeBound is this server's bound theme ("" = the global pick);
	// loaded from ServerWarmInfo.Theme on connect, wins in
	// applyThemeAsync while the session lives.
	themeBound string
	// rehearsal marks the offline cached-asset browser (no connection;
	// the manager's network gate is closed while set). Rehearsal never
	// parks — backgrounding it would hold the global gate closed.
	rehearsal bool
}
