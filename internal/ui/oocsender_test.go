package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Issue #68: the OOC log colours a sender's name by who sent the line, as AO2's append_server_chatmessage does.
func TestOOCSenderNameInk(t *testing.T) {
	const wide = 1000
	serverInk := sdl.Color{R: 250, G: 200, B: 40, A: 255}
	playerInk := sdl.Color{R: 90, G: 220, B: 250, A: 255}
	a := testTabApp(t)
	a.oocPct = DefaultScalePct
	a.pushOOC("Server: welcome", "Server", true)
	a.pushOOC("Bob: hi", "Bob", false)
	a.oocWrapped(wide)
	const serverRow, playerRow = 0, 1
	plain := logNameInk{}
	perSpeaker := logNameInk{perSpeaker: true}

	if ink := a.oocRowNameInk(serverRow, plain, true); !ink.fixedSet || ink.fixed != ColAccent {
		t.Errorf("server line without a theme colour: %+v, want the accent", ink)
	}
	if ink := a.oocRowNameInk(playerRow, plain, true); ink.tints() {
		t.Errorf("player line without a theme colour tints its name: %+v", ink)
	}

	a.themeFonts.senderInk[oocFromServer], a.themeFonts.senderInkSet[oocFromServer] = serverInk, true
	a.themeFonts.senderInk[oocFromPlayer], a.themeFonts.senderInkSet[oocFromPlayer] = playerInk, true
	if ink := a.oocRowNameInk(serverRow, perSpeaker, true); ink.of("Server", ColText) != serverInk {
		t.Errorf("server line on the theme's canvas: %+v, want server_chatlog_sender_color", ink)
	}
	if ink := a.oocRowNameInk(playerRow, plain, true); ink.of("Bob", ColText) != playerInk {
		t.Errorf("player line on the theme's canvas: %+v, want ms_chatlog_sender_color", ink)
	}
	if ink := a.oocRowNameInk(playerRow, perSpeaker, true); ink.fixedSet {
		t.Errorf("per-speaker colours were turned on, yet the theme's player colour won: %+v", ink)
	}
	if ink := a.oocRowNameInk(serverRow, plain, false); ink.fixed != ColAccent {
		t.Errorf("off the theme's canvas the server line took the theme colour: %+v", ink)
	}
}

// A theme sender colour that can't be read on the log's backdrop is dropped like any other theme ink.
func TestUnreadableSenderInkIsDropped(t *testing.T) {
	tbl := themeFontTable{}
	tbl.senderInk[oocFromServer], tbl.senderInkSet[oocFromServer] = ColPanel, true
	guardThemeInk(&themeApply{}, &tbl)
	if tbl.senderInkSet[oocFromServer] {
		t.Error("a sender colour equal to the panel was kept")
	}
}
