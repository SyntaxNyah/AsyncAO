package theme

// THE AO2-COMPAT EXPORT's gates (v1.90.0 W8).
//
// Two properties, and both of them are about somebody ELSE's client:
//
//  1. the baked courtroom_design.ini is readable by the Qt-parity parser, i.e.
//     by the reader AO2-Client actually has (ParseINI is this repo's oracle for
//     QSettings::IniFormat, pinned by iniparity_test.go);
//  2. the author's own file, their comments and their unknown keys survive the
//     bake, because the alternative is a theme that stops round-tripping the
//     moment anybody shares it.

import (
	"bytes"
	"strings"
	"testing"
)

// TestAO2CompatExportIsReadableByTheQtParityParser runs our own baked design INI
// back through the Qt oracle and asserts the geometry an AO2 client would read.
func TestAO2CompatExportIsReadableByTheQtParityParser(t *testing.T) {
	upstream := "; the author's own header\n" +
		"courtroom = 0, 0, 256, 192\n" +
		"viewport = 0, 0, 256, 144\n" +
		"ic_chatlog = 8, 8, 100, 40\n" +
		"some_future_key = 1, 2, 3, 4\n"
	s := parsedSidecar(t, "[theme]\nformat = 1\n[overrides]\nic_chatlog = 24, 320, 460, 260\n")

	baked, err := BakeAO2Design([]byte(upstream), s)
	if err != nil {
		t.Fatalf("BakeAO2Design: %v", err)
	}

	ini, err := ParseINI(bytes.NewReader(baked))
	if err != nil {
		t.Fatalf("the baked file is not readable by the Qt-parity parser: %v\n%s", err, baked)
	}
	if got, _ := ini.Get("ic_chatlog"); got != "24, 320, 460, 260" {
		t.Errorf("ic_chatlog reads back as %q — the override did not bake into AO2's own key", got)
	}
	if got, _ := ini.Get("courtroom"); got != "0, 0, 256, 192" {
		t.Errorf("courtroom was disturbed: %q", got)
	}
	// PRESERVATION. A writer that rebuilt the file from its own model would drop
	// the author's comment and the key this build does not know, and an AO2 user's
	// next upstream diff would be noise.
	if !strings.Contains(string(baked), "; the author's own header") {
		t.Errorf("the author's comment is gone:\n%s", baked)
	}
	if got, _ := ini.Get("some_future_key"); got != "1, 2, 3, 4" {
		t.Errorf("an unknown key was dropped: %q", got)
	}
}

// TestBakingAThemeWithNoDesignINIStillProducesOne — a native theme may ship no
// courtroom_design.ini at all (the sidecar has been a standalone source since W3),
// and the AO2 user needs the geometry regardless.
func TestBakingAThemeWithNoDesignINIStillProducesOne(t *testing.T) {
	s := parsedSidecar(t, "[theme]\nformat = 1\n[overrides]\nic_chatlog = 1, 2, 3, 4\n")
	baked, err := BakeAO2Design(nil, s)
	if err != nil {
		t.Fatalf("BakeAO2Design(nil): %v", err)
	}
	ini, err := ParseINI(bytes.NewReader(baked))
	if err != nil {
		t.Fatalf("the produced file is unreadable: %v", err)
	}
	if got, _ := ini.Get("ic_chatlog"); got != "1, 2, 3, 4" {
		t.Errorf("ic_chatlog = %q, want the baked override", got)
	}
}

// TestStripForAO2NeverTouchesTheLiveModel. The editor's document and the applied
// theme are the SAME *Sidecar, so a strip that mutated it in place would move
// every widget in the user's own courtroom the instant they pressed Share.
func TestStripForAO2NeverTouchesTheLiveModel(t *testing.T) {
	s := parsedSidecar(t, "[theme]\nformat = 1\n[overrides]\nic_chatlog = 1, 2, 3, 4\n[rotations]\nic_chatlog = 64\n")
	if len(s.Overrides) != 1 || len(s.Rotations) != 1 {
		t.Fatalf("the fixture did not parse: %d overrides, %d rotations", len(s.Overrides), len(s.Rotations))
	}
	stripped := StripForAO2(s)
	if len(s.Overrides) != 1 || len(s.Rotations) != 1 {
		t.Error("StripForAO2 emptied the LIVE model — the theme on screen would jump on the Share click")
	}
	if len(stripped.Overrides) != 0 || len(stripped.Rotations) != 0 {
		t.Errorf("the exported copy still carries %d overrides and %d rotations, so an AsyncAO client "+
			"opening the bundle would apply them on top of the geometry already baked into the design INI",
			len(stripped.Overrides), len(stripped.Rotations))
	}
	b, err := stripped.Bytes()
	if err != nil {
		t.Fatalf("the stripped copy does not serialise: %v", err)
	}
	if strings.Contains(string(b), "ic_chatlog") {
		t.Errorf("the stripped sidecar still names the baked widget:\n%s", b)
	}
}

// TestTheLostReportNamesEveryDeclaredGap. A file in the bundle beats a dialog in
// our UI — it survives re-share, renaming and re-zipping, and it reaches the AO2
// user who will never see our export sheet. So it has to be complete.
func TestTheLostReportNamesEveryDeclaredGap(t *testing.T) {
	src := "[theme]\nformat = 1\n" +
		"[element.butterflies]\nkind = image\nmedia = wings\n" +
		"[rotations]\nic_chatlog = 64\n" +
		"[pane.split]\nrect = 0, 0, 100, 100\n" +
		"[effect.viewport]\neffect = glow\nperiod_ms = 900\namp_pct = 40\n" +
		"[clock.2]\nspeed_pct = 200\n" +
		"[media]\nwings = wings.webp\n" +
		"[palette]\naccent = #c8a34a\n"
	s := parsedSidecar(t, src)
	report := string(LostReport("Paper", s))

	for _, want := range []string{
		"butterflies", // a free element
		"ic_chatlog",  // a rotation AO2 has no key for
		"split",       // a pane
		"viewport",    // a widget effect
		"clock",       // an animation clock
		"wings",       // media AO2 never references
		"palette",     // AsyncAO palette roles
	} {
		if !strings.Contains(report, want) {
			t.Errorf("%s is missing from %s:\n%s", want, LostFileName, report)
		}
	}
	if !strings.Contains(report, "Paper") {
		t.Error("the report does not name the theme it is about")
	}
	// It explains itself to somebody who has never heard of AsyncAO, because that
	// is exactly who finds this file.
	if !strings.Contains(report, "AsyncAO") || !strings.Contains(report, DesignFileName) {
		t.Errorf("the header does not explain what this file is:\n%s", report)
	}
}

// TestALostReportIsEmptyForAPlainAO2Theme — a theme using only what AO2 already
// understands must not ship a list of nothing dressed up as a warning.
func TestALostReportIsEmptyForAPlainAO2Theme(t *testing.T) {
	s := parsedSidecar(t, "[theme]\nformat = 1\n[overrides]\nic_chatlog = 1, 2, 3, 4\n")
	if lost := LostToAO2(s); len(lost) != 0 {
		t.Errorf("a theme with only baked overrides reports %v as lost — overrides are the one thing "+
			"the bake DOES carry across", lost)
	}
	if !strings.Contains(string(LostReport("Paper", s)), "Nothing") {
		t.Error("the report does not say plainly that nothing is lost")
	}
}
