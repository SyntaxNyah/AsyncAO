package ui

import (
	"path/filepath"
	"testing"
	"time"
)

// TestDroppedThemeBundleNeverRepointsTheThemeRoot is the W2 gate on drop
// routing, and it is written from the failure rather than from the feature.
// The fix is one shared classifier, so the gate is: the bundle is claimed by
// HandleFileDrop, the Settings screen's arm therefore never reaches its
// theme-folder default, and the theme root is untouched by the drop.
func TestDroppedThemeBundleNeverRepointsTheThemeRoot(t *testing.T) {
	const bundle = `C:\Users\x\Downloads\umineko-meta-world.aotheme`

	if got := claimDroppedFile(bundle, false); got != dropClaimThemeBundle {
		t.Fatalf("claim(%q) = %d, want dropClaimThemeBundle", bundle, got)
	}
	// The arming state must not change the answer: a bundle is a bundle even
	// while a settings import is armed.
	if got := claimDroppedFile(bundle, true); got != dropClaimThemeBundle {
		t.Errorf("an armed settings import stole the theme bundle")
	}
	// Case is not part of the extension.
	if got := claimDroppedFile(`C:\x\THEME.AOTHEME`, false); got != dropClaimThemeBundle {
		t.Errorf("upper-case %s was not recognised", themePackExt)
	}

	// The Settings screen's routing: anything CLAIMED is a no-op there, and only
	// the unclaimed default reaches resolveDroppedFolder.
	for _, tc := range []struct {
		path       string
		armed      bool
		claim      dropClaim
		repoints   bool
		whyNotSafe string
	}{
		{bundle, false, dropClaimThemeBundle, false, "a bundle's parent folder is not a theme root"},
		{`C:\x\clip.aorec`, false, dropClaimRecording, false, "HandleFileDrop imports and plays recordings"},
		{`C:\x\scene.demo`, false, dropClaimRecording, false, "HandleFileDrop routes demos"},
		{`C:\x\asyncao-settings.json`, true, dropClaimSettingsImport, false, "the armed import consumes it"},
		{`C:\x\MyThemes`, false, dropClaimNone, true, "a dropped folder IS the documented theme import (#21)"},
		{`C:\x\readme.txt`, false, dropClaimNone, true, "an unclaimed file still means 'use its folder'"},
		// A .json with nothing armed is NOT a settings import: it falls through
		// to the folder arm exactly as it did before this wave.
		{`C:\x\asyncao-settings.json`, false, dropClaimNone, true, "unarmed .json keeps its old behaviour"},
		// W4's font intake: a face file is a FILE, so it carried the same
		// repoint-the-theme-root bug the bundle did.
		{`C:\x\Downloads\CourtSerif.ttf`, false, dropClaimThemeFont, false, "a font's parent folder is not a theme root"},
		{`C:\x\Downloads\Igiari.OTF`, false, dropClaimThemeFont, false, "the extension test is case-blind"},
		{`C:\x\Downloads\Igiari.otf`, true, dropClaimThemeFont, false, "an armed settings import must not steal a font"},
		// A collection is deliberately NOT claimed: internFace opens ONE face out
		// of a file, so claiming a container we cannot open would trade a wrong
		// action for a wrong promise.
		{`C:\x\Downloads\Menlo.ttc`, false, dropClaimNone, true, ".ttc keeps its old behaviour"},
	} {
		got := claimDroppedFile(tc.path, tc.armed)
		if got != tc.claim {
			t.Errorf("claim(%q, armed=%v) = %d, want %d — %s", tc.path, tc.armed, got, tc.claim, tc.whyNotSafe)
		}
		act := settingsDropAction(got)
		if reaches := act == settingsDropRepointThemeRoot; reaches != tc.repoints {
			t.Errorf("%q reaches the theme-folder arm = %v, want %v — %s", tc.path, reaches, tc.repoints, tc.whyNotSafe)
		}
		// And the import arm is exactly as narrow: the only path that consumes a
		// drop into the settings importer is the one the user armed it for.
		if wantImport := tc.claim == dropClaimSettingsImport; (act == settingsDropImportSettings) != wantImport {
			t.Errorf("%q routes to the settings importer = %v, want %v", tc.path, act == settingsDropImportSettings, wantImport)
		}
	}
}

// dropClaimCensusMin is the smallest roster the claim census accepts before it calls
// itself vacuous. The enum carries six claims today; the floor is deliberately well
// under that, because the point is to catch a walk that stopped walking, not to restate
// the count — the same bargain settingsTabBodyCensusMin strikes in settingscardwidth_test.go.
const dropClaimCensusMin = 3

// TestEveryDropClaimHasASettingsAnswer keeps settingsDropAction total over
// dropClaim, walking the enum by VALUE so a claim appended to the iota block
// joins this gate the moment it exists. The bound is the SENTINEL
// (c < dropClaimCount), not the last named member: the walk once ended at
// dropClaimThemeFont and W10's dropClaimThemeImage slipped past it.
// The sentinel itself is guarded by the compiler (dropClaimNames' fixed array),
// by the empty-name loop at the foot of this test (sentinel too large, or a
// claim added with no name) and by dropClaimCensusMin. Do not remove the loop:
func TestEveryDropClaimHasASettingsAnswer(t *testing.T) {
	// The Settings screen may only repoint the theme root for the UNCLAIMED case.
	// Every other claim names an owner, and an owner means "not this screen".
	seen := 0
	for c := dropClaimNone; c < dropClaimCount; c++ {
		seen++
		act := settingsDropAction(c)
		if c != dropClaimNone && act == settingsDropRepointThemeRoot {
			t.Errorf("dropClaim %s (%d) has an owner but still reaches the theme-folder arm — a dropped FILE would "+
				"become 'use its parent folder as the theme root', silently, while its real owner warns about it", c, c)
		}
		if c == dropClaimNone && act != settingsDropRepointThemeRoot {
			t.Errorf("an UNCLAIMED drop no longer reaches the theme-folder arm — that is the documented folder " +
				"import path (#21) and dropping a base folder would stop working")
		}
	}
	// The walk is bounded by the SENTINEL, and this clause is what says so out loud. It
	// is tautological only for as long as that remains true, and that conditionality is
	// the point: revert the bound to a named member (`c <= dropClaimThemeFont`, which is
	// what W10 actually shipped) or hand-list the roster, and this is the only check in
	// the file that notices. It is not redundant with the floor below — see
	// dropClaimCensusMin's declaration for the three-way split.
	if seen != int(dropClaimCount) {
		t.Fatalf("the census walked %d of %d claims — the walk is no longer bounded by the sentinel, "+
			"which is exactly how dropClaimThemeImage shipped uncovered", seen, int(dropClaimCount))
	}
	// Non-vacuity, because a census that walks nothing passes — and so does one whose
	// enum shrank to a single claim, which the clause above cannot see. The floor is a
	// named literal rather than the enum's own count for the reason given at its
	// declaration: a bound cannot audit itself.
	if seen < dropClaimCensusMin {
		t.Fatalf("the census walked %d claims, want at least %d — the loop no longer reaches the enum "+
			"and this gate is checking almost nothing", seen, dropClaimCensusMin)
	}
	// Every walked value is a NAMED claim, which is what proves the sentinel is a
	// sentinel: an unnamed slot means dropClaimCount sits past the block's real end.
	for c := dropClaimNone; c < dropClaimCount; c++ {
		if dropClaimNames[c] == "" {
			t.Errorf("dropClaim %d has no name — dropClaimCount is out of step with the iota block", c)
		}
	}
	// And the sentinel itself is not an owner: a value past the enum must fall to
	// the documented folder arm, not be quietly claimed by an arm that grew too wide.
	if settingsDropAction(dropClaimCount) != settingsDropRepointThemeRoot {
		t.Errorf("the sentinel is being treated as a real claim — settingsDropAction's arms have outgrown the enum")
	}
}

// TestThemeBundleDropSaysWhereToPutIt pins the other half of the parked import:
// the drop must not be silent. A drop that does nothing and says nothing is
// indistinguishable from a broken client, which is how the .aotheme routing
// would be read if it only stopped doing the wrong thing.
func TestThemeBundleDropSaysWhereToPutIt(t *testing.T) {
	a := &App{env: env{}}
	before := a.warnAt
	a.handleThemeBundleDrop(filepath.FromSlash("C:/Users/x/Downloads/umineko-meta-world.aotheme"))
	if a.warnLine == "" {
		t.Fatal("a dropped theme bundle produced no message at all")
	}
	if a.warnAt.Equal(before) || a.warnAt.IsZero() {
		t.Errorf("warnAt was not stamped, so the line would never show or never expire")
	}
	if a.warnAt.After(time.Now().Add(time.Second)) {
		t.Errorf("warnAt is in the future: %v", a.warnAt)
	}
	// It names the file, so a user who dropped several knows which one this is
	// about, and it does not claim the import happened.
	if !containsFold(a.warnLine, "umineko-meta-world.aotheme") {
		t.Errorf("message %q does not name the dropped file", a.warnLine)
	}
	for _, lie := range []string{"imported", "installed", "added to your themes"} {
		if containsFold(a.warnLine, lie) {
			t.Errorf("message %q claims %q, but nothing was imported this wave", a.warnLine, lie)
		}
	}
}

// TestThemeFontDropSaysWhereToPutIt is the same contract for W4's font intake, and
// it exists for the same reason: the claim alone only stops the WRONG thing (the
// theme root no longer gets repointed at the font's folder). Without a line, the
// drop would become silent, which reads as a broken client.
//
// It also pins that the message names both INI sections. `[fonts]` alone installs
// nothing — a family with no `[fontbind]` row binds to no element — and that pair
// is the single most common way this feature is got wrong.
func TestThemeFontDropSaysWhereToPutIt(t *testing.T) {
	a := &App{env: env{}}
	a.handleThemeFontDrop(filepath.FromSlash("C:/Users/x/Downloads/CourtSerif.ttf"))
	if a.warnLine == "" {
		t.Fatal("a dropped font produced no message at all")
	}
	if a.warnAt.IsZero() || a.warnAt.After(time.Now().Add(time.Second)) {
		t.Errorf("warnAt is %v — the line would never show, never expire, or both", a.warnAt)
	}
	if !containsFold(a.warnLine, "CourtSerif.ttf") {
		t.Errorf("message %q does not name the dropped file", a.warnLine)
	}
	for _, key := range []string{"fonts", "fontbind"} {
		if !containsFold(a.warnLine, key) {
			t.Errorf("message %q never mentions [%s] — a face the theme ships and binds to nothing does nothing", a.warnLine, key)
		}
	}
	for _, lie := range []string{"imported", "installed", "applied"} {
		if containsFold(a.warnLine, lie) {
			t.Errorf("message %q claims %q, but the writer is the editor's (W7)", a.warnLine, lie)
		}
	}
}
