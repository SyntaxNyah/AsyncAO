package ui

// Tests for the Players tab's ARUP status/lock/CM display (the user's ask:
// "Add Area statuses into player tab so stuff like casing, spectator, RP,
// Locked, gaming etc."). The data (courtroom.AreaInfo) and the text/colour
// formatting (arupStatusText, areaStatusColor) already existed for the Areas
// tab — these gates pin that the Players tab READS them, reads them LIVE
// (never from a value baked into the roster memo), and shares the one
// formatter/vocabulary rather than forking a second copy of it.

import (
	"bytes"
	"go/ast"
	"go/token"
	"image"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestArupStatusTextFormatsARUPFields pins arupStatusText's field order and
// its degrade cases: nothing reported (Players/Status/CM/Lock all at their
// courtroom.AreaInfo zero/unknown values) yields "", each field is its own
// "  ·  "-joined segment, LOCKED and SPECTATABLE are mutually exclusive
// (Lock only ever holds one value on the wire), and a "FREE" CM (no case
// master) is excluded exactly like the Areas tab's own detail line always
// excluded it.
func TestArupStatusTextFormatsARUPFields(t *testing.T) {
	if got := arupStatusText(nil); got != "" {
		t.Errorf("arupStatusText(nil) = %q, want \"\"", got)
	}
	if got := arupStatusText(&courtroom.AreaInfo{Players: -1}); got != "" {
		t.Errorf("unreported ARUP = %q, want \"\" (never a stray separator)", got)
	}
	if got := arupStatusText(&courtroom.AreaInfo{Status: "CASING"}); got != "  ·  CASING" {
		t.Errorf("status only = %q, want %q", got, "  ·  CASING")
	}
	if got := arupStatusText(&courtroom.AreaInfo{Lock: "LOCKED"}); got != "  ·  locked" {
		t.Errorf("locked = %q, want %q", got, "  ·  locked")
	}
	if got := arupStatusText(&courtroom.AreaInfo{Lock: "SPECTATABLE"}); got != "  ·  spectatable" {
		t.Errorf("spectatable = %q, want %q", got, "  ·  spectatable")
	}
	if got := arupStatusText(&courtroom.AreaInfo{CM: "FREE"}); got != "" {
		t.Errorf("CM=FREE must not show as a case master, got %q", got)
	}
	if got := arupStatusText(&courtroom.AreaInfo{CM: "Phoenix, Edgeworth"}); got != "  ·  CM: Phoenix, Edgeworth" {
		t.Errorf("CM = %q, want %q", got, "  ·  CM: Phoenix, Edgeworth")
	}
	full := arupStatusText(&courtroom.AreaInfo{Status: "CASING", Lock: "LOCKED", CM: "Phoenix"})
	if want := "  ·  CASING  ·  locked  ·  CM: Phoenix"; full != want {
		t.Errorf("all fields = %q, want %q (status, then lock, then CM — ARUP field order)", full, want)
	}
}

// TestArupStatusTextSharedAcrossAreaAndPlayerTabs is the encapsulation gate
// for the extraction: it proves, by parsing the real sources (funcBodySource
// + containsCall, internal/ui/srcgate_test.go — the package's own idiom, not
// a copy of it), that the Areas tab (areaWrapped, screens.go) and both
// Players-tab surfaces (drawAreaHeaderRow's /gas header, and
// drawCurrentAreaStatusStrip's flat /ga line, playerlist.go) all call
// arupStatusText. A future edit that "fixes" one tab by re-deriving the
// status/lock/CM text inline — the exact drift CLAUDE.md's mirror-test
// warning describes — fails this test the moment it's written; it does not
// require inspecting the diff.
func TestArupStatusTextSharedAcrossAreaAndPlayerTabs(t *testing.T) {
	cases := []struct{ file, fn string }{
		{"screens.go", "areaWrapped"},
		{"playerlist.go", "drawAreaHeaderRow"},
		{"playerlist.go", "drawCurrentAreaStatusStrip"},
	}
	for _, tc := range cases {
		body := funcBodySource(t, tc.file, tc.fn)
		if !containsCall(body, "arupStatusText") {
			t.Errorf("%s: %s never calls arupStatusText — it has forked its own status/lock/CM text "+
				"instead of sharing the Areas tab's formatter", tc.file, tc.fn)
		}
	}
}

// TestCurrentAreaStatusStripOnlyDrawnWhenRosterIsFlat pins the product
// decision that the always-visible current-area status strip is a FLAT-
// roster-only affordance: a /gas roster already carries this text per area,
// on drawAreaHeaderRow, so drawPlayerList must gate the strip on the same
// `!multiArea` this file computes for the Rooms button — never call it
// unconditionally (which would double the text for the current area's own
// group) and never call it only somewhere unreachable.
func TestCurrentAreaStatusStripOnlyDrawnWhenRosterIsFlat(t *testing.T) {
	body := funcBodySource(t, "playerlist.go", "drawPlayerList")
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		unary, ok := ifs.Cond.(*ast.UnaryExpr)
		if !ok || unary.Op != token.NOT {
			return true
		}
		id, ok := unary.X.(*ast.Ident)
		if !ok || id.Name != "multiArea" {
			return true
		}
		found = true
		if !containsCall(ifs.Body, "drawCurrentAreaStatusStrip") {
			t.Error("drawPlayerList's `if !multiArea` branch no longer calls drawCurrentAreaStatusStrip — " +
				"the flat-roster ARUP status line would either never show or show unconditionally " +
				"(double-showing it once the /gas per-area header already carries it)")
		}
		return true
	})
	if !found {
		t.Fatal("drawPlayerList no longer branches on `!multiArea` — the flat/grouped gating this test pins is gone")
	}
}

// allBlackPix reports whether every pixel in img is pure black (R=G=B=0):
// what render.CaptureTarget.Capture clears to before draw runs, so "still all
// black" is a robust, font-metric-independent proxy for "nothing was drawn".
func allBlackPix(img *image.RGBA) bool {
	for i := 0; i+2 < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 || img.Pix[i+1] != 0 || img.Pix[i+2] != 0 {
			return false
		}
	}
	return true
}

// arupPixelFixture stages a real (dummy-video, software) Ctx and an offscreen
// capture target so the ARUP status draw functions can be driven for real and
// read back — the sliderthumbcontrast_test.go idiom: drive the REAL draw
// path into a render target with the painted pixels read back, rather than
// re-checking the text-building arithmetic in isolation.
func arupPixelFixture(t *testing.T, w, h int32) (*App, *render.CaptureTarget, sdl.Rect, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	ct, err := render.NewCaptureTarget(ren, w, h)
	if err != nil {
		cleanup()
		t.Skipf("capture target unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.ctx.mouseX, a.ctx.mouseY = -1000, -1000 // parked off-panel: no hover/border noise between the two renders
	a.playerPct = 100
	return a, ct, sdl.Rect{X: 0, Y: 0, W: w, H: h}, func() {
		ct.Close()
		cleanup()
	}
}

// TestDrawCurrentAreaStatusStripShowsARUPStatus is the flat-roster half: a
// bare strip before ARUP has reported anything (matching the Areas tab's own
// empty-detail degrade), non-blank once a status lands.
func TestDrawCurrentAreaStatusStripShowsARUPStatus(t *testing.T) {
	a, ct, rect, cleanup := arupPixelFixture(t, 300, 24)
	defer cleanup()
	a.curArea = "Lobby"
	a.sess = &courtroom.Session{
		Areas:    []string{"Lobby"},
		AreaInfo: []courtroom.AreaInfo{{Players: -1}},
	}

	blank, err := ct.Capture(a.ctx.Ren, func(sdl.Rect) { a.drawCurrentAreaStatusStrip(rect) })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	if !allBlackPix(blank) {
		t.Fatal("drawCurrentAreaStatusStrip painted something before ARUP ever reported a status — " +
			"it must degrade to a blank strip, matching the Areas tab's own empty-detail degrade")
	}

	a.sess.AreaInfo[0].Status = "LOOKING-FOR-PLAYERS"
	lit, err := ct.Capture(a.ctx.Ren, func(sdl.Rect) { a.drawCurrentAreaStatusStrip(rect) })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	if allBlackPix(lit) {
		t.Fatal("drawCurrentAreaStatusStrip stayed blank after ARUP reported a status — the /ga flat " +
			"roster would show nothing for casing/locked/gaming/etc., the exact gap this item closes")
	}
}

// TestDrawAreaHeaderRowReadsARUPStatusLive is the staleness gate: the real
// defect risk this item's dossier calls out. playerRosterRows memoizes on
// rosterStamp()/areaListAt, NOT on any ARUP generation counter, so an
// ARUP-only update (session.go's ARUP case: a mod locks an area, nobody
// joins or leaves) must still change what drawAreaHeaderRow paints — it can
// only do that by reading a.sess.AreaInfo LIVE, every call, never from a
// value baked into the cached rosterRow.
//
// Driven through the REAL production calls (playerRosterRows,
// drawAreaHeaderRow) with pixels read back, not a re-derivation of either.
func TestDrawAreaHeaderRowReadsARUPStatusLive(t *testing.T) {
	a, ct, rect, cleanup := arupPixelFixture(t, 400, 30)
	defer cleanup()
	a.rosterLegacy = true
	a.areaPlayers = []areaPlayer{
		{uid: "0", name: "phoenix", area: "Lobby"},
		{uid: "1", name: "maya", area: "Courtroom"},
	}
	a.areaListAt = time.Now()
	a.playerSort = playerSortUID
	a.sess = &courtroom.Session{
		Areas:    []string{"Lobby", "Courtroom"},
		AreaInfo: []courtroom.AreaInfo{{Players: 1}, {Players: 1}},
	}

	lobbyHeader := func(rows []rosterRow) (rosterRow, bool) {
		for _, rw := range rows {
			if rw.header && rw.area == "Lobby" {
				return rw, true
			}
		}
		return rosterRow{}, false
	}

	rows := a.playerRosterRows("")
	hr, ok := lobbyHeader(rows)
	if !ok {
		t.Fatal("no Lobby header row in a 2-area roster — test setup is wrong")
	}

	render1, err := ct.Capture(a.ctx.Ren, func(sdl.Rect) { a.drawAreaHeaderRow(hr, rect) })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	// Sanity: redrawing the SAME unchanged row must be byte-identical, or the
	// byte-compare below (the actual regression check) can't be trusted on
	// this backend.
	repeat, err := ct.Capture(a.ctx.Ren, func(sdl.Rect) { a.drawAreaHeaderRow(hr, rect) })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	if !bytes.Equal(render1.Pix, repeat.Pix) {
		t.Fatal("two renders of the identical, unchanged header row differ — this backend's output isn't " +
			"deterministic enough for the byte-compare this test relies on")
	}

	// The ARUP-only update: mutate AreaInfo in place exactly as session.go's
	// ARUP handler does. Deliberately does NOT touch a.areaListAt or add/remove
	// a player — the roster snapshot the memo keys on is untouched.
	a.sess.AreaInfo[0].Status = "CASING"

	rows2 := a.playerRosterRows("")
	hr2, ok := lobbyHeader(rows2)
	if !ok {
		t.Fatal("Lobby header row vanished after the ARUP-only update")
	}
	if hr2 != hr {
		t.Fatal("the cached rosterRow itself changed after an ARUP-only update — rosterRow must never carry " +
			"baked-in ARUP text (it would go stale by the same memo the header/count already goes stale by)")
	}

	render2, err := ct.Capture(a.ctx.Ren, func(sdl.Rect) { a.drawAreaHeaderRow(hr2, rect) })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	if bytes.Equal(render1.Pix, render2.Pix) {
		t.Fatal("drawAreaHeaderRow painted identical pixels after an ARUP-only status change — it must read " +
			"a.sess.AreaInfo LIVE (via areaInfoByName) on every call, not from anything cached at roster-build time")
	}
}
