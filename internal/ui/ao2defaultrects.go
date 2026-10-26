package ui

// AO2's stock `default` theme geometry, as a fallback tier (#21).
//
// AO2 resolves a missing design key by falling through to the bundled
// base/themes/default (get_config_value walks theme → subtheme → default). A
// STREAMING client has no such tree on disk, so before this table a key the theme
// omitted resolved to nothing at all. That is only safe while every widget also
// has an AsyncAO-native place to fall back to — and the governing rule for issue
// #21 is that it does not: inside a theme's canvas a missing rect means "draw
// nothing", not "use AsyncAO's own arrangement".
//
// This table is what makes that rule survivable. A theme that declares `courtroom`
// and `viewport` and nothing else still gets a complete, coherent AO2 courtroom,
// exactly as it would under AO2 itself, instead of an empty canvas.
//
// It is transcribed from AO2-Client/base/themes/default/courtroom_design.ini —
// machine-extracted from that file rather than typed, then filtered to the keys
// the themeSlots registry actually reads. The precedent and the reasoning are
// charSelectDefaultRects (charselectlayout.go), which solved the same problem for
// the character-select screen.
//
// Keys deliberately absent:
//   - player_list, which the stock default does not declare either.
//   - the asyncao_* opt-ins, which are AsyncAO's own and have no AO2 default.
//   - `immediate`, which AO2's stock file spells `pre_no_interrupt`; the registry
//     row carries both and resolves the alt.

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// ao2CanvasKey / ao2ViewportKey are the two keys themeLayoutIn requires before it
// will build a themed canvas at all (theme_layout.go). Named because the backstop
// below gates on exactly that pair, and the two must never drift apart.
const (
	ao2CanvasKey   = "courtroom"
	ao2ViewportKey = "viewport"
)

// ao2DefaultCanvas is the canvas the table's coordinates were authored in —
// line 3 of AO2's own base/themes/default/courtroom_design.ini. Every rect below
// is a position ON THIS CANVAS, and that is the fact the placement rule turns on:
// carried onto a canvas of another size they are not a layout, just numbers.
var ao2DefaultCanvas = theme.Rect{X: 0, Y: 0, W: 714, H: 579}

// applyAO2DefaultRects fills AO2's per-key default for every courtroom key the
// theme was silent about.
//
// GATED ON THE THEME DECLARING A CANVAS OF ITS OWN, which is the whole safety
// argument: the stock table declares courtroom AND viewport, so an ungated fill
// would hand those two keys to a theme directory that has no
// courtroom_design.ini at all — and themeLayoutIn validates on exactly that pair.
// Every user whose theme ships no design INI would be silently moved onto AO2's
// 714x579 layout. This reproduces AO2's fallback for a theme that HAS a design
// INI; it is not a way to invent one.
//
// SECOND GATE: a default is refused when it would land on a widget the theme's
// author placed themselves, unless the theme is working on AO2's own canvas.
//
// The reason is generational, and it is the defect this gate was added for.
// Widgets arrive in AO2 over time — slide_enable is a 2.11 addition — and the
// enormous majority of themes in circulation were authored for 2.10 or earlier,
// so they are silent about it through no fault of their own. AO2 fills that
// silence from its own default file and gets away with it because those
// coordinates are native there. AsyncAO carries the same numbers onto a canvas
// the theme chose: aceattorney2x is 944x600, VA-11 HALL-A is 1262x700. On those,
// slide_enable's stock {200,464} is not "where the Slide box goes" — it is a
// point that happens to sit on top of the author's Pre row, their colour
// dropdown and their music panel. Reported from a live run as a Slide checkbox
// colliding with the toggle row, on two different themes.
//
// Scaling the rect instead was rejected: a proportional guess is still a guess,
// and it would land somewhere equally unrelated to the author's arrangement,
// only less obviously wrong. Drawing nothing is what AO2's own rule for a
// missing rect says (set_size_and_pos hides a widget the theme is silent about,
// courtroom.cpp:1334-1338) and what #21's rule (e) demands.
//
// On AO2's OWN canvas the defaults go in verbatim, overlaps included — the stock
// file overlaps itself (slide_enable 200,464 against casing 200,470; pre 5,400
// against flip 64,400), so a theme that adopts that canvas is asking for exactly
// that arrangement and must get it.
func applyAO2DefaultRects(layout map[string]theme.Rect) {
	canvas, ok := layout[ao2CanvasKey]
	if !ok {
		return
	}
	if _, ok := layout[ao2ViewportKey]; !ok {
		return
	}
	// Only the AUTHOR's rects count as occupied. Filling in key order would make
	// the result depend on map iteration order — one default could block another —
	// so the occupancy set is snapshotted before anything is added.
	native := canvas.W == ao2DefaultCanvas.W && canvas.H == ao2DefaultCanvas.H
	authored := make([]theme.Rect, 0, len(layout))
	if !native {
		for key, r := range layout {
			// courtroom and viewport are CONTAINERS, not widgets: the canvas
			// contains everything by definition and AO2 overlays the chatbox, the
			// shouts and the desk on the stage on purpose (courtroom.cpp:3301).
			// Counting either as occupied would refuse almost every stock rect and
			// leave a theme that declares only those two — the exact case the
			// backstop exists for — with no courtroom at all.
			if key == ao2CanvasKey || key == ao2ViewportKey {
				continue
			}
			if r.Valid() {
				authored = append(authored, r)
			}
		}
	}
	for key, def := range ao2DefaultDesignRects {
		if _, declared := layout[key]; declared {
			continue
		}
		if !native && rectHitsAny(def, authored) {
			continue
		}
		layout[key] = def
	}
}

// rectHitsAny reports whether r overlaps any of rs. Plain integer comparisons on
// a slice built once per theme apply — this is not a draw path.
func rectHitsAny(r theme.Rect, rs []theme.Rect) bool {
	for _, o := range rs {
		if r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H {
			return true
		}
	}
	return false
}

// ao2DefaultDesignRects is AO2's stock default courtroom_design.ini, restricted
// to the keys themeSlots reads.
var ao2DefaultDesignRects = map[string]theme.Rect{
	"additive":            {X: 114, Y: 400, W: 80, H: 21},
	"ao2_chatbox":         {X: 0, Y: 114, W: 256, H: 78},
	"ao2_ic_chat_message": {X: 0, Y: 192, W: 256, H: 23},
	"ao2_ic_chat_name":    {X: 200, Y: 444, W: 78, H: 23},
	"blip_label":          {X: 282, Y: 557, W: 41, H: 16},
	"blip_slider":         {X: 326, Y: 558, W: 140, H: 16},
	"call_mod":            {X: 104, Y: 547, W: 64, H: 23},
	"casing":              {X: 200, Y: 470, W: 80, H: 21},
	"change_character":    {X: 5, Y: 520, W: 120, H: 23},
	"clock_0":             {X: 90, Y: 2, W: 70, H: 14},
	"clock_1":             {X: 2, Y: 2, W: 70, H: 14},
	"clock_2":             {X: 184, Y: 2, W: 70, H: 14},
	"clock_3":             {X: 2, Y: 18, W: 70, H: 14},
	"clock_4":             {X: 184, Y: 18, W: 70, H: 14},
	"courtroom":           {X: 0, Y: 0, W: 714, H: 579},
	"cross_examination":   {X: 290, Y: 425, W: 85, H: 42},
	"custom_objection":    {X: 250, Y: 221, W: 76, H: 28},
	"defense_bar":         {X: 5, Y: 476, W: 187, H: 9},
	"defense_minus":       {X: 5, Y: 476, W: 9, H: 9},
	"defense_plus":        {X: 183, Y: 476, W: 9, H: 9},
	"effects_dropdown":    {X: 330, Y: 352, W: 89, H: 22},
	"emote_dropdown":      {X: 5, Y: 380, W: 105, H: 20},
	"emote_left":          {X: 5, Y: 344, W: 60, H: 32},
	"emote_right":         {X: 428, Y: 344, W: 60, H: 32},
	"emotes":              {X: 5, Y: 253, W: 490, H: 98},
	"evidence_button":     {X: 627, Y: 322, W: 85, H: 18},
	"flip":                {X: 64, Y: 400, W: 51, H: 21},
	"guard":               {X: 200, Y: 490, W: 61, H: 21},
	"guilty":              {X: 380, Y: 425, W: 85, H: 42},
	"hold_it":             {X: 10, Y: 221, W: 76, H: 28},
	"ic_chatlog":          {X: 260, Y: 0, W: 231, H: 220},
	"iniswap_dropdown":    {X: 100, Y: 352, W: 89, H: 20},
	"iniswap_remove":      {X: 78, Y: 352, W: 20, H: 20},
	"left_evidence_icon":  {X: 13, Y: 13, W: 70, H: 70},
	"message":             {X: 10, Y: 13, W: 242, H: 57},
	"ms_chatlog":          {X: 490, Y: 1, W: 224, H: 277},
	"music_display":       {X: 490, Y: 0, W: 224, H: 26},
	"music_label":         {X: 282, Y: 517, W: 41, H: 16},
	"music_list":          {X: 490, Y: 342, W: 224, H: 236},
	"music_name":          {X: 0, Y: 0, W: 224, H: 26},
	"music_search":        {X: 490, Y: 319, W: 100, H: 23},
	"music_slider":        {X: 326, Y: 518, W: 140, H: 16},
	"mute_button":         {X: 155, Y: 425, W: 42, H: 42},
	"not_guilty":          {X: 380, Y: 380, W: 85, H: 42},
	"objection":           {X: 90, Y: 221, W: 76, H: 28},
	"ooc_chat_message":    {X: 492, Y: 281, W: 222, H: 19},
	"ooc_chat_name":       {X: 492, Y: 300, W: 85, H: 19},
	"ooc_toggle":          {X: 580, Y: 300, W: 133, H: 19},
	"pair_button":         {X: 104, Y: 425, W: 42, H: 42},
	"pos_dropdown":        {X: 222, Y: 380, W: 60, H: 20},
	"pos_remove":          {X: 200, Y: 380, W: 20, H: 20},
	"pre":                 {X: 5, Y: 400, W: 80, H: 21},
	"pre_no_interrupt":    {X: 200, Y: 400, W: 80, H: 21},
	"prosecution_bar":     {X: 5, Y: 492, W: 187, H: 9},
	"prosecution_minus":   {X: 5, Y: 492, W: 9, H: 9},
	"prosecution_plus":    {X: 183, Y: 492, W: 9, H: 9},
	"realization":         {X: 5, Y: 425, W: 42, H: 42},
	"reload_theme":        {X: 5, Y: 547, W: 94, H: 23},
	"right_evidence_icon": {X: 173, Y: 13, W: 70, H: 70},
	"screenshake":         {X: 55, Y: 425, W: 42, H: 42},
	"server_chatlog":      {X: 490, Y: 1, W: 224, H: 277},
	"settings":            {X: 130, Y: 520, W: 60, H: 23},
	"sfx_dropdown":        {X: 220, Y: 352, W: 89, H: 20},
	"sfx_label":           {X: 282, Y: 537, W: 41, H: 16},
	"sfx_remove":          {X: 198, Y: 352, W: 20, H: 20},
	"sfx_slider":          {X: 326, Y: 538, W: 140, H: 16},
	"showname":            {X: 1, Y: 0, W: 46, H: 15},
	"showname_enable":     {X: 200, Y: 420, W: 80, H: 21},
	"slide_enable":        {X: 200, Y: 464, W: 80, H: 21},
	"switch_area_music":   {X: 590, Y: 319, W: 35, H: 23},
	"take_that":           {X: 170, Y: 221, W: 76, H: 28},
	"text_color":          {X: 115, Y: 380, W: 80, H: 20},
	"viewport":            {X: 0, Y: 0, W: 256, H: 192},
	"witness_testimony":   {X: 290, Y: 380, W: 85, H: 42},
}
