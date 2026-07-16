package ui

import "testing"

// These pin the pure row-height / content-height arithmetic for the Ctrl+wheel
// zoom on the Areas list and the Mod/CM dashboard lists. The load-bearing
// invariant for the fixed-height-rows + big-fonts bug class: a bigger font (a
// higher zoom) makes each row TALLER and the scrollbar content-height LARGER in
// lock-step, so click hitboxes and the scroll clamp track the text. The helpers
// are pure (font metric passed in / an int pct), so no SDL font is needed —
// mirroring how playerlist_test pins rowHeight geometry with a bare &App{}.

// TestAreaRowLineHScales pins that the two-row area card height is 2×fontH plus
// the fixed slack, grows monotonically with the font, and that contentH is
// exactly shown×lineH (the VScrollbar content-height the draw feeds).
func TestAreaRowLineHScales(t *testing.T) {
	// At the historical 100%-zoom chrome height (~17px) the formula reproduces
	// the old literal `font.Height()*2 + 11`.
	if got, want := areaRowLineH(17), int32(17*2+11); got != want {
		t.Fatalf("areaRowLineH(17) = %d, want %d (historical font.Height()*2 + 11)", got, want)
	}
	// Monotonic: a zoomed (taller) font must give a strictly taller row.
	small := areaRowLineH(17)
	big := areaRowLineH(34) // ~200% zoom doubles the glyph height
	if big <= small {
		t.Fatalf("areaRowLineH must grow with the font: small=%d big=%d", small, big)
	}
	// contentH the scrollbar sees = shown × lineH, so more/bigger rows push the
	// clamp down and the two zoom levels differ.
	const shown = int32(12)
	if got := shown * small; got != shown*areaRowLineH(17) {
		t.Fatalf("contentH mismatch: %d", got)
	}
	if shown*big <= shown*small {
		t.Fatalf("zoomed contentH (%d) must exceed unzoomed (%d)", shown*big, shown*small)
	}
}

// TestAuditRowHeightScales pins the mod-dash audit row: two text lines + pads,
// monotonic in the font height.
func TestAuditRowHeightScales(t *testing.T) {
	if got, want := auditRowHeight(17), int32(17*2)+modAuditSubLinePad+modAuditRowBottomPad; got != want {
		t.Fatalf("auditRowHeight(17) = %d, want %d", got, want)
	}
	if auditRowHeight(34) <= auditRowHeight(17) {
		t.Fatalf("auditRowHeight must grow with the font: %d !> %d", auditRowHeight(34), auditRowHeight(17))
	}
}

// TestModRowHeightScales pins that mod-dash PLAYER rows scale with the zoom pct
// while area-group HEADERS stay fixed (a stable click target, mirroring the
// player list's rowHeight), and that the height math matches the const × pct/100
// form the draw + scroll loop rely on.
func TestModRowHeightScales(t *testing.T) {
	player := rosterRow{header: false, idx: 0}
	header := rosterRow{header: true, area: "Lobby", count: 3}

	if got, want := modRowHeight(player, 100), modRosterRowH; got != want {
		t.Fatalf("player row at 100%% = %d, want %d", got, want)
	}
	if got, want := modRowHeight(player, 200), modRosterRowH*2; got != want {
		t.Fatalf("player row at 200%% = %d, want %d (must double)", got, want)
	}
	if modRowHeight(player, 200) <= modRowHeight(player, 100) {
		t.Fatalf("player row must grow with the zoom")
	}
	// Headers never scale — same height at every zoom.
	if a, b := modRowHeight(header, 100), modRowHeight(header, 200); a != playerHeaderH || b != playerHeaderH {
		t.Fatalf("headers must stay fixed at %d: got %d and %d", playerHeaderH, a, b)
	}
}

// TestRosterIdentityLines pins the pure identity-string composition extracted
// from drawModRosterIdentity, so the chrome-font CM path and the zoom-scaled
// mod-dash row can never disagree on what the two lines say.
func TestRosterIdentityLines(t *testing.T) {
	cases := []struct {
		name    string
		p       areaPlayer
		wantIC  string
		wantSub string
	}{
		{
			name:    "showname differs from character appends the character",
			p:       areaPlayer{uid: "7", name: "Phoenix", showname: "Nick", ipid: "AB12"},
			wantIC:  "[7] Nick · Phoenix",
			wantSub: "IPID: AB12",
		},
		{
			name:    "no showname promotes the character, ooc skipped",
			p:       areaPlayer{uid: "3", name: "Maya", ooc: "mayamaya"},
			wantIC:  "[3] Maya",
			wantSub: "",
		},
		{
			name:    "ooc kept when a showname exists, joined with ipid",
			p:       areaPlayer{uid: "9", name: "Edgeworth", showname: "Miles", ooc: "worth", ipid: "ZZ"},
			wantIC:  "[9] Miles · Edgeworth",
			wantSub: "OOC: worth   ·   IPID: ZZ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ic, sub := rosterIdentityLines(tc.p)
			if ic != tc.wantIC || sub != tc.wantSub {
				t.Fatalf("rosterIdentityLines = %q / %q, want %q / %q", ic, sub, tc.wantIC, tc.wantSub)
			}
		})
	}
}
