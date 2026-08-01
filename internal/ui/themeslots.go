package ui

// The courtroom_design.ini widget registry (#21).
//
// AO2 does not have a "list of keys". It has a list of WIDGETS, each of which
// asks set_size_and_pos for its own identifier and HIDES ITSELF when the theme
// is silent about it — qWarning + p_widget->hide(), AO2-Client
// src/courtroom.cpp:1328-1344. AsyncAO's themeLayoutKeys was the other shape: a
// whitelist of NAMES with no widget behind them. Two failure modes followed, and
// issue #21 reported both. A key the theme declared but the list omitted was
// structurally invisible (aceattorney2x declares ~100 rects; 35 were ingested).
// And a key the list held but nothing painted became a ghost box in the layout
// editor — music_search has been in themeLayoutKeys since it was written and has
// never had a single draw site.
//
// This table is the widget list. Every row names one AO2 element, says which
// coordinate system its rect lives in, which pass paints it, and whether anything
// paints it YET. themeLayoutKeys, themeButtonStems and the editor's editable set
// are all DERIVED from it, so a key cannot be ingested without a row, and a row
// cannot be draggable without a painter.
//
// Rows land as slotStateInert and graduate to slotStateHandDrawn as each binding
// commit of the #21 arc lands; the arc's last structural commit moves the
// hand-ordered draw bodies into the `draw` field and flips them to
// slotStateTable. That is why the state is data and not a bool.

import "github.com/veandco/go-sdl2/sdl"

// themeSlotRelSpace is the coordinate system a design rect is expressed in.
// AO2's own courtroom_design.ini says this out loud in its section banners
// ("**COORDINATE SYSTEM RELATIVE TO ...**"). AsyncAO hard-coded exactly one case
// of it — `key != "showname" && key != "message"` — and had no way to say the
// other two.
type themeSlotRelSpace uint8

const (
	// relCourtroom: window-absolute after the canvas transform (the default).
	relCourtroom themeSlotRelSpace = iota
	// relChatbox: child of ao2_chatbox (courtroom.cpp:3301-3303).
	relChatbox
	// relViewport: child of the stage (the two evidence icons).
	relViewport
	// relMusicDisplay: child of the music_display plate. The theme's own banner
	// says "WARNING: music_name x/y coordinates relative to music_display!".
	relMusicDisplay
)

// themeSlotState is how much of a slot exists.
type themeSlotState uint8

const (
	// slotStateInert: the rect is ingested for audit only. NOTHING paints here,
	// so the layout editor must not offer a box for it — that was the
	// music_search ghost. A bound-but-unpainted key draws nothing; it does not
	// fall back to AsyncAO's own arrangement.
	slotStateInert themeSlotState = iota
	// slotStateHandDrawn: a hand-ordered draw site owns this rect today.
	slotStateHandDrawn
	// slotStateTable: the slot's own draw func owns it.
	slotStateTable
)

// themeSlotPhase splits the themed pass at drawCourtroomModals' early return. A
// flat draw loop cannot reproduce a mid-sequence abort, so the eventual
// dispatcher runs two loops over disjoint phases with the modal check between.
type themeSlotPhase uint8

const (
	// phaseStage: painted BEFORE the modal return — the canvas, the viewport,
	// the chatbox and its children, and the toolbox fence.
	phaseStage themeSlotPhase = iota
	// phasePanels: painted after it.
	phasePanels
)

// themeSlot is one AO2 courtroom_design.ini element.
type themeSlot struct {
	// key is the identifier AO2 passes to set_size_and_pos.
	key string
	// alt is a SECOND identifier AO2 itself probes for the same widget, LOWER
	// priority than key. Exactly one exists upstream: ui_immediate reads
	// "immediate" first and falls back to "pre_no_interrupt". Both are ingested;
	// the draw site picks.
	alt string
	// art are the AOButton image stems AO2 loads for this element, in probe
	// order. Empty means the widget has no theme art and draws as a text chip,
	// which is what AOButton::setImage itself falls back to.
	art []string
	// rel is the coordinate system key's rect is expressed in.
	rel themeSlotRelSpace
	// phase is which half of the themed pass paints it.
	phase themeSlotPhase
	// state is how much of this slot exists.
	state themeSlotState
	// fixed marks a rect the layout editor must never offer: the design canvas
	// itself and the two chatbox children, which ride the chatbox rather than
	// being placed independently. This is the whole of the old layoutEditSkip.
	fixed bool
	// external marks a rect whose PAINTED geometry is not the design transform.
	// Only asyncao_tabbar: the server-tab strip is intrinsically sized by its
	// chips, so tabStripCacheRect overwrites the transform.
	external bool
	// draw is nil until the arc's harvest commit. It is a top-level func, never
	// a method value — a method value would allocate a closure the first time the
	// table is read, and the themed frame is gated at zero allocations.
	draw func(*App, sdl.Rect)
}

// themeSlotCap bounds the registry (hard rule 4). AO2's whole set_size_and_pos
// inventory is 86 identifiers and AsyncAO's courtroom scope is a subset plus the
// asyncao_* opt-ins. 160 leaves room for KFO's extras without ever being a real
// limit, and the table test fails loudly rather than silently truncating.
const themeSlotCap = 160

// themeSlots is the registry. Order is NOT load-bearing: themeLayoutKeys sorts,
// and the dispatcher orders by phase.
var themeSlots = [...]themeSlot{
	// ---- phaseStage: the canvas and its children ----------------------------
	{key: "courtroom", phase: phaseStage, state: slotStateHandDrawn, fixed: true},
	{key: "viewport", phase: phaseStage, state: slotStateHandDrawn},                               // courtroom.cpp:742
	{key: "ao2_chatbox", phase: phaseStage, state: slotStateHandDrawn},                            // courtroom.cpp:3301
	{key: "showname", rel: relChatbox, phase: phaseStage, state: slotStateHandDrawn, fixed: true}, // courtroom.cpp:3302
	{key: "message", rel: relChatbox, phase: phaseStage, state: slotStateHandDrawn, fixed: true},  // courtroom.cpp:3303
	{key: "asyncao_toolbox", phase: phaseStage, state: slotStateHandDrawn},                        // AsyncAO opt-in
	{key: "asyncao_tabbar", phase: phaseStage, state: slotStateHandDrawn, external: true},         // AsyncAO opt-in (#14); geometry from tabStripCacheRect

	// ---- phasePanels: logs and OOC ------------------------------------------
	{key: "ic_chatlog", phase: phasePanels, state: slotStateHandDrawn},       // courtroom.cpp:827
	{key: "server_chatlog", phase: phasePanels, state: slotStateHandDrawn},   // courtroom.cpp:834
	{key: "ms_chatlog", phase: phasePanels, state: slotStateHandDrawn},       // courtroom.cpp:831 — the DEBUG log, not a server_chatlog alias
	{key: "ooc_chat_message", phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:918
	{key: "ooc_chat_name", phase: phasePanels, state: slotStateHandDrawn},    // courtroom.cpp:920
	{key: "ooc_toggle", phase: phasePanels, state: slotStateHandDrawn},       // courtroom.cpp:1013 — swaps server_chatlog <-> the ms_chatlog debug log

	// ---- phasePanels: the jukebox -------------------------------------------
	{key: "music_list", phase: phasePanels, state: slotStateHandDrawn},                                            // courtroom.cpp:864 (areas) + :867 (music) — ONE rect, two widgets
	{key: "music_search", phase: phasePanels, state: slotStateHandDrawn},                                          // courtroom.cpp:923
	{key: "switch_area_music", art: []string{"switch_area_music"}, phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1060
	{key: "player_list", phase: phasePanels, state: slotStateInert},                                               // courtroom.cpp:879
	{key: "music_display", phase: phasePanels, state: slotStateHandDrawn},                                         // courtroom.cpp:888
	{key: "music_name", rel: relMusicDisplay, phase: phasePanels, state: slotStateHandDrawn, fixed: true},         // courtroom.cpp:885 — a CHILD of the plate (:171), so its rect is plate-relative and the editor must never offer it a box
	{key: "music_label", phase: phasePanels, state: slotStateHandDrawn},                                           // courtroom.cpp:988
	{key: "sfx_label", phase: phasePanels, state: slotStateHandDrawn},                                             // courtroom.cpp:990
	{key: "blip_label", phase: phasePanels, state: slotStateHandDrawn},                                            // courtroom.cpp:992
	{key: "music_slider", phase: phasePanels, state: slotStateHandDrawn},                                          // courtroom.cpp:1144
	{key: "sfx_slider", phase: phasePanels, state: slotStateHandDrawn},                                            // courtroom.cpp:1145
	{key: "blip_slider", phase: phasePanels, state: slotStateHandDrawn},                                           // courtroom.cpp:1146

	// ---- phasePanels: the IC bar --------------------------------------------
	{key: "ao2_ic_chat_message", phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:906
	{key: "ao2_ic_chat_name", phase: phasePanels, state: slotStateHandDrawn},    // courtroom.cpp:907
	{key: "text_color", phase: phasePanels, state: slotStateHandDrawn},          // courtroom.cpp:1138
	{key: "sfx_dropdown", phase: phasePanels, state: slotStateHandDrawn},        // courtroom.cpp:952
	{key: "sfx_remove", art: []string{"evidencex"}, phase: phasePanels, state: slotStateInert},
	{key: "effects_dropdown", phase: phasePanels, state: slotStateInert},     // courtroom.cpp:968
	{key: "emote_dropdown", phase: phasePanels, state: slotStateHandDrawn},   // courtroom.cpp:925
	{key: "iniswap_dropdown", phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:937
	{key: "iniswap_remove", art: []string{"evidencex"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "pos_dropdown", phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:928
	{key: "pos_remove", art: []string{"evidencex"}, phase: phasePanels, state: slotStateHandDrawn},
	// AO2 reads "immediate" first and falls back to "pre_no_interrupt".
	{key: "immediate", alt: "pre_no_interrupt", phase: phasePanels, state: slotStateHandDrawn},
	{key: "pre", phase: phasePanels, state: slotStateHandDrawn},          // courtroom.cpp:1065
	{key: "flip", phase: phasePanels, state: slotStateHandDrawn},         // courtroom.cpp:1086
	{key: "additive", phase: phasePanels, state: slotStateHandDrawn},     // courtroom.cpp:1089
	{key: "guard", phase: phasePanels, state: slotStateHandDrawn},        // courtroom.cpp:1092
	{key: "slide_enable", phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1096
	// showname_enable and casing are NOT placed by upstream AO2-Client:
	// ui_showname_enable is a bare declaration (courtroom.h) and there is no
	// set_size_and_pos for it or for "casing" anywhere in src/. They are
	// KFO-Client / 2.9-era elements, so the only citation we can honestly give is
	// the theme itself (aceattorney2x courtroom_design.ini:162 and :213).
	{key: "showname_enable", phase: phasePanels, state: slotStateHandDrawn},
	{key: "casing", phase: phasePanels, state: slotStateInert},

	// ---- phasePanels: shouts, judge strip, utility buttons ------------------
	{key: "hold_it", art: []string{"holdit"}, phase: phasePanels, state: slotStateHandDrawn},          // courtroom.cpp:995
	{key: "objection", art: []string{"objection"}, phase: phasePanels, state: slotStateHandDrawn},     // courtroom.cpp:1001
	{key: "take_that", art: []string{"takethat"}, phase: phasePanels, state: slotStateHandDrawn},      // courtroom.cpp:1007
	{key: "custom_objection", art: []string{"custom"}, phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1100
	{key: "realization", art: []string{"realization"}, phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1108
	{key: "screenshake", art: []string{"screenshake"}, phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1113
	{key: "mute_button", phase: phasePanels, state: slotStateInert},                                   // courtroom.cpp:1117
	{key: "witness_testimony", art: []string{"witnesstestimony"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "cross_examination", art: []string{"crossexamination"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "guilty", art: []string{"guilty"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "not_guilty", art: []string{"notguilty"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "defense_bar", phase: phasePanels, state: slotStateHandDrawn},                                  // courtroom.cpp:982
	{key: "prosecution_bar", phase: phasePanels, state: slotStateHandDrawn},                              // courtroom.cpp:985
	{key: "defense_plus", art: []string{"defplus"}, phase: phasePanels, state: slotStateHandDrawn},       // courtroom.cpp:1123 setImage("defplus")
	{key: "defense_minus", art: []string{"defminus"}, phase: phasePanels, state: slotStateHandDrawn},     // courtroom.cpp:1127 setImage("defminus")
	{key: "prosecution_plus", art: []string{"proplus"}, phase: phasePanels, state: slotStateHandDrawn},   // courtroom.cpp:1131 setImage("proplus")
	{key: "prosecution_minus", art: []string{"prominus"}, phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1135 setImage("prominus")
	{key: "pair_button", art: []string{"pair_button"}, phase: phasePanels, state: slotStateHandDrawn},    // courtroom.cpp:861 setImage("pair_button")
	{key: "call_mod", art: []string{"call_mod", "callmod"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "change_character", art: []string{"change_character"}, phase: phasePanels, state: slotStateHandDrawn},       // courtroom.cpp:1038 setImage("change_character")
	{key: "reload_theme", art: []string{"reload_theme"}, phase: phasePanels, state: slotStateHandDrawn},               // courtroom.cpp:1043 setImage("reload_theme")
	{key: "settings", art: []string{"courtroom_settings", "settings"}, phase: phasePanels, state: slotStateHandDrawn}, // courtroom.cpp:1053-1057: courtroom_settings, then the pre-2.10 "settings"

	// ---- phasePanels: emotes, evidence icons, clocks ------------------------
	{key: "emotes", phase: phasePanels, state: slotStateHandDrawn},                                    // emotes.cpp:45
	{key: "emote_left", art: []string{"arrow_left"}, phase: phasePanels, state: slotStateHandDrawn},   // emotes.cpp:47
	{key: "emote_right", art: []string{"arrow_right"}, phase: phasePanels, state: slotStateHandDrawn}, // emotes.cpp:50
	{key: "evidence_button", art: []string{"evidence_button", "evidencebutton"}, phase: phasePanels, state: slotStateHandDrawn},
	{key: "left_evidence_icon", rel: relViewport, phase: phasePanels, state: slotStateInert},
	{key: "right_evidence_icon", rel: relViewport, phase: phasePanels, state: slotStateInert},
	{key: "clock_0", phase: phasePanels, state: slotStateInert}, // courtroom.cpp loops clock_N
	{key: "clock_1", phase: phasePanels, state: slotStateInert},
	{key: "clock_2", phase: phasePanels, state: slotStateInert},
	{key: "clock_3", phase: phasePanels, state: slotStateInert},
	{key: "clock_4", phase: phasePanels, state: slotStateInert},

	// ---- AsyncAO-only opt-ins (#4b) -----------------------------------------
	// These have no AO2 equivalent. They are an AUTHOR OVERRIDE tier that WINS
	// over the AO2 key a theme declares — not a fallback, and not the layout
	// mechanism. asyncao_ic_react is deliberately absent: it is provably dead,
	// as theme_layout.go says in its own source.
	{key: "asyncao_ic_color", phase: phasePanels, state: slotStateHandDrawn},
	{key: "asyncao_ic_immediate", phase: phasePanels, state: slotStateHandDrawn},
	{key: "asyncao_ic_pre", phase: phasePanels, state: slotStateHandDrawn},
	{key: "asyncao_ic_sfx", phase: phasePanels, state: slotStateHandDrawn},
	{key: "asyncao_ic_emoji", phase: phasePanels, state: slotStateHandDrawn},
	{key: "asyncao_ic_fx", phase: phasePanels, state: slotStateHandDrawn},
}

// themeSlotDeferred names every courtroom_design.ini rect key AsyncAO KNOWINGLY
// does not ingest, with the reason. It is not a to-do list — it is the other half
// of the totality oracle: a rect key a real theme declares must be in exactly one
// of themeSlots, this map, or charSelectDefaultRects, or the fixture test fails.
// (Non-rect keys — colours, pairs, scalars — are filtered by ElementRect before
// the classification runs, so they need no set of their own.) Adding a key to a
// theme corpus without deciding about it is what made issue #21 possible.
var themeSlotDeferred = map[string]string{
	"chatbox":                  "legacy spelling: AO2 places the chatbox from ao2_chatbox unconditionally (courtroom.cpp:3301). Reading it too would double-place.",
	"ic_chat_message":          "legacy spelling: AO2 uses ao2_ic_chat_message (courtroom.cpp:906).",
	"ic_chat_name":             "legacy spelling: AO2 uses ao2_ic_chat_name (courtroom.cpp:907).",
	"chat_arrow":               "AO2 places a chatbox-relative animated idle arrow (courtroom.cpp:3383-3393). AsyncAO's typewriter has no themed arrow widget and #21 reports no symptom; row it when one exists.",
	"mute_list":                "AO2's mute list is a popup over the courtroom (courtroom.cpp:837). AsyncAO mutes from the players panel, which is chrome outside the canvas.",
	"pair_list":                "AO2's pairing UI is four in-canvas widgets (courtroom.cpp:840-857); AsyncAO's is a floatWin. Rehoming it is its own design conversation.",
	"pair_offset_spinbox":      "see pair_list.",
	"pair_vert_offset_spinbox": "see pair_list.",
	"pair_order_dropdown":      "see pair_list.",
	"casing_button":            "AO2's case-announce dialog (KFO/2.9 era; no set_size_and_pos in upstream src/). AsyncAO announces cases from the CM panel, which is chrome.",
	"area_list":                "AO2 places ui_area_list at the music_list rect (courtroom.cpp:864); the separate area_list key is declared by a few AOHD themes and read by no build we can cite.",
	"area_password":            "AO2's placement is commented out (courtroom.cpp:922) — the widget is dead upstream.",
	// The evidence panel. Its coordinate model is mixed: evidence_button is
	// courtroom-relative, evidence_name/buttons/left/right are relative to
	// evidence_background, and evidence_delete/image_name/image_button/x/ok/
	// description are relative to evidence_overlay, which is itself parented to
	// evidence_background. left_evidence_icon and right_evidence_icon are carved
	// OUT of this deferral — they are viewport-relative and have rows above.
	"evidence_background":   "evidence panel: deferred as a unit.",
	"evidence_buttons":      "evidence panel: deferred as a unit.",
	"evidence_name":         "evidence panel: deferred as a unit.",
	"evidence_left":         "evidence panel: deferred as a unit.",
	"evidence_right":        "evidence panel: deferred as a unit.",
	"evidence_present":      "evidence panel: deferred as a unit.",
	"evidence_overlay":      "evidence panel: deferred as a unit.",
	"evidence_delete":       "evidence panel: deferred as a unit.",
	"evidence_image_name":   "evidence panel: deferred as a unit.",
	"evidence_image_button": "evidence panel: deferred as a unit.",
	"evidence_x":            "evidence panel: deferred as a unit.",
	"evidence_ok":           "evidence panel: deferred as a unit.",
	"evidence_description":  "evidence panel: deferred as a unit.",
	"evidence_load":         "evidence panel: deferred as a unit.",
	"evidence_save":         "evidence panel: deferred as a unit.",
	"evidence_transfer":     "evidence panel: deferred as a unit.",
	"evidence_switch":       "evidence panel: deferred as a unit.",
}

// themeSlotIndex maps key (and alt) to its row. Built once at init from a fixed
// compile-time table, so it is bounded by construction (hard rule 4).
var themeSlotIndex = func() map[string]*themeSlot {
	m := make(map[string]*themeSlot, 2*len(themeSlots))
	for i := range themeSlots {
		s := &themeSlots[i]
		m[s.key] = s
		if s.alt != "" {
			m[s.alt] = s
		}
	}
	return m
}()

// themeSlotFor returns the row that owns a design key, or nil.
func themeSlotFor(key string) *themeSlot { return themeSlotIndex[key] }

// themeSlotRel is the coordinate system a design key's rect lives in. An unknown
// key is courtroom-relative, which is what the generic transform did before this
// table existed.
func themeSlotRel(key string) themeSlotRelSpace {
	if s := themeSlotIndex[key]; s != nil {
		return s.rel
	}
	return relCourtroom
}

// themeKeyEditable reports whether the themed layout editor may offer a drag box
// for a design key. It REPLACES layoutEditSkip, and it is derived rather than
// stored so the table and the invariant cannot disagree: a rect nothing paints
// must not be draggable, and the design canvas and the two chatbox-relative
// children are never independently placeable.
func themeKeyEditable(key string) bool {
	s := themeSlotIndex[key]
	if s == nil {
		return false
	}
	return s.state != slotStateInert && !s.fixed
}
