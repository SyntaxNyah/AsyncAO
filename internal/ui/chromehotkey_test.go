package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// Keyboard toggles for the two chrome bars.
//
// They exist because the only way to hide or show them was the UI-chrome panel,
// which is itself a piece of chrome: anything drawn over it, or a layout that put
// it awkwardly, left no route back except the mouse. A key that does not depend
// on being able to click the thing it controls is the whole point.

// TestChromeTogglesAreRebindableAndListed is the pair of properties the owner
// asked for, and both come from one place: an entry in hotkeyDefs drives the
// Settings row AND the F1 cheat sheet. This fails if either surface stops being
// generated from that table, which is the only way these could silently become
// unbindable or undiscoverable.
func TestChromeTogglesAreRebindableAndListed(t *testing.T) {
	want := map[string]string{
		hotkeyToggleMenuBar: "f5",
		hotkeyToggleToolbox: "f6",
	}
	found := map[string]string{}
	for _, def := range hotkeyDefs {
		if _, ok := want[def.id]; ok {
			found[def.id] = def.def
			if def.label == "" {
				t.Errorf("%s has no label — the Settings row and the F1 sheet would both be blank", def.id)
			}
		}
	}
	for id, key := range want {
		got, ok := found[id]
		if !ok {
			t.Errorf("%s is not in hotkeyDefs, so it is neither rebindable nor listed", id)
			continue
		}
		if got != key {
			t.Errorf("%s defaults to %q, want %q", id, got, key)
		}
	}

	// And they really do reach the F1 sheet, which is built from the same table.
	a := testTabApp(t)
	sheet := a.hotkeyCheatEntries()
	for id := range want {
		label := ""
		for _, def := range hotkeyDefs {
			if def.id == id {
				label = def.label
			}
		}
		listed := false
		for _, e := range sheet {
			if e.label == label {
				listed = true
				break
			}
		}
		if !listed {
			t.Errorf("%q is missing from the F1 hotkey sheet", label)
		}
	}
}

// TestChromeToggleDefaultsAvoidThePlainFunctionKeys: the cheat sheet lists plain
// F1, F3 and F8 as fixed keys in the same window as these Ctrl chords. They are
// separate namespaces and would not actually clash, but a Ctrl+F3 printed
// directly under a plain F3 reads as a bug to whoever is looking at the list.
func TestChromeToggleDefaultsAvoidThePlainFunctionKeys(t *testing.T) {
	fixed := map[string]bool{"f1": true, "f3": true, "f8": true}
	for _, id := range []string{hotkeyToggleMenuBar, hotkeyToggleToolbox} {
		for _, def := range hotkeyDefs {
			if def.id == id && fixed[def.def] {
				t.Errorf("%s defaults to %q, which the cheat sheet also lists as a plain fixed key", id, def.def)
			}
		}
	}
}

// TestChromeToggleDefaultsAvoidTheTakenSpace: the Ctrl+letter, Ctrl+digit and
// Ctrl+symbol space is documented as exhausted, and the clipboard and
// editor-undo chords claim what is left. These two therefore have to live in the
// function-key band, and must not collide with anything already there or with
// each other.
func TestChromeToggleDefaultsAvoidTheTakenSpace(t *testing.T) {
	seen := map[string]string{}
	for _, def := range hotkeyDefs {
		if prev, dup := seen[def.def]; dup {
			t.Errorf("default %q is claimed by both %q and %q", def.def, prev, def.id)
		}
		seen[def.def] = def.id
	}
	for _, id := range []string{hotkeyToggleMenuBar, hotkeyToggleToolbox} {
		for _, def := range hotkeyDefs {
			if def.id != id {
				continue
			}
			if !strings.HasPrefix(def.def, "f") || len(def.def) < 2 {
				t.Errorf("%s defaults to %q — the printable Ctrl space is full, these belong on a function key",
					id, def.def)
			}
		}
	}
}

// TestChromeToggleFlipsAndReports: pressing it has to do something visible. A
// bind whose whole purpose is being usable when you cannot see the panel would
// be useless if it gave no feedback about which way it went.
func TestChromeToggleFlipsAndReports(t *testing.T) {
	a := testTabApp(t)
	if a.panelHidden(panelToolbox) {
		t.Fatal("the toolbox starts hidden in this fixture; the test assumes shown")
	}
	a.toggleChromePanel(panelToolbox, "Toolbox")
	if !a.panelHidden(panelToolbox) {
		t.Fatal("the toggle did not hide the toolbox")
	}
	if !strings.Contains(a.warnLine, "hidden") {
		t.Errorf("no feedback after hiding: %q", a.warnLine)
	}
	a.toggleChromePanel(panelToolbox, "Toolbox")
	if a.panelHidden(panelToolbox) {
		t.Fatal("the toggle did not bring the toolbox back")
	}
	if !strings.Contains(a.warnLine, "shown") {
		t.Errorf("no feedback after showing: %q", a.warnLine)
	}
}

// TestEveryBuiltInRowIsRebindableFromTheSheet: the F1 window is where people go
// to find out what a key does, so it is where they want to change it. Every row
// built from hotkeyDefs must carry its action id — that is what makes it
// clickable — while the fixed keys and the sections with their own editors must
// NOT, or clicking them would arm a capture that binds nothing.
func TestEveryBuiltInRowIsRebindableFromTheSheet(t *testing.T) {
	a := testTabApp(t)
	byLabel := map[string]string{}
	for _, def := range hotkeyDefs {
		byLabel[def.label] = def.id
	}
	seen := 0
	for _, e := range a.hotkeyCheatEntries() {
		if e.header {
			continue
		}
		id, isBuiltIn := byLabel[e.label]
		switch {
		case isBuiltIn && e.action != id:
			t.Errorf("row %q carries action %q, want %q — it would rebind the wrong action or none", e.label, e.action, id)
		case isBuiltIn:
			seen++
		case e.action != "":
			t.Errorf("row %q is not a hotkeyDefs action but carries %q — clicking it would arm a capture that binds nothing",
				e.label, e.action)
		}
	}
	if seen != len(hotkeyDefs) {
		t.Errorf("%d of %d built-in actions reached the sheet as rebindable rows", seen, len(hotkeyDefs))
	}
}

// TestHotkeyCaptureCompletesOutsideSettings is the bug the shared capture exists
// to prevent. Consuming the keypress used to live inside the Settings draw, so a
// rebind armed from the F1 sheet would never complete — the row would sit on
// "press a key…" until the user opened Settings, which is exactly the detour the
// sheet-side rebinding was added to remove.
func TestHotkeyCaptureCompletesOutsideSettings(t *testing.T) {
	a := testTabApp(t)
	a.hkCapture = hotkeyToggleToolbox
	a.hkCache = a.hotkeyCheatEntries()
	a.ctx.keyPressed = sdl.K_F9

	a.consumeHotkeyCapture()

	if a.hkCapture != "" {
		t.Error("the capture never completed — the row would stay armed forever")
	}
	if got := a.hotkeyFor(hotkeyToggleToolbox); got != "f9" {
		t.Errorf("bound key = %q, want f9", got)
	}
	if a.ctx.keyPressed != 0 {
		t.Error("the captured key was not consumed; it would also fire whatever else it is bound to")
	}
	if a.hkCache != nil {
		t.Error("the sheet's cached rows survived a rebind — it would keep showing the old key")
	}

	// Esc cancels without binding anything.
	a.hkCapture = hotkeyToggleMenuBar
	before := a.hotkeyFor(hotkeyToggleMenuBar)
	a.ctx.keyPressed = sdl.K_ESCAPE
	a.consumeHotkeyCapture()
	if a.hkCapture != "" {
		t.Error("Esc did not disarm the capture")
	}
	if got := a.hotkeyFor(hotkeyToggleMenuBar); got != before {
		t.Errorf("Esc rebound the action to %q", got)
	}
}

// TestChromeToggleCannotStrandYou is the reason the hotkey routes through the
// GUARDED setter. A key that can hide the last mouse route back to the client's
// controls is worse than no key at all, because the person who pressed it is by
// definition the one who could not find the way back.
func TestChromeToggleCannotStrandYou(t *testing.T) {
	a := testTabApp(t)
	// Hide every lifeline but the last, the way a determined user would.
	a.setPanelHiddenGuarded(ctrlSettingsSlot, true)
	a.setPanelHiddenGuarded(panelToolbox, true)
	a.warnLine = ""

	a.toggleChromePanel(panelMenuBar, "Menu bar")
	if a.panelHidden(panelMenuBar) {
		t.Error("the hotkey hid the LAST mouse lifeline — there would be no way back to the controls")
	}
	if a.warnLine == "" {
		t.Error("the refusal was silent; the user would think the key does nothing")
	}
	if strings.Contains(a.warnLine, "hidden") {
		t.Errorf("the refusal reported success: %q", a.warnLine)
	}
}
