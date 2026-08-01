package ui

import (
	"strings"
	"testing"
)

// TestFontWarnDialogOpensOncePerTheme is the whole design constraint made
// testable. A theme with missing fonts has them missing on EVERY apply — every
// reload, every restart, every time the user pokes a setting that re-applies the
// theme. Warning each time would make the client unusable for anyone who does not
// intend to install the fonts, and the second showing is what teaches people to
// dismiss a dialog without reading it.
func TestFontWarnDialogOpensOncePerTheme(t *testing.T) {
	a := testTabApp(t)
	fams := []string{"Ace Name", "Igiari Cyrillic"}

	if !a.maybeWarnMissingFonts("AceAttorney 2x", fams, "body", `C:\fonts`) {
		t.Fatal("the first apply of a theme with missing fonts must warn")
	}
	if !a.fontWarnDlg.open {
		t.Fatal("it reported warning but left the dialog closed")
	}
	a.fontWarnDlg = fontWarnDialog{} // the user dismissed it

	if a.maybeWarnMissingFonts("AceAttorney 2x", fams, "body", `C:\fonts`) {
		t.Error("the SAME theme warned twice — every reload would reopen this")
	}
	if a.fontWarnDlg.open {
		t.Error("a repeat apply reopened the dialog")
	}

	// A DIFFERENT theme is a different warning: its fonts are different files.
	if !a.maybeWarnMissingFonts("DRRA 2x Widescreen", fams, "body", `C:\fonts`) {
		t.Error("a second theme with missing fonts must warn on its own")
	}
}

// TestFontWarnDialogSurvivesARestart: the remembered set is persisted, so a
// dismissed warning stays dismissed. Held in the preferences rather than in
// memory precisely so "once per theme" does not silently mean "once per launch".
func TestFontWarnDialogSurvivesARestart(t *testing.T) {
	a := testTabApp(t)
	a.maybeWarnMissingFonts("AceAttorney 2x", []string{"Ace Name"}, "body", "")
	if !a.d.Prefs.FontWarnShownFor("AceAttorney 2x") {
		t.Fatal("showing the dialog did not record the theme")
	}
	// Case-insensitively, because the same theme arrives spelled differently from
	// a folder name and from a saved preference.
	if !a.d.Prefs.FontWarnShownFor("aceattorney 2x") {
		t.Error("the remembered set is case-sensitive; the same theme would warn again")
	}
	a.d.Prefs.ClearFontWarnThemes()
	if a.d.Prefs.FontWarnShownFor("AceAttorney 2x") {
		t.Error("clearing the set did not bring the warnings back")
	}
}

// TestFontWarnDialogStaysShutWhenNothingIsMissing: this must never be the reason
// somebody sees a dialog. A theme that resolved everything, or one we have no
// message for, opens nothing.
func TestFontWarnDialogStaysShutWhenNothingIsMissing(t *testing.T) {
	a := testTabApp(t)
	for _, tc := range []struct {
		name  string
		fams  []string
		body  string
		label string
	}{
		{"Theme A", nil, "body", "no missing families"},
		{"Theme B", []string{"Ace Name"}, "", "no message to show"},
	} {
		if a.maybeWarnMissingFonts(tc.name, tc.fams, tc.body, "") {
			t.Errorf("%s: warned with %s", tc.name, tc.label)
		}
		if a.fontWarnDlg.open {
			t.Errorf("%s: dialog opened with %s", tc.name, tc.label)
		}
	}
	// An unnamed theme cannot be remembered, so warning about it would warn on
	// every single apply. It must stay silent rather than nag.
	if a.maybeWarnMissingFonts("", []string{"Ace Name"}, "body", "") {
		t.Error("warned about a theme with no name — nothing could ever mark it seen")
	}
}

// TestThemeFontWarningNamesTheRemedy: the message's job is to be actionable. It
// has to list the families (what to go and find) and say where to put them, and
// it says something DIFFERENT when the installation has no fonts at all — that
// user needs a base, not a file copy.
func TestThemeFontWarningNamesTheRemedy(t *testing.T) {
	fams := []string{"Ace Name", "Igiari Cyrillic"}

	withDir := themeFontWarning(fams, true, `C:\AsyncAO\fonts`)
	for _, want := range []string{"Ace Name", "Igiari Cyrillic", `C:\AsyncAO\fonts`, "reload"} {
		if !strings.Contains(withDir, want) {
			t.Errorf("message %q is missing %q", withDir, want)
		}
	}

	// No fonts anywhere: almost certainly a themes-only download. Say so, because
	// it is the actual situation and it is nobody's mistake.
	noFonts := themeFontWarning(fams, false, `C:\AsyncAO\fonts`)
	if !strings.Contains(noFonts, "base") {
		t.Errorf("with no fonts on disk the message must mention the base: %q", noFonts)
	}

	// No drop folder (a read-only config location): still actionable, just via the
	// theme's own folder — never a dangling "put them in " with no path.
	noDir := themeFontWarning(fams, true, "")
	if strings.Contains(noDir, "  ") || strings.HasSuffix(strings.TrimSpace(noDir), "in") {
		t.Errorf("message has a hole where the folder should be: %q", noDir)
	}
	if !strings.Contains(noDir, "theme's own folder") {
		t.Errorf("with no drop folder the message must point at the theme folder: %q", noDir)
	}

	if got := themeFontWarning(nil, true, `C:\fonts`); got != "" {
		t.Errorf("nothing missing produced a message: %q", got)
	}
}
