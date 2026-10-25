package ui

import (
	"time"
)

// budgetState is App's "global per-frame budgets" banner group lifted into a named component: the fields
// used to sit flat on App, readable at depth 1 from every ui file. They are
// reached as a.budget.<field> now.
type budgetState struct {
	iconAskBudget int // per-frame icon demand allowance, reset each Frame
	// frameNow is this frame's single clock snapshot: animation clocks
	// (theme art, previews, splashes) read it instead of hitting the OS
	// clock per draw site.
	frameNow time.Time
	// musicTabDucked silences the single music stream because it belongs to a
	// BACKGROUNDED tab (cross-tab music continuity). It's App-level, not per-tab:
	musicTabDucked bool
	// musicOwnerKey is the serverKey of the tab whose track is in the single music
	// stream right now — stamped at the play sites (the active-tab EventMusic handler
	// and applyResumeDuck, both while a.serverKey is the tab starting the track).
	musicOwnerKey string
	// musicAwaitURL is the MusicURL the ACTIVE tab has requested while the single
	// stream is still ducked-foreign (musicTabDucked true): the duck may only lift
	// once THIS url is the one the mixer is actually playing, else the un-duck would
	// make a DIFFERENT (still-loading-away) track audible during the download gap —
	// the cross-tab "wrong song plays for a while" bug. "" = nothing awaited (the
	// common idle state). settleAwaitedMusic (per frame, after Audio.Frame drains the
	// delivery) lifts the duck when Audio.CurrentMusicURL() catches up to this. It's
	// cleared at every duck/stream reset (StopMusic, disconnect, tab close/park,
	// streaming-off) so a stale await can never un-duck a later foreign track — the
	// invariant is: stopped ⇒ not ducked, no await.
	musicAwaitURL string
	// musicAwaitSince stamps when musicAwaitURL was armed, so a never-arriving track
	// can't wedge the stream silent forever. Every OTHER release is event-driven
	// (settle-on-match, park, close, disconnect, stop, streaming-off) — but a fetch
	// that SILENTLY fails (a 404, an unsupported format, a URL the resolver builds
	// that CurrentMusicURL never equals, or a stalled download) fires no event, so
	// the awaited URL simply never becomes current and settleAwaitedMusic never
	// lifts the duck. settleAwaitedMusic uses this stamp as the last-resort release:
	musicAwaitSince time.Time
	// defaultOOC is the sticky AsyncAO<n> fallback OOC name, minted once
	// per run on first use (commands/macros need SOME name to send).
	defaultOOC string
	// nameOpts caches the courtroom name-picker dropdown options ("▾" then the
	// saved showname presets); nameOptsLen is the preset count it was built at, so
	// the per-frame chat row never re-copies the list (ensureNameOpts).
	nameOpts    []string
	nameOptsLen int
	// macroBind ≥ 0 while the settings macro editor captures a key.
	macroBind int
	// holdKeyArmed: the Settings hold-to-clear rebind is capturing the next key.
	holdKeyArmed bool
	// voicePTTBindArmed: the Voice settings push-to-talk rebind is capturing a key.
	voicePTTBindArmed bool
	// F3 perf HUD: toggle + the frame-time ring it graphs.
	perfHUD     bool
	frameDts    [perfHUDFrames]float32 // milliseconds
	frameDtIdx  int
	previewBase string
	// previewName caches the display name of the emote that produced previewBase,
	// stamped by previewEmote at SET time (it holds the *Emote right there) so the
	// preview-box caption never reverse-maps the URL — the old lookup linear-scanned
	// a.emotes building 1-2 URLs per emote EVERY frame a preview was up (per-frame
	// alloc churn on 4000+ emote packs). previewNameFor records which base the name
	// belongs to: consumers return the name only while it equals previewBase, so
	// every OTHER previewBase setter (bgpicker, wardrobe cycle) auto-invalidates
	// the caption without having to know these fields exist.
	previewName    string
	previewNameFor string
	previewFor     string    // base the preview clock was started for
	previewAt      time.Time // loop anchor — animated previews play, not freeze
	previewZoom    int       // magnifier factor (1 = fit; >1 shows a cursor-panned window)
	// Try-before-wear: cycle a previewed (non-worn) wardrobe character's
	// emotes in the preview box. previewChar guards a one-shot char.ini parse;
	// the capped anim/label slices drive the ‹ › buttons and arrow keys.
	previewChar     string
	previewAnims    []string
	previewLabels   []string
	previewEmoteIdx int
	// previewEmoteCycle records WHICH surface asked for the emote list, because
	// the two want different landings when the parse comes back:
	previewEmoteCycle bool
}
