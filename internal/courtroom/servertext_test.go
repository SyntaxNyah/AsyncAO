package courtroom

// The wire-seam gates for SERVER-AUTHORED PROSE — a KK/KB kick reason, a BD ban
// reason (which is what a locked-down or whitelisting server sends), and a BB popup
// notice. All four are attacker-controlled strings that end up in a texture and on
// the clipboard, and all four are capped here rather than at the four consumers.
//
// Every gate below drives the REAL path: a Packet built by protocol.NewPacket,
// serialized with its own escaping, parsed back by protocol.ParsePacket, and handed
// to the real Session.HandlePacket. Nothing re-implements the cap — a test that
// called capServerText directly and compared it with its own truncation would stay
// green with the call sites deleted, which is the failure mode rule 11 names.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// wireEvents round-trips one packet through the real framing and returns what the
// session made of it. The round trip is the point: it is the only way the escaping
// (#%$& → <num>/<percent>/<dollar>/<and>) is exercised alongside the cap, and a
// removal reason with a literal '#' in it is not hypothetical.
func wireEvents(t *testing.T, s *Session, header string, fields ...string) []Event {
	t.Helper()
	p, err := protocol.ParsePacket(protocol.NewPacket(header, fields...).String())
	if err != nil {
		t.Fatalf("the %s packet did not survive its own encoder: %v", header, err)
	}
	return s.HandlePacket(p)
}

// newTestSession is a session with a send hook that cannot fail and no HDID of
// consequence; none of the gates here send anything.
func newTestSession() *Session {
	return NewSession(func(protocol.Packet) error { return nil }, "h")
}

// oneEvent asserts exactly one event came back and returns it.
func oneEvent(t *testing.T, evs []Event) Event {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("want exactly 1 event, got %d: %+v", len(evs), evs)
	}
	return evs[0]
}

// TestARemovalReasonArrivesWholeAndKeepsItsShape is the baseline the cap must not
// disturb: the lockdown payload this whole change exists for, through the wire.
//
// It pins three things a one-line label could not carry and the cap must not eat:
// the INTERIOR NEWLINES (a lockdown puts its access code on the last line), the
// Cyrillic (the reported server is Russian), and the "Banned: " prefix that four
// consumers in internal/ui match with strings.HasPrefix.
func TestARemovalReasonArrivesWholeAndKeepsItsShape(t *testing.T) {
	const reason = "Вы не находитесь в вайт-листе сервера.\n" +
		"Присоединитесь к нашему Discord: https://discord.gg/n95zkcBE8h\n" +
		"Код доступа: 1234"

	ev := oneEvent(t, wireEvents(t, newTestSession(), "BD", reason))
	if ev.Kind != EventDisconnect {
		t.Fatalf("BD produced kind %v, want EventDisconnect", ev.Kind)
	}
	if want := "Banned: " + reason; ev.Text != want {
		t.Fatalf("the reason did not survive the wire intact:\n got %q\nwant %q", ev.Text, want)
	}
	if strings.Count(ev.Text, "\n") != 2 {
		t.Fatalf("the reason lost its line breaks (%d left) — the access code is on the last one",
			strings.Count(ev.Text, "\n"))
	}
}

// TestARemovalReasonKeepsItsPrefixWhenTheCapBites is the ban-evasion gate.
//
// The cap is applied to the WIRE FIELD, before the prefix is concatenated. If it were
// applied to the joined string instead, a small enough cap would cut into the
// "Banned: " literal itself — and shouldAutoReconnect (internal/ui/reconnect.go)
// decides whether to redial by matching exactly that prefix. A reason that stopped
// matching would re-arm auto-reconnect against a ban, which is the client arguing
// with a moderator on the user's behalf.
//
// So: a pathological reason must still be prefixed, still be capped, and SAY it was
// capped. The over-cap payload is Cyrillic on purpose — see the rune gate below.
func TestARemovalReasonKeepsItsPrefixWhenTheCapBites(t *testing.T) {
	huge := strings.Repeat("б", serverTextRuneCap+500)

	for _, header := range []string{"KK", "KB", "BD"} {
		prefix := "Banned: "
		if header == "KK" {
			prefix = "Kicked: "
		}
		ev := oneEvent(t, wireEvents(t, newTestSession(), header, huge))
		if !strings.HasPrefix(ev.Text, prefix) {
			t.Fatalf("%s lost its %q prefix under the cap (%.20q…) — shouldAutoReconnect "+
				"matches on it, so this would re-arm a redial against a ban", header, prefix, ev.Text)
		}
		body := strings.TrimPrefix(ev.Text, prefix)
		if n := utf8.RuneCountInString(body); n > serverTextRuneCap+1 {
			t.Errorf("%s kept %d runes, past the %d cap (+1 for the marker)", header, n, serverTextRuneCap)
		}
		if !strings.HasSuffix(body, "…") {
			t.Errorf("%s truncated silently — a copied half-message must not read as the whole one", header)
		}
	}
}

// TestTheServerTextCapCountsRunesNotBytes is the Cyrillic half, and it is the reason
// clampWireBytes (profilewire.go) was not reused.
//
// Exactly serverTextRuneCap Cyrillic runes is TWICE that many bytes. Under a byte cap
// this payload comes back truncated; under the rune cap it comes back whole. A
// Russian server's whitelist notice is the shape that makes the difference real.
func TestTheServerTextCapCountsRunesNotBytes(t *testing.T) {
	atCap := strings.Repeat("б", serverTextRuneCap)
	if len(atCap) != 2*serverTextRuneCap {
		t.Fatalf("fixture is not two bytes per rune (%d bytes for %d runes) — this gate "+
			"cannot tell a byte cap from a rune cap", len(atCap), serverTextRuneCap)
	}

	ev := oneEvent(t, wireEvents(t, newTestSession(), "BD", atCap))
	if got := strings.TrimPrefix(ev.Text, "Banned: "); got != atCap {
		t.Fatalf("a payload of exactly %d runes was cut to %d — the cap is counting bytes",
			serverTextRuneCap, utf8.RuneCountInString(got))
	}
}

// TestABBNoticeReachesTheUICappedAndAnEmptyOneDoesNot pins both ends of the popup
// path. A notice with words becomes an EventNotice the UI can raise a box for; a BB
// with no fields at all stays nil rather than becoming an empty modal.
func TestABBNoticeReachesTheUICappedAndAnEmptyOneDoesNot(t *testing.T) {
	const notice = "Rules:\n1. No metagaming.\n2. Ask a CM before joining a case."
	ev := oneEvent(t, wireEvents(t, newTestSession(), "BB", notice))
	if ev.Kind != EventNotice || ev.Text != notice {
		t.Fatalf("BB produced %v %q, want EventNotice %q", ev.Kind, ev.Text, notice)
	}

	if evs := wireEvents(t, newTestSession(), "BB"); len(evs) != 0 {
		t.Fatalf("a fieldless BB produced %+v — an empty notice is a modal with nothing in it", evs)
	}

	huge := oneEvent(t, wireEvents(t, newTestSession(), "BB", strings.Repeat("x", serverTextRuneCap*2)))
	if n := utf8.RuneCountInString(huge.Text); n > serverTextRuneCap+1 {
		t.Errorf("a BB notice kept %d runes, past the %d cap", n, serverTextRuneCap)
	}
}

// TestAControlByteCannotTruncateAServerMessage is the scrub's behavioural gate, and the
// attack it closes is one byte long.
//
// Every drawn row ends in SDL_ttf and every Copy ends in SDL_SetClipboardText, and both
// marshal through C.CString, which is NUL-TERMINATED. A server that puts a single \x00 in
// front of its own access code would therefore have the box draw, and Copy hand over,
// everything before that byte and nothing after it — the same unreachable tail this whole
// change exists to end, arriving through a different door. The scrub runs inside the cap,
// so it is applied before the rune count and what gets counted is what can be shown.
//
// All four prose headers, because the scrub lives in the one shared cap and a per-case
// hand-rolled copy is exactly what must never appear.
func TestAControlByteCannotTruncateAServerMessage(t *testing.T) {
	const code = "WHITELIST-7781"
	// The NUL sits immediately before the payload's most valuable line, which is where a
	// hostile server would put it. ESC and BEL ride along as the rest of the
	// not-layout family, and the \r as the CRLF case real servers actually send.
	const hostile = "You are not on the whitelist.\x1b[2J\r\n" +
		"Ask in our Discord.\x07\n" +
		"\x00Access code: " + code

	for _, header := range []string{"KK", "KB", "BD", "BB"} {
		ev := oneEvent(t, wireEvents(t, newTestSession(), header, hostile))
		if !strings.HasSuffix(ev.Text, code) {
			t.Errorf("%s: the access code did not survive to the end of the message: %q", header, ev.Text)
		}
		for _, r := range ev.Text {
			if r == '\n' || r == '\t' {
				continue // layout, and the only two controls that carry meaning here
			}
			if unicode.IsControl(r) {
				t.Errorf("%s: %#U survived into a string that goes to C.CString — one NUL there "+
					"truncates the draw and the clipboard together", header, r)
				break
			}
		}
		// The LAYOUT has to survive the scrub. A lockdown notice puts its code on the last
		// line, so the breaks are content: two in, two out, with the \r dropped without
		// taking its \n along.
		if n := strings.Count(ev.Text, "\n"); n != 2 {
			t.Errorf("%s: %d line breaks survived, want 2 — the scrub is eating the layout", header, n)
		}
	}
}

// TestTheCapUsesTheSharedScrub is the anti-deletion gate for the scrub, and it names
// protocol.SanitizeText on purpose.
//
// A local copy of the same logic would pass every behavioural gate above while quietly
// becoming a second rule to keep in step with the dial-refusal reader in
// internal/protocol — and the two guarantees must be identical, because the notice box
// shows both through the same draw and copies both with the same button. The shared
// function is the mechanism; this is what fails when it is inlined away.
func TestTheCapUsesTheSharedScrub(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "session.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse session.go: %v", err)
	}
	found, seen := false, false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "capServerText" || fn.Recv != nil {
			continue
		}
		seen = true
		ast.Inspect(fn, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "SanitizeText" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "protocol" {
				found = true
			}
			return true
		})
	}
	if !seen {
		t.Fatal("capServerText is gone from session.go — this gate is no longer watching anything")
	}
	if !found {
		t.Error("capServerText does not call protocol.SanitizeText — a NUL now reaches SDL_ttf and " +
			"SDL_SetClipboardText, truncating both at that byte, and the scrub rule has forked " +
			"from internal/protocol's refusal-body reader")
	}
}

// TestEveryServerProseCaseIsCapped is the anti-deletion gate for the seam itself.
//
// The cap is one call in each of four case clauses, and nothing else in the package
// would notice if one were dropped: the events still flow, every other test still
// passes, and the hole only shows up as a hostile server's megabyte reason reaching a
// texture. So this reads session.go's own AST and requires the call to be THERE, in
// each clause, by name.
//
// It is deliberately structural rather than behavioural. The behavioural gates above
// cover today's four headers; this one is what fails when a fifth server-prose packet
// is added to the switch without a cap, or when one of the four is refactored and the
// call is lost in the move.
func TestEveryServerProseCaseIsCapped(t *testing.T) {
	const capFn = "capServerText"
	want := map[string]bool{"KK": false, "KB": false, "BD": false, "BB": false}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "session.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse session.go: %v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			header := strings.Trim(lit.Value, `"`)
			if _, tracked := want[header]; !tracked {
				continue
			}
			called := false
			for _, stmt := range clause.Body {
				ast.Inspect(stmt, func(inner ast.Node) bool {
					if id, ok := inner.(*ast.Ident); ok && id.Name == capFn {
						called = true
					}
					return true
				})
			}
			if called {
				want[header] = true
			} else {
				t.Errorf("the %q case does not call %s — server prose reaches internal/ui uncapped "+
					"from %s", header, capFn, fset.Position(clause.Pos()))
			}
		}
		return true
	})
	for header, found := range want {
		if !found {
			t.Errorf("no %q case found in session.go at all — this gate is no longer watching it", header)
		}
	}
}
