package ui

// chatLogState owns chatlog state.
type chatLogState struct {
	icLog    []icEntry
	icLogSeq uint64 // bumps per mutation: keys the filter cache
	icScroll int32
	// icScrollVis is the EASED on-screen scroll offset (#22 smooth scrolling):
	// icScroll is the target (wheel/stick/bar writes it), the visual glides
	// toward it exponentially so wheel steps and the sticky-bottom's new-line
	// jumps stop teleporting. frameDtMs (stamped per drawn frame) drives the
	// time constant so the feel is frame-rate independent.
	icScrollVis float64
	frameDtMs   float32
	logSearch   string
	// Advanced log filters (#129): show ONLY entries whose speaker matches
	// logFilterShowname and whose position matches logFilterPos ("" = no
	// filter), stacked on top of the logSearch text match. Both inclusive.
	logFilterShowname string
	logFilterPos      string
	logFilterColor    int  // colour filter (0 = "Any color"); index into logColorFilterChoices (#49)
	logFilterOpen     bool // the filter sub-panel is expanded
	oocSeq            uint64
	oocLog            []string
	oocSpeakers       []string // parallel to oocLog: speaker per line ("" = system line); for name colours
	oocScroll         int32
	// areaWrap caches the word-wrapped ARUP row text (Issue #22: area names
	// and the player-count/status detail line previously never wrapped and
	// just clipped off the panel edge). Mirrors the icWrapped idiom — rebuilds
	// only when the ARUP state, search query, panel width, zoom, or font
	// chain actually changed, never per frame. One slot per query source, for
	// the same reason areaFiltered has two: the two drawers differ in WIDTH as
	// well as query, and re-wrapping every area twice a frame is the expensive
	// half of that thrash.
	areaWrap [areaQuerySlots]areaWrapCache
	logTab   int
	// debugOOC swaps the themed server_chatlog panel for the debug log — what
	// AO2's ooc_toggle button does (on_ooc_toggle_clicked, courtroom.cpp:5197:
	// show ui_debug_log, hide ui_server_chatlog, relabel the button).
	//
	// Stored INVERTED relative to AO2's server_ooc so the ZERO value is AO2's
	// initial state (server chat visible, button reads "Server"). A field that
	// defaulted true would have to be seeded in resetSessionState, and forgetting
	// that seed is a standing bug class in this file.
	debugOOC       bool
	debugOOCScroll int32 // the debug panel's own scroll offset (per tab, like the logs)
}
