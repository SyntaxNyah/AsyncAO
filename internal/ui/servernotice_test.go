package ui

// The gates for the server-notice box (servernotice.go) and for the wrapped body the
// involuntary-disconnect dialog now shares with it.
//
// WHAT THESE ARE FOR. A server that refuses or ends a connection says why, and the
// message carries the thing you have to act on: a whitelist code, a Discord invite, an
// appeal link. AO2 puts it in a modal. We put a BD reason on the lobby as ONE clipped
// label, so the tail of the message — where the code lives — was unreachable, and past
// some length the label drew nothing at all. Three shapes of gate below:
//
//   - ROUTING: a pre-courtroom removal raises the box, with the tail intact.
//   - LAYOUT: the body never ends silently, and never draws outside the panel.
//   - FENCING: while the box is up, nothing behind it takes input — and the box's own
//     buttons still do, which is the same coin's other face (the frame tail has to
//     fence for the modal and then unfence for it).
//
// Where a gate can be driven through App.Frame it is, because the wiring is what broke:
// a modal that draws but is never reached, or is reached but never fenced for, passes
// every unit test of its own functions.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// ---------------------------------------------------------------------------
// Routing: the reported bug
// ---------------------------------------------------------------------------

// noticeLockdownReason is the shape this whole change exists for: a lockdown refusal
// whose ACCESS CODE is on the last line (../Nyathena/internal/athena/
// lockdown_passkey.go formats it that way), in Cyrillic, as the reported server sent
// it. The code is what every gate below looks for — it is the one substring that a
// one-line view structurally cannot show.
const (
	noticeLockdownCode   = "WHITELIST-7781"
	noticeLockdownReason = "Вы не находитесь в вайт-листе сервера.\n" +
		"Присоединитесь к нашему Discord: https://discord.gg/n95zkcBE8h\n" +
		"Код доступа: " + noticeLockdownCode
)

// TestAPreCourtroomBanRaisesTheNoticeBoxWithTheTail is the regression gate for the
// reported defect, driven through the real drop path.
//
// handleInvoluntaryDrop with no room to freeze is the arm a refused handshake lands on
// (the server answers our join with BD and closes, so there is no courtroom yet). It
// must now raise the box, attribute it to the server we dialled, and keep the WHOLE
// reason — the last line especially. The disconnect dialog must NOT open: the two
// surfaces are mutually exclusive branches, and `disconnectDlg.open ⇒ screen ==
// Courtroom` is an invariant the frame tail relies on.
func TestAPreCourtroomBanRaisesTheNoticeBoxWithTheTail(t *testing.T) {
	a := froomApp(t)
	a.room = nil // a drop before the courtroom: char-select, or a refused join
	server := a.serverName

	a.handleInvoluntaryDrop("Banned: " + noticeLockdownReason)

	d := &a.serverNoticeDlg
	if !d.open {
		t.Fatal("a pre-courtroom ban raised no box — the reason is back to one lobby label, " +
			"which is the bug this exists to fix")
	}
	if d.kind != serverNoticeRemoved {
		t.Errorf("kind = %v, want serverNoticeRemoved (a removal is bordered and worded differently)", d.kind)
	}
	if !strings.Contains(d.body, noticeLockdownCode) {
		t.Errorf("the box body lost the access code on the last line: %q", d.body)
	}
	if strings.Count(d.body, "\n") != 2 {
		t.Errorf("the body lost its line breaks (%d left): %q", strings.Count(d.body, "\n"), d.body)
	}
	if strings.HasPrefix(d.body, "Banned: ") {
		t.Error("the box is showing OUR prefix as if the server had said it — the body is the " +
			"server's own words, and Copy hands exactly this to the clipboard")
	}
	if d.server != server {
		t.Errorf("attribution = %q, want %q — serverName lives on sessionState, which the "+
			"teardown rebuilds, so it has to be snapshotted before Disconnect", d.server, server)
	}
	if d.title == "" {
		t.Error("no title: the box would draw a blank heading band")
	}
	if a.disconnectDlg.open {
		t.Error("the disconnect dialog opened too — it must never be up off the courtroom screen")
	}
	if a.screen != ScreenLobby {
		t.Errorf("screen = %v, want the lobby: the box does not navigate, the drop path does", a.screen)
	}
}

// TestOnlyAServersOwnWordsRaiseTheBox pins the other side of the routing decision. Our
// OWN prose — a timeout, a transport drop, a dial error — is already fully said by the
// lobby line and must not pop a modal: a box on every blip is the nagging the font
// warning exists to avoid. A removal with no reason attached is also not a modal
// ("The server banned you." is already on screen; a box saying "no reason given" adds
// a click and no information).
func TestOnlyAServersOwnWordsRaiseTheBox(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		want   bool
	}{
		{"a ban with a reason", "Banned: no metagaming", true},
		{"a kick with a reason", "Kicked: read the rules", true},
		{"a ban with an empty reason", "Banned: ", false},
		{"a ban with only whitespace", "Banned: \n  \n", false},
		{"a transport drop", "connection lost: EOF", false},
		{"a timeout", "write timeout: i/o timeout", false},
		{"a dial failure", "protocol: dialing ws://x: no such host", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := froomApp(t)
			a.room = nil
			a.handleInvoluntaryDrop(tc.reason)
			if got := a.serverNoticeDlg.open; got != tc.want {
				t.Errorf("box open = %v, want %v for %q", got, tc.want, tc.reason)
			}
		})
	}
}

// TestADeliberateDisconnectIsSilent is the one case where a "Kicked:"-shaped reason
// must NOT raise the box: the user pressed Disconnect themselves. Its reason string
// comes from whatever the socket said on the way out, and a modal over a departure the
// user asked for is noise.
func TestADeliberateDisconnectIsSilent(t *testing.T) {
	a := froomApp(t)
	a.room = nil
	a.deliberateClose = true
	a.handleInvoluntaryDrop("Kicked: you left")
	if a.serverNoticeDlg.open {
		t.Error("a deliberate close raised a modal")
	}
}

// TestARemovalNoticeCannotBeBuriedByAnythingElse is the precedence rule, and it is a
// safety property rather than a nicety: a server that spams a BB popup as it closes the
// socket must not be able to cover the ban reason, which is the only copy of the appeal
// instructions the user gets.
func TestARemovalNoticeCannotBeBuriedByAnythingElse(t *testing.T) {
	a := froomApp(t)
	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)

	a.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, "S", "read the rules")
	if a.serverNoticeDlg.kind != serverNoticeRemoved || !strings.Contains(a.serverNoticeDlg.body, noticeLockdownCode) {
		t.Fatal("an info notice buried a removal notice")
	}

	// The disconnect dialog is the other blocking modal: it dismisses a live INFO
	// notice (moot the instant the session is gone) and must leave a removal alone.
	a.openDisconnectDialog("S", "ws://s", "connection lost", a.now())
	if !a.serverNoticeDlg.open || a.serverNoticeDlg.kind != serverNoticeRemoved {
		t.Error("the disconnect dialog destroyed a removal notice")
	}

	// And the info case really is dismissed, so the two never stack.
	b := froomApp(t)
	b.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, "S", "motd")
	b.openDisconnectDialog("S", "ws://s", "connection lost", b.now())
	if b.serverNoticeDlg.open {
		t.Error("an info notice stayed up under the disconnect dialog — two blocking modals stacked")
	}

	// A removal always replaces whatever is showing, including another removal.
	a.openServerNotice(serverNoticeRemoved, "Kicked", "S2", "second")
	if a.serverNoticeDlg.body != "second" || a.serverNoticeDlg.server != "S2" {
		t.Error("a second removal did not replace the first")
	}
}

// TestOpeningANoticeDropsEveryFieldOfThePreviousOne pins the whole-struct assignment.
// A surviving wrap cache would draw one server's words under another server's heading,
// which is the client putting words in a server's mouth.
func TestOpeningANoticeDropsEveryFieldOfThePreviousOne(t *testing.T) {
	a := froomApp(t)
	a.openServerNotice(serverNoticeInfo, "first", "S1", "first body")
	a.serverNoticeDlg.wrap.get(func(string) int32 { return 1 }, 0, "first body", 100, 4, "")
	if a.serverNoticeDlg.wrap.lines == nil {
		t.Fatal("the fixture never baked a wrap, so nothing below is proving it gets dropped")
	}
	a.openServerNotice(serverNoticeInfo, "second", "S2", "second body")
	if a.serverNoticeDlg.wrap.lines != nil {
		t.Error("the previous notice's laid-out body survived into the new one")
	}
	a.closeServerNotice()
	d := &a.serverNoticeDlg
	if d.open || d.kind != serverNoticeNone || d.title != "" || d.server != "" || d.body != "" ||
		d.wrap.lines != nil {
		t.Errorf("closing left state behind (%+v) — the zero value is the closed state", *d)
	}
}

// TestAnOversizedTitleCannotOverflowTheHeading pins the one string the box does NOT
// wrap. Titles are client-authored by contract, and clampRunes is the structural
// guarantee for a caller who ignores that contract; RUNES because a Cyrillic or CJK
// heading would lose half its characters to a byte cap.
func TestAnOversizedTitleCannotOverflowTheHeading(t *testing.T) {
	a := froomApp(t)
	long := strings.Repeat("б", serverNoticeTitleRuneCap*2)
	a.openServerNotice(serverNoticeInfo, long, "S", "body")
	title := a.serverNoticeDlg.title
	if n := len([]rune(title)); n > serverNoticeTitleRuneCap+1 {
		t.Errorf("title kept %d runes, past the %d cap", n, serverNoticeTitleRuneCap)
	}
	if !strings.HasSuffix(title, "…") {
		t.Error("the title was cut without saying so")
	}

	exact := strings.Repeat("б", serverNoticeTitleRuneCap)
	a.openServerNotice(serverNoticeInfo, exact, "S", "body")
	if a.serverNoticeDlg.title != exact {
		t.Error("a title of exactly the cap was truncated — the clamp is counting bytes, " +
			"and Cyrillic is two bytes a rune")
	}
}

// ---------------------------------------------------------------------------
// Layout: never silent, never outside the panel
// ---------------------------------------------------------------------------

// TestTheNoticeBodyNeverEndsSilently is the layout half of the reported bug, measured
// with the REAL font chain.
//
// A body that overflows its row budget must spend its last row SAYING so and pointing
// at Copy. Silence is the actual defect: the tail of these payloads is the part you
// need, so a body that just stops looks like the whole message.
//
// It runs on the frame harness because Ctx.TextWidth returns 0 with no font loaded —
// a wrap gate written against a fontless Ctx passes vacuously, every row measuring
// zero and everything "fitting".
func TestTheNoticeBodyNeverEndsSilently(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a) // settle the harness exactly as every other gate on it does

	const textW = int32(serverNoticeW) - 2*pad
	rows := serverNoticeRows(frameHarnessH)
	if rows < 2 {
		t.Fatalf("the harness window fits %d body rows — this gate needs at least two", rows)
	}

	// Short enough to fit: every line survives, the code included, and no marker.
	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)
	lines, fit := a.serverNoticeBody(textW, rows)
	if !fit {
		t.Fatalf("the reported reason did not fit %d rows, so this arm proves nothing: %q", rows, lines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), noticeLockdownCode) {
		t.Errorf("the access code never reached a drawn row: %q", lines)
	}
	for _, l := range lines {
		if l == serverNoticeTruncMark {
			t.Error("a body that fits must not claim it was cut")
		}
	}

	// Far too long: the budget holds, and the last row says so.
	flood := strings.Repeat("a long sentence that will certainly wrap more than once. ", 200)
	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", flood)
	lines, fit = a.serverNoticeBody(textW, rows)
	if fit {
		t.Fatal("a 200-sentence body reported that it fit")
	}
	if len(lines) != rows {
		t.Errorf("the body spent %d rows of a %d budget", len(lines), rows)
	}
	if len(lines) == 0 || lines[len(lines)-1] != serverNoticeTruncMark {
		t.Errorf("the body was truncated SILENTLY — last row %q, want the marker", lines[len(lines)-1])
	}
}

// TestANoticeWithNoWordsStillSaysSomething: a server may close us out with an empty
// string, and an empty panel with a button in it reads as a broken client.
func TestANoticeWithNoWordsStillSaysSomething(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)

	a.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, "S", "")
	lines, _ := a.serverNoticeBody(int32(serverNoticeW)-2*pad, serverNoticeRows(frameHarnessH))
	if len(lines) == 0 || strings.TrimSpace(strings.Join(lines, "")) == "" {
		t.Fatalf("an empty body drew nothing at all: %q", lines)
	}
	if !strings.Contains(strings.Join(lines, " "), strings.Fields(serverNoticeNoReason)[0]) {
		t.Errorf("the stand-in sentence is not what got laid out: %q", lines)
	}
}

// TestEveryNoticeRowFitsInsideThePanel is the arithmetic gate on the two sizing
// helpers, across the window sizes the client can actually be.
//
// serverNoticeRows and drawServerNotice derive their geometry from the same constants
// on purpose — the trap is that they drift, so the wrap is told a budget the draw then
// refuses to paint, and rows vanish with no marker (the draw's own break guard would
// silently eat them). config.MinWindowH is the smallest window the client allows, so
// it is the size that has to work, not 720.
func TestEveryNoticeRowFitsInsideThePanel(t *testing.T) {
	for _, h := range []int32{config.MinWindowH, 600, frameHarnessH, 1080, 2160} {
		rows := serverNoticeRows(h)
		if rows < 1 {
			t.Errorf("h=%d: %d rows — the body would wrap to nothing", h, rows)
			continue
		}
		if rows > serverNoticeMaxRows {
			t.Errorf("h=%d: %d rows, past the %d cap", h, rows, serverNoticeMaxRows)
		}
		// The panel the draw builds for a full budget, and the body limit it enforces.
		mh := serverNoticeChromeH() + int32(rows)*serverNoticeLineH
		if mh < serverNoticeMinH {
			mh = serverNoticeMinH
		}
		if lim := h - 2*serverNoticeMargin; mh > lim {
			t.Errorf("h=%d: a full %d-row body needs %d px but only %d fit in the window",
				h, rows, mh, lim)
			continue
		}
		// Walk the rows the way drawServerNotice does and require the last one inside.
		y := int32(serverNoticeHeadH + serverNoticeMetaH)
		bodyLimit := mh - btnH - pad - serverNoticeBtnGap
		drawn := 0
		for i := 0; i < rows; i++ {
			if y+serverNoticeLineH > bodyLimit {
				break
			}
			y += serverNoticeLineH
			drawn++
		}
		if drawn != rows {
			t.Errorf("h=%d: the wrap was given %d rows but the panel only paints %d — the "+
				"budget and the layout have drifted apart", h, rows, drawn)
		}
	}
}

// TestTheOneLineDropDialogKeepsItsOldShape is the open-closed evidence for the
// disconnect dialog. It grew a computed height and a third button; the overwhelmingly
// common case — a one-line transport drop — must still be the 520x220 panel it was, or
// this change altered a surface it was not supposed to touch.
func TestTheOneLineDropDialogKeepsItsOldShape(t *testing.T) {
	const oldW, oldH = 520, 220
	if discDlgW != oldW || discDlgMinH != oldH {
		t.Fatalf("the dialog's floor moved to %dx%d from %dx%d", discDlgW, discDlgMinH, oldW, oldH)
	}
	// One friendly line + one raw row must come out UNDER the floor, so the floor wins
	// and the panel is pixel-identical to the fixed-size version.
	if got := discDlgChromeH() + discDlgFriendlyH + discDlgLineH; got > discDlgMinH {
		t.Errorf("a one-line drop now needs %d px, past the old %d — the common case resized", got, discDlgMinH)
	}
	// The three buttons have to fit side by side without touching: 150 + copy + 150 in
	// a 520 px panel with 8 px padding.
	left := int32(pad) + 150
	mid := int32((discDlgW - discDlgCopyW) / 2)
	right := int32(discDlgW - pad - 150)
	if mid < left {
		t.Errorf("Copy (x=%d) overlaps Reconnect (ends x=%d)", mid, left)
	}
	if mid+discDlgCopyW > right {
		t.Errorf("Copy (ends x=%d) overlaps Back to lobby (x=%d)", mid+discDlgCopyW, right)
	}
	// And the raw rows stay inside the panel at every window height, by the draw's own
	// arithmetic (heading + friendly line + rows, above the countdown strip).
	for _, h := range []int32{config.MinWindowH, frameHarnessH, 1440} {
		for _, friendlyH := range []int32{0, discDlgFriendlyH} {
			rows := discDlgRawRows(h, friendlyH)
			if rows < 1 || rows > discDlgRawMaxRows {
				t.Errorf("h=%d friendly=%d: %d rows, outside [1,%d]", h, friendlyH, rows, discDlgRawMaxRows)
				continue
			}
			mh := discDlgChromeH() + friendlyH + int32(rows)*discDlgLineH
			if mh < discDlgMinH {
				mh = discDlgMinH
			}
			if lim := h - 2*serverNoticeMargin; mh > lim {
				t.Errorf("h=%d friendly=%d: panel %d px does not fit in %d", h, friendlyH, mh, lim)
				continue
			}
			y := discDlgHeadH + friendlyH
			bodyLimit := mh - btnH - pad - discDlgCountdownH
			drawn := 0
			for i := 0; i < rows; i++ {
				if y+discDlgLineH > bodyLimit {
					break
				}
				y += discDlgLineH
				drawn++
			}
			if drawn != rows {
				t.Errorf("h=%d friendly=%d: %d rows budgeted, %d painted", h, friendlyH, rows, drawn)
			}
		}
	}
}

// TestTheWrappedBodyIsBakedNotRewrappedEveryFrame is the alloc gate for the seam, and
// it measures the SEAM rather than the frame for the reason the report panel's gate
// spells out: a whole-frame budget is large enough to swallow a per-frame rewrap.
//
// The modals redraw sixty times a second while they are up, and the text cannot change
// while they are open, so the wrap must run on change only.
func TestTheWrappedBodyIsBakedNotRewrappedEveryFrame(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)

	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)
	const textW = int32(serverNoticeW) - 2*pad
	rows := serverNoticeRows(frameHarnessH)
	a.serverNoticeBody(textW, rows) // one warm call: the wrap bakes, the measure memoises

	if n := testing.AllocsPerRun(50, func() { a.serverNoticeBody(textW, rows) }); n > 0 {
		t.Errorf("the notice body allocates %.0f/op — the wrap is running on the draw", n)
	}
	// The metric itself must be built once. A fresh closure (or a bound method value)
	// here would allocate on every frame a modal is up.
	if n := testing.AllocsPerRun(50, func() { a.chromeWrapMeasure() }); n > 0 {
		t.Errorf("chromeWrapMeasure allocates %.0f/op — it is building the closure per call", n)
	}
	// A changed input MUST rewrap, or the cache is a bug instead of an optimisation.
	before, _ := a.serverNoticeBody(textW, rows)
	after, _ := a.serverNoticeBody(textW/2, rows)
	if len(before) == len(after) && strings.Join(before, "|") == strings.Join(after, "|") {
		t.Error("halving the width returned the same rows — the cache is ignoring its key")
	}
}

// TestAStaleFontChainCannotFreezeTheWrap pins the generation key, which is load-bearing
// for exactly the case this feature was built for. A Cyrillic notice is first measured
// in the embedded chrome face, which does not cover it; the covering fallback lands
// asynchronously a few frames later and bumps fontChainGen. Without that key the rows
// keep the widths we guessed before the face we draw them in existed.
func TestAStaleFontChainCannotFreezeTheWrap(t *testing.T) {
	var narrow paraWrapCache
	measure := func(s string) int32 { return int32(len(s)) * 4 }
	wide := func(s string) int32 { return int32(len(s)) * 40 }

	first, _ := narrow.get(measure, 1, "a b c d e f g h i j", 40, 8, "")
	same, _ := narrow.get(wide, 1, "a b c d e f g h i j", 40, 8, "")
	if strings.Join(first, "|") != strings.Join(same, "|") {
		t.Fatal("the cache rewrapped without its generation changing, so the next arm proves nothing")
	}
	next, _ := narrow.get(wide, 2, "a b c d e f g h i j", 40, 8, "")
	if strings.Join(next, "|") == strings.Join(first, "|") {
		t.Error("a bumped font generation did not rewrap — a Cyrillic notice keeps the widths " +
			"measured in a face that could not draw it")
	}
}

// TestTheLobbyLineStaysOneLineAndIsMemoised covers the other end of the same reason
// string. connErr can now be a multi-line ban message, and the lobby draws it as a
// single label: it must be cut at the first newline, marked, clamped, and computed on
// CHANGE — clampLine allocates a []rune unconditionally, and the lobby redraws every
// frame.
func TestTheLobbyLineStaysOneLineAndIsMemoised(t *testing.T) {
	a := froomApp(t)
	a.connErr = "Banned: " + noticeLockdownReason

	line := a.connErrLabel()
	if strings.Contains(line, "\n") {
		t.Errorf("the lobby label carries a newline: %q", line)
	}
	if !strings.HasSuffix(line, "…") {
		t.Errorf("a multi-line reason was cut with no marker: %q", line)
	}
	if n := len([]rune(line)); n > logLineMax+2 {
		t.Errorf("the label is %d runes long", n)
	}
	if n := testing.AllocsPerRun(50, func() { a.connErrLabel() }); n > 0 {
		t.Errorf("connErrLabel allocates %.0f/op on an unchanged reason", n)
	}
	a.connErr = "connection lost: EOF"
	if got := a.connErrLabel(); got != a.connErr {
		t.Errorf("a changed reason was not recomputed: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Fencing: through the real frame
// ---------------------------------------------------------------------------

// serverNoticeCloseCentre is the Close/Got it button's hit point, in
// drawServerNotice's own arithmetic. Duplicated rather than exported (the
// reportChipCentre idiom) — and the gate below proves the coordinate LANDS by first
// clicking somewhere else and requiring nothing to happen, so a stale copy of this
// arithmetic cannot make the test pass by missing.
func serverNoticeCloseCentre(a *App, w, h int32) (x, y int32) {
	rows := serverNoticeRows(h)
	lines, _ := a.serverNoticeBody(int32(serverNoticeW)-2*pad, rows)
	mh := serverNoticeChromeH() + int32(len(lines))*serverNoticeLineH
	if mh < serverNoticeMinH {
		mh = serverNoticeMinH
	}
	if lim := h - 2*serverNoticeMargin; mh > lim {
		mh = lim
	}
	m := sdl.Rect{X: (w - serverNoticeW) / 2, Y: (h - mh) / 2, W: serverNoticeW, H: mh}
	return m.X + serverNoticeW - pad - serverNoticeBtnW/2, m.Y + mh - btnH - pad + btnH/2
}

// TestTheNoticeBoxDrawsThroughTheFrameAndTakesItsClicks is the wiring gate: the box has
// to be reached by App.Frame's tail, and its buttons have to work there.
//
// Those are two halves of one thing. The tail fences the pointer for the whole modal
// family and then unfences just before the box draws; delete the draw call and nothing
// appears, delete the unfence and the box appears but is dead. Both mutations pass
// every test of drawServerNotice itself.
func TestTheNoticeBoxDrawsThroughTheFrameAndTakesItsClicks(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)

	const body = "Server maintenance at 20:00 UTC."
	if textCacheHasLabel(a.ctx, body) {
		t.Fatal("the fixture already drew the body text before anything opened the box")
	}
	a.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, "S", body)
	driveFrame(a)
	if !textCacheHasLabel(a.ctx, body) {
		t.Fatal("the notice body never reached the screen — App.Frame is not drawing the box")
	}

	// A click inside the panel but NOT on a button must not dismiss it. This is what
	// makes the arm below meaningful rather than "any click closes it".
	x, y := serverNoticeCloseCentre(a, frameHarnessW, frameHarnessH)
	driveClickAt(a, frameHarnessW/2, y-serverNoticeLineH*3)
	if !a.serverNoticeDlg.open {
		t.Fatal("a click on the panel body closed the box")
	}

	driveClickAt(a, x, y)
	if a.serverNoticeDlg.open {
		t.Error("the box's own dismiss button did nothing — the frame tail is not restoring the " +
			"pointer for it, so the modal is unclosable by mouse")
	}
}

// TestTheNoticeBoxLeavesTheChromeBehindItDead is the fence, driven as a user would hit
// it: the menu bar is painted on every screen and sits nowhere near the box, so a click
// on it is the cleanest proof that input does not reach past the modal.
//
// Both directions are asserted. The same click with the box closed MUST open the menu,
// or this gate would pass against a menu bar that is broken for unrelated reasons.
func TestTheNoticeBoxLeavesTheChromeBehindItDead(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)

	r := a.menuBarTitleRect(0)
	if r.W <= 0 || r.H <= 0 {
		t.Skip("the menu bar is not laid out in this fixture")
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2

	driveClickAt(a, cx, cy)
	if !a.menuBarOpen() {
		t.Skip("the menu bar does not take a click in this fixture, so the fence proves nothing here")
	}
	a.closeMenuBar()
	driveFrame(a)

	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)
	driveClickAt(a, cx, cy)
	if a.menuBarOpen() {
		t.Error("the menu bar took a click through the notice box — the strip must be inert " +
			"(menuBarSuppressed) and the pointer fenced while a blocking modal is up")
	}
	if !a.serverNoticeDlg.open {
		t.Error("the click dismissed the box from outside the panel")
	}
}

// TestTheNoticeBoxKeepsCourtroomHotkeysOffTheWire pins the keyboard half. A BB notice
// can land while the courtroom is fully LIVE, so this is not about a dead socket: it is
// that a shout or a panel opened blind underneath the box is an action the user did not
// see themselves take.
func TestTheNoticeBoxKeepsCourtroomHotkeysOffTheWire(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)

	key := sdl.GetKeyFromName(a.hotkeyFor(hotkeyEvidence))
	if key == 0 {
		t.Skip("no key is bound to the evidence action in this build")
	}
	driveHotkey(a, key)
	if !a.showEvid {
		t.Skip("the evidence chord does not fire in this fixture, so the gate below proves nothing")
	}

	a.showEvid = false
	a.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, "S", "read the rules")
	driveHotkey(a, key)
	if a.showEvid {
		t.Error("a courtroom chord fired under the notice box — handleHotkeys must stand down " +
			"for it, exactly as it does for the disconnect dialog")
	}
}

// TestEscClosesTheNoticeBoxFirst pins the Esc rung. Esc arrives as ctx.escPressed (NOT
// keyPressed) and is answered by closeTopOverlay, which is also the reason the box is
// still dismissable from the keyboard while handleHotkeys is standing down.
//
// Priority matters: a floating panel open behind the box must survive the press that
// closes the box, or one keystroke would tear down two layers.
func TestEscClosesTheNoticeBoxFirst(t *testing.T) {
	a := froomApp(t)
	a.showVoice = true
	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)

	if !a.closeTopOverlay() {
		t.Fatal("Esc did not close anything with the box up")
	}
	if a.serverNoticeDlg.open {
		t.Error("Esc closed something else and left the box up")
	}
	if !a.showVoice {
		t.Error("Esc closed the panel behind the box as well — one layer per press")
	}
}

// TestTheNoticeBoxFreezesTheTabStrip: the strip can still be hit while the box is up
// (the box is on App and does not park with a tab), and for a REMOVED notice a tab
// switch would scroll the appeal text away before it has been read or copied. The gate
// is at the top of handleTabBar, so it is driven by calling handleTabBar with a drag in
// flight; the frame's own call site is covered by the census gate below.
func TestTheNoticeBoxFreezesTheTabStrip(t *testing.T) {
	a := froomApp(t)
	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)
	a.tabDragFrom, a.tabDragging = 0, true

	a.handleTabBar(frameHarnessW, frameHarnessH)

	if a.tabDragging || a.tabDragFrom != -1 {
		t.Errorf("a tab drag survived the notice box (from=%d dragging=%v)", a.tabDragFrom, a.tabDragging)
	}
}

// TestEnterOnTheLobbyCannotRedialTheServerThatRefusedYou drives the first of the two
// holes the pointer fence structurally cannot cover.
//
// fencePointer blanks the MOUSE — clicked, mouseDown, the wheel, and the cursor itself
// (ui.go) — and leaves the keyboard exactly as it was, so every site that reads a key
// without going through handleHotkeys needs its own guard. The lobby's arrow-key
// selection is the worst of them: the box is on screen precisely BECAUSE the selected
// server refused us, and for a rate-limit refusal (429) redialling is the one action
// that makes the situation worse.
//
// The second half is not decoration, it is the only thing standing between this gate
// and vacuity — the arm being fenced shipped DEAD. It read c.keyPressed, which
// HandleEvent can never set to Return (Return is answered with c.enter in an earlier
// arm of the same switch), so "Enter joins the selected server" has done nothing since
// it shipped. A fence around dead code passes no matter which way it is written.
func TestEnterOnTheLobbyCannotRedialTheServerThatRefusedYou(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a) // settle one real frame before the screen changes under it

	// Port 1 on loopback is chosen for two properties at once: WSPort > 0 makes the
	// entry Joinable (SecurityWS, not the black legacy-TCP tier the lobby refuses to
	// dial at all), and nothing listens there, so the arm that DOES reach Connect is
	// refused by the OS in microseconds instead of waiting out a handshake.
	const wantURL = "ws://127.0.0.1:1"
	a.screen = ScreenLobby
	a.servers = []network.ServerEntry{{Name: "Refuser", IP: "127.0.0.1", WSPort: 1}}
	a.selServer = 0
	a.ctx.focusID = "" // the arrow/Enter block only runs when no field holds the caret
	// Arm the on-open auto-refresh's cap. lastLobbyRefreshAt is zero in a fresh fixture,
	// which lobbyAutoRefreshDue reads as "maximally due", and a real master-list fetch
	// would replace a.servers from a goroutine mid-test.
	a.lastLobbyRefreshAt = a.now()

	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "Refuser", noticeLockdownReason)
	a.lastConnURL = ""
	driveEnter(a)
	if a.lastConnURL != "" {
		t.Errorf("Enter under the notice box dialled %q — the box's own text is the reason that "+
			"server is unreachable", a.lastConnURL)
	}
	if !a.serverNoticeDlg.open {
		t.Error("Enter dismissed the box; only Esc and its own button do that, and a ban reason " +
			"must not vanish on the keystroke that was trying to rejoin")
	}

	a.closeServerNotice()
	a.selServer = 0
	a.ctx.focusID = ""
	driveEnter(a)
	if a.lastConnURL != wantURL {
		t.Fatalf("Enter with nothing in the way left lastConnURL = %q, want %q — the lobby's "+
			"Enter-to-join is not reached at all, so the arm above is a fence around dead code",
			a.lastConnURL, wantURL)
	}
}

// TestEnterInTheICFieldIsFencedWithoutEatingTheDraft drives the second hole, on BOTH
// courtroom layouts, and pins the half that is easy to get wrong while fixing it.
//
// BOTH, because the courtroom has two IC fields and only one of them was fenced when
// this gate was first written. drawCourtroomThemed draws its own field at the theme's
// ao2_ic_chat_message rect and shares only the send helper with the classic bar, so a
// guard on one is not a guard on the other — and this was found by mutation, not by
// reading: the frame fixture is THEMED, so the first version of this gate stayed green
// with the classic fence deleted because the classic field never drew. The classic arm
// therefore has to turn the theme layout off to reach the row at all.
//
// The fence must sit on the SEND, not on the field. Swallow the keystroke earlier and
// the line the user typed is gone, which for a wall of IC text is a worse bug than the
// one being fixed — so the draft has to survive the fenced press and still send on the
// next one.
//
// "/unpair" is the draft on purpose. It is a LOCAL chat command (handleChatCommand), so
// a send that gets through proves itself by clearing the field and moving pairWith, with
// no live socket, no wire packet, and no MyCharID deref on a zero-value Session.
func TestEnterInTheICFieldIsFencedWithoutEatingTheDraft(t *testing.T) {
	for _, tc := range []struct {
		name   string
		themed bool
	}{
		{"themed layout", true},
		{"classic layout", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, cleanup := stageFrameDrivenApp(t)
			defer cleanup()
			a.d.Prefs.SetThemeLayout(tc.themed) // which of the two IC fields draws
			driveFrame(a)

			const draft = "/unpair"
			const pairedWith = 7
			a.icInput = draft
			a.pairWith = pairedWith
			a.ctx.focusID = icFieldID // the field only returns its Enter while it holds focus
			a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)

			driveEnter(a)
			// FATAL, not an error: with the fence gone the draft is already spent, and the
			// non-vacuity press below then sends a REAL IC line — which on a fixture with no
			// socket panics inside Session.reply and buries this failure in a stack trace.
			// Stopping here keeps a broken fence reading as a broken fence.
			if a.icInput != draft {
				t.Fatalf("icInput = %q, want %q — either the send went through under the box or the "+
					"keystroke was swallowed somewhere that took the draft with it", a.icInput, draft)
			}
			if a.pairWith != pairedWith {
				t.Errorf("pairWith = %d, want %d — the line was sent into a room the server has "+
					"already removed us from", a.pairWith, pairedWith)
			}

			a.closeServerNotice()
			a.ctx.focusID = icFieldID
			driveEnter(a)
			if a.icInput != "" || a.pairWith != protocol.UnpairedCharID {
				t.Fatalf("Enter with nothing in the way left icInput=%q pairWith=%d — this layout's "+
					"IC field never reaches sendIC in the fixture, so the arm above proves nothing "+
					"about it", a.icInput, a.pairWith)
			}
		})
	}
}

// TestEveryWrapInputIsPartOfTheCacheKey is the encapsulation gate on the shared cache,
// and it covers the two of its six inputs the other wrap gates leave untested: the ROW
// BUDGET and the truncation MARK. (Width is pinned by the alloc gate above, the font
// generation by the stale-chain gate.)
//
// Rows is the one with a live failure mode. Both modals derive their budget from the
// window height, so a key that ignored it would keep a fourteen-row layout after a
// resize into a three-row window, and the draw's own limit would then eat the extra rows
// SILENTLY — the exact way of losing the tail of a message that this whole feature
// exists to end.
//
// The mark is one step further out, and the type's own comment says why it is a key
// rather than an argument: today both callers pass the same constant, so it reads as
// invariant from inside either one, and the day a second marker appears a cache that
// ignored it would serve one modal's marker from the other's cut.
func TestEveryWrapInputIsPartOfTheCacheKey(t *testing.T) {
	var shared paraWrapCache
	measure := func(s string) int32 { return int32(len(s)) * 4 }
	const body = "a b c d e f g h i j k l m n o p q r s t"

	// Budget: one row, then four, with the text, width, generation and mark all identical.
	one, fit := shared.get(measure, 1, body, 40, 1, serverNoticeTruncMark)
	if fit {
		t.Fatalf("the body fit in one row, so nothing here is a truncation: %q", one)
	}
	if len(one) == 0 || one[len(one)-1] != serverNoticeTruncMark {
		t.Fatalf("an overflowing body did not end in the mark: %q", one)
	}
	// Three, not four: at four this body fits exactly, and a body that fits is never
	// marked — so the arm below would be asserting against rows the cache was right to
	// leave unmarked. Every budget here has to OVERFLOW for the mark to be in play.
	three, fit := shared.get(measure, 1, body, 40, 3, serverNoticeTruncMark)
	if fit {
		t.Fatalf("the body fit in three rows, so the mark is not in play: %q", three)
	}
	if len(three) == len(one) {
		t.Errorf("a 1-row and a 3-row budget both returned %d rows — the budget is missing from "+
			"the key, so a resized window keeps the old layout and drops rows with no marker",
			len(one))
	}

	// Mark: same everything, different cut marker.
	const otherMark = "…"
	marked, _ := shared.get(measure, 1, body, 40, 3, otherMark)
	if len(marked) == 0 || marked[len(marked)-1] != otherMark {
		t.Errorf("the cache returned %q for a changed mark, want it to end in %q — the mark is "+
			"missing from the key, so whichever caller wrapped first owns the other's marker",
			marked, otherMark)
	}
}

// TestAnOversizedAttributionCannotOverflowTheHeading is the title clamp's twin, on the
// field that is NOT client-authored.
//
// server comes from a.serverName, which for a lobby entry came out of the master list's
// JSON with no per-field length of its own, so a server can choose it. It draws through
// LabelClipped, which rasterizes the whole string before clipping the blit — the exact
// mechanism that made a long BD reason draw NOTHING, reproduced on the attribution row
// of the box built to end it.
func TestAnOversizedAttributionCannotOverflowTheHeading(t *testing.T) {
	a := froomApp(t)

	a.openServerNotice(serverNoticeRemoved, "The server banned you.",
		strings.Repeat("server-name ", 400), noticeLockdownReason)
	if n := len([]rune(a.serverNoticeDlg.server)); n > serverNoticeTitleRuneCap+1 {
		t.Errorf("the attribution row kept %d runes, past the %d cap (+1 for the marker)",
			n, serverNoticeTitleRuneCap)
	}
	if !strings.HasSuffix(a.serverNoticeDlg.server, "…") {
		t.Errorf("a clamped attribution was cut with no marker: %q", a.serverNoticeDlg.server)
	}
	// RUNES, not bytes, and Cyrillic is the case that catches a byte cap: exactly at the
	// cap it must pass through untouched, where a byte cap would cut it in half.
	atCap := strings.Repeat("я", serverNoticeTitleRuneCap)
	a.openServerNotice(serverNoticeRemoved, "T", atCap, "b")
	if a.serverNoticeDlg.server != atCap {
		t.Errorf("a Cyrillic attribution exactly at the %d-rune cap was cut to %d runes — the "+
			"clamp is counting bytes", serverNoticeTitleRuneCap, len([]rune(a.serverNoticeDlg.server)))
	}
}

// TestTheNoticeBoxStaysClickableInAWindowSmallerThanItself is the gate on the two
// degradation clamps, and it is behavioural on purpose: both of them are arithmetic that
// looks harmless and fails as "the button is not there".
//
// The logical window is the physical one divided by the UI scale, so config.MinWindowH
// does NOT bound the height this code sees — 150% scale on a short window gets here.
// Without the absolute floor the window clamp hands back a height smaller than the
// chrome, and the buttons' y is measured from the panel's BOTTOM, so they climb off the
// top of the panel. Without the X pin a 560 px panel centred in a 400 px viewport starts
// at a NEGATIVE x, dragging left-anchored Copy — the button that exists to rescue an
// access code — off the left edge.
//
// One click proves both: it lands on Copy under the clamps and misses it (into the gap
// between the buttons, or off the row entirely) under either failure. Driven on the real
// Ctx rather than through driveFrame because the harness drives a fixed 1280x720, and
// the window size IS the variable under test.
func TestTheNoticeBoxStaysClickableInAWindowSmallerThanItself(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)

	// Narrower and shorter than the panel, in both axes at once.
	const tinyW, tinyH = int32(400), int32(40)
	if tinyW >= serverNoticeW || tinyH >= serverNoticeMinH {
		t.Fatalf("%dx%d is not smaller than the panel, so neither clamp is exercised", tinyW, tinyH)
	}
	a.openServerNotice(serverNoticeRemoved, "The server banned you.", "S", noticeLockdownReason)

	// The floored panel: pinned to the top-left corner, exactly its chrome plus the one
	// row the floor guarantees. Copy is left-anchored inside it.
	mh := serverNoticeChromeH() + serverNoticeLineH
	cx := int32(pad) + serverNoticeBtnW/2
	cy := mh - btnH - pad + btnH/2

	a.warnLine = ""
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.mouseX, a.ctx.mouseY = cx, cy
	a.ctx.downX, a.ctx.downY = cx, cy
	a.ctx.clicked = true
	a.drawServerNotice(tinyW, tinyH)

	if a.warnLine == "" {
		t.Errorf("a click at (%d,%d) — the middle of Copy in a %dx%d window — did nothing. The "+
			"panel is off the left edge or its buttons are above their own heading, which is "+
			"exactly the state where a whitelist code cannot be copied", cx, cy, tinyW, tinyH)
	}
	if !a.serverNoticeDlg.open {
		t.Error("Copy dismissed the box")
	}
}

// TestTheNoticeBoxIsHonouredAtEveryModalSite is the anti-deletion gate, and it is the
// one rule 11 asks for by name: the flag is a term added at each site of a census that
// no single behavioural test can cover (two of the three in App.Frame are a pointer
// fence and a draw order, neither of which leaves an observable mark after the frame
// ends).
//
// House doctrine (floatbox.go) forbids folding these sites into one predicate — the
// sets genuinely differ — so the only way to keep them honest is to require the
// reference, by function, and say what is lost when it goes. Each count is a MINIMUM,
// because App.Frame carries three distinct sites and deleting any one of them is the
// regression.
func TestTheNoticeBoxIsHonouredAtEveryModalSite(t *testing.T) {
	// Two kinds of site: the ones that CHECK the flag (fences, Esc, the draw order) and
	// the ones that RAISE the box (the three packet/dial routes). Each row names the
	// identifier that must appear in that function, so a deleted term fails here with
	// the consequence written out rather than as a silent hole.
	sites := []struct {
		fn    string
		ref   string
		least int
		lost  string
	}{
		// FOUR sites in App.Frame, and the count is exact for a reason: it was written as
		// 3 first, and deleting the pointer fence then left it green — the behavioural
		// gate below did not catch that mutation either, because menuBarSuppressed
		// independently deadens the strip the click lands on. The two mechanisms overlap
		// on purpose, so only a count can hold each of them.
		{"Frame", "serverNoticeDlg", 4, "one of: the quick-connect key guard (a keypress redials " +
			"the server that just banned you), the pointer fence (a click lands on the lobby's " +
			"Reconnect and does the same with the mouse), the hotkey-sheet unfence, or the draw call " +
			"itself (the box never appears)"},
		{"handleHotkeys", "serverNoticeDlg", 1, "a shout or a panel fires blind under the box"},
		{"menuBarSuppressed", "serverNoticeDlg", 1, "the menu bar stays live and its modalOn blanks the box's buttons"},
		{"closeTopOverlay", "serverNoticeDlg", 1, "Esc stops closing the box and toggles something behind it instead"},
		{"handleTabBar", "serverNoticeDlg", 1, "a tab switch scrolls an unread ban reason away"},
		// The KEYBOARD sites the pointer fence cannot cover, because fencePointer blanks
		// the mouse and leaves the keyboard alone. None of them go through handleHotkeys,
		// so the guard there does not reach them.
		{"drawLobby", "serverNoticeDlg", 1, "Enter on the lobby's arrow-key selection redials the " +
			"server whose ban message is the reason the box is on screen"},
		// All THREE IC send sites are listed, enumerated rather than summarised, because
		// they were not found together: the classic bar was fenced first and the other two
		// were still open behind it. Each owns its own TextFieldEmoji whose Enter fires
		// while that field holds focus, and focus survives the box opening.
		{"drawICControls", "serverNoticeDlg", 1, "Enter in the IC field sends a line into a room the " +
			"server has already removed you from"},
		{"drawCourtroomThemed", "serverNoticeDlg", 1, "the same Enter hole reopens for anyone on an AO2 " +
			"theme, since that layout path draws its own IC field and shares only the send helper"},
		{"drawSplitInput", "serverNoticeDlg", 1, "Enter in the pinned pane sends a line the user never " +
			"saw themselves send, because they were reading the box at the time"},
		{"openDisconnectDialog", "serverNoticeDlg", 1, "a BB notice and the disconnect dialog stack"},
		{"handleInvoluntaryDrop", "openServerNotice", 1, "a pre-courtroom ban goes back to one unreadable lobby label"},
		{"handleSessionEvents", "openServerNotice", 1, "a BB popup notice is one OOC line again, so a " +
			"whitelist procedure scrolls away unread"},
		{"connectWith", "openServerNotice", 1, "a refused handshake's own explanation (403/429/503) " +
			"is thrown away at the dial"},
	}
	refs := map[string]bool{}
	for _, s := range sites {
		refs[s.ref] = true
	}

	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("glob the package: %v (%d files)", err, len(names))
	}
	// got[function][identifier] — counted per function so App.Frame's three distinct
	// sites are distinguishable from one.
	got := map[string]map[string]int{}
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || !refs[id.Name] {
					return true
				}
				if got[fn.Name.Name] == nil {
					got[fn.Name.Name] = map[string]int{}
				}
				got[fn.Name.Name][id.Name]++
				return true
			})
		}
	}
	for _, s := range sites {
		if n := got[s.fn][s.ref]; n < s.least {
			t.Errorf("%s references %s %d time(s), want at least %d — without it: %s",
				s.fn, s.ref, n, s.least, s.lost)
		}
	}
}
