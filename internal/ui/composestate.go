package ui

import "github.com/SyntaxNyah/AsyncAO/internal/courtroom"

// composeState owns compose state.
type composeState struct {
	// --- courtroom chrome ---
	icInput string
	// icPendingSent snapshots icInput at IC send time. AO2-Client parity
	// (courtroom.cpp handle_chatmessage): the input box clears only when the
	// server ECHOES our message back (CHAR_ID == m_cid) — tsuserver-family
	// servers silently swallow an MS that lands inside another message's
	// area-wide delay window (area.can_send_message), so clearing at send
	// time threw the whole typed line away whenever two people sent at once.
	icPendingSent string
	// (The old one-slot icUndoText/oocUndoText Ctrl+Z stash is gone: every
	// text field now has a real undo history — fieldhistory.go — whose
	// out-of-band detector catches the same clears, with redo.)
	oocInput string
	// oocName is THIS TAB's OOC chat name, seeded from the saved default in
	// resetSessionState — it lived on App and a name typed in one tab showed
	// up in every other (playtest). Only the Settings field writes the saved
	// default; the courtroom fields are tab-local.
	oocName string
	// IC message recall (#8): Up/Down in the IC field walk your recently-sent messages,
	// shell-style. icRecallIdx == -1 = editing the live draft; icRecallDraft stashes that
	// draft while you browse history. Per-tab (lives in sessionState).
	icSentHist    []string
	icRecallIdx   int
	icRecallDraft string
	// OOC message recall: the same Up/Down history walk for the OOC inputs (the OOC box,
	// the bottom OOC bar, and the themed OOC field — they all share oocInput). Its own ring,
	// separate from IC, so OOC and IC histories never bleed together.
	oocSentHist    []string
	oocRecallIdx   int
	oocRecallDraft string
	// Friends-tab PM composer (#friends): the selected friend + the message being
	// typed. Sends as "/pm <their uid> <message>" over OOC.
	pmTarget string
	pmInput  string
	// pmThreads is the per-conversation PM history (keyed by the partner's
	// canonical name via pmThreadKey) the Friends tab shows as a little DM thread.
	// Outgoing lines append on send; incoming ones are best-effort parsed out of
	// OOC (detectIncomingPM — server formats vary, a miss just stays in the OOC
	// log). Session-scoped (PMs are per-server) and bounded.
	pmThreads map[string][]pmLine
	// shownameOverride is the in-courtroom showname box: when non-blank
	// it wins over the persisted Settings showname for outgoing messages
	// (session-scoped — it never overwrites the saved one).
	shownameOverride string
	sidePref         string // OUR side (char.ini default, /pos override)
	// lastSentStyle is the sprite style we last TRANSMITTED this session. Send-on-
	// change appends the marker only when the current style differs (EncodeChangeMarker),
	// so the invisible run rides change-messages only — zero value = nothing sent yet,
	// so the first active style (re)transmits on a fresh connection.
	lastSentStyle courtroom.SpriteStyle
	// lastSentProfile is the cross-client character profile (#101 slice 2) we last
	// TRANSMITTED this session — send-on-change like lastSentStyle (a profile rarely
	// changes, so the invisible run rides at most our first post-join message).
	lastSentProfile courtroom.WireProfile
	// myStatus is our chosen presence flag (#M1); lastSentStatus is what we last
	// TRANSMITTED (send-on-change). Session-scoped — a fresh connect starts at None.
	myStatus       courtroom.Status
	lastSentStatus courtroom.Status
	iniWarmed      string // last char.ini hover-warmed (dedupe)
	icColor        int    // outgoing MS text_color (dropdown)
	// icExtColor selects an extended AsyncAO colour (#98), 1-based into
	// render.ExtColorAt (icExtColor-1); 0 = none, so the &App{} zero value stays
	// idle. Mutually exclusive with the wire palette / Rainbow / Random.
	icExtColor int
	// icCustomOn/icCustomRGB: the free hex chat colour (Tifera). The
	// exact colour ships as inline `\c#RRGGBB` markup with a nearest-standard
	// wire fallback (funColor); per-tab like every other colour pick. The last
	// pick also persists globally (Prefs.ICCustomColorRGB seeds a fresh tab).
	icCustomOn  bool
	icCustomRGB int
	// icColorSel freezes the IC field's selection so the colour dropdown can wrap
	// it (§3.8 select-and-colour, folded into the dropdown to match AO2's
	// on_text_color_changed). captureICColorSel snapshots it every frame BEFORE the
	// dropdown draws — while the field is still focused — because the dropdown's
	// open-click unfocuses the field (clearing the live Ctx selection) before the
	// pick lands a frame later. applyICColorPick consumes it. Session-scoped; the
	// zero value (active:false) is idle.
	icColorSel  icColorSel
	icImmediate bool  // MS Immediate: preanim plays without holding the text (session toggle)
	icPreanim   bool  // "Pre" toggle: send the selected emote's preanimation (session toggle; AUTO-FOLLOWS each emote pick — set when the new emote defines a usable preanim, mirroring AO2-Client ui_pre; the user can override until the next pick). Seeded true in resetSessionState so an untouched fresh join reproduces today's always-play-preanim behavior.
	icAdditive  bool  // #14 2.8 ADDITIVE: this message appends to your last (session toggle; shown only when the server advertises additive + the pref is on)
	icEffect    uint8 // #M5 sticky Text FX (courtroom.TextEffect*); 0 = off. Wraps every message you send.
	// icSlide is AO2's ui_slide (#21): the next message SLIDES the character into
	// position rather than cutting. Session-scoped like every other IC toggle. AO2
	// resets it after each send — "Slides can't be sticky for nausea reasons"
	// (courtroom.cpp:2362) — which the send site does, not this field.
	icSlide bool
	// icRealize / icShake are AO2's realization_state / screenshake_state (#21):
	// ONE-SHOT arms that ride the next message — the white flash and the camera
	// shake. AO2 clears both the instant the packet is built, next to
	// objection_state (courtroom.cpp:2327-2329), which is what stops a stuck arm
	// firing on every following line.
	icRealize bool
	icShake   bool
	// icShout is AO2's objection_state (courtroom.cpp:6048-6127) — the ARMED
	// interjection, riding the NEXT message exactly like icRealize and icShake:
	icShout int
	// modcallGuard is AO2's ui_guard (#21): silence the modcall ALERT for this
	// session. sessionState rather than a preference because AO2's ui_guard is a
	// bare QCheckBox with no Options read (courtroom.cpp:316-319) — it is a
	// per-session mute, not a setting, and a mod watching two servers wants it per
	// tab. The OOC line and the auto-clip still land; only the noise stops.
	modcallGuard bool
	// charDefaultSide is the side the ACTIVE character's char.ini declares — AO2's
	// default_side (courtroom.cpp:4723-4726 = get_char_side). Empty means "not
	// known yet or none declared", which defaultSide() reports as "wit", matching
	// get_char_side's own fallback (text_file_functions.cpp:480-483).
	// It MUST be cleared whenever the active character changes, before the new
	// char.ini lands. pos_remove is shown by comparing the current side against
	// this, and clicking it SENDS /pos — so a stale value is a wrong packet on the
	charDefaultSide string
	charBlips       string // char.ini blips/gender (outgoing default)
	// 2.10 custom shouts ([Shouts] in char.ini): customIdx −1 = the base
	// "custom" art, ≥ 0 indexes customShouts.
	customShouts []courtroom.CustomShout
	customIdx    int
	customName   string
	charINIBusy  bool
	// Stick flags: the logs FOLLOW new lines while true; scrolling up
	// releases, scrolling back to the bottom re-sticks. (The old "within
	// one line of the bottom" heuristic broke whenever one wrapped
	// message added several rows at once.)
	icStick  bool
	oocStick bool
	// icReadMark is the IC entry count when last caught up to the bottom; while
	// scrolled up, len(icLog)-icReadMark is the unread count the "N new" pill shows.
	icReadMark int
	icCountN   int    // M5 IC char counter: cached count + its string, reformatted
	icCountStr string // only when the length changes so the frame stays 0-alloc
}
