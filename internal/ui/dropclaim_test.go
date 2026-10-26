package ui

import (
	"path/filepath"
	"testing"
	"time"
)

// TestDroppedThemeBundleNeverRepointsTheThemeRoot is the W2 gate on drop
// routing, and it is written from the failure rather than from the feature.
//
// The bug: SDL hands every drop to HandleFileDrop AND to the visible screen in
// the same frame. The Settings screen's default arm calls resolveDroppedFolder,
// which turns a dropped FILE into "its parent folder" and stores that as the
// user's theme root. So dropping a .aotheme on the settings page repointed the
// theme root at (typically) the Downloads folder — silently, while the global
// handler was warning that the file "isn't a recording". Every theme the user
// had resolved through the old root stopped resolving.
//
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
	//
	// It DRIVES settingsDropAction (dropclaim.go) rather than restating the
	// switch. That distinction is the whole gate: the arm used to live inline in
	// drawSettings, which zero tests call, and the check here used to be
	// `got == dropClaimNone` — a re-implementation. Deleting the
	// `case dropClaimRecording, dropClaimThemeBundle, dropClaimThemeFont:` line
	// therefore left the suite green while a dropped .aotheme or .ttf silently
	// repointed the user's theme root at their Downloads folder.
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
//
// IT IS A LITERAL, INDEPENDENT OF dropClaimCount, and that is the whole point of it
// having a name: a floor derived from the bound cannot audit the bound.
//
// IT DOES NOT REPLACE `seen != int(dropClaimCount)`, and an earlier draft of this
// comment said it did, on the reasoning that the clause is "tautologically false" and
// "can never fire". That is true only WHILE the loop bound IS dropClaimCount, and the
// conditionality is the tripwire, not a defect in it. Proven by mutation on this
// package: rewrite the walk to the pre-W10 shape
// `for c := dropClaimNone; c <= dropClaimThemeFont; c++` — verbatim the W10 defect,
// the one that left dropClaimThemeImage outside the census — and this floor of 3 stays
// green while the clause fires with "the census walked 5 of 6 claims". Deleting it
// therefore removed the only check that speaks to the exact bug class this file exists
// to prevent, and the aphorism above, though true, was being aimed at a clause that
// never audited the bound in the first place.
//
// THE THREE QUESTIONS ARE SEPARATE, and the split is exact:
//   - the SENTINEL is audited by the COMPILER, via the dropClaimNames array literal
//     (and, in the too-large direction, by the empty-name loop at the foot of the test);
//   - the WALK is audited by `seen != int(dropClaimCount)` — it asks whether the loop is
//     still bounded by the sentinel, and it fires the day someone reverts that bound to a
//     named member or hand-lists the roster, which is a realistic refactor;
//   - this FLOOR audits neither. It only refuses a roster too small to be worth walking —
//     an enum shrunk to a claim or two would satisfy both of the above and prove nothing.
const dropClaimCensusMin = 3

// TestEveryDropClaimHasASettingsAnswer keeps settingsDropAction TOTAL over
// dropClaim, so a claim added later cannot fall through to the theme-folder arm
// by omission — which is precisely how a dropped file came to repoint the user's
// theme root in the first place.
//
// It walks the enum by VALUE rather than listing the constants: a new claim
// appended to the iota block joins this gate the moment it exists.
//
// THE BOUND IS THE SENTINEL, not the last named member, and that correction is the
// whole reason this comment can be believed. The walk used to end at
// `c <= dropClaimThemeFont`, so W10's dropClaimThemeImage was appended to the block
// and landed OUTSIDE the census that promised to catch it — the gate went one short
// in exactly the wave that added the member. `c < dropClaimCount` cannot go short:
// the sentinel moves with the enum.
//
// WHAT GUARDS THE SENTINEL ITSELF, since the walk trusts it completely. Three
// mechanisms, and this comment names the one that actually fires in each case,
// because an earlier draft credited THIS TEST with all three and was wrong about two:
//
//   - Sentinel too SMALL — restated as a low literal, or moved above a live member:
//     the COMPILER, via dropClaimNames. That table is [dropClaimCount]string with one
//     entry per claim, so shrinking the bound overflows the literal; moving
//     dropClaimCount above dropClaimThemeImage yields `index 5 is out of bounds (>= 5)`.
//     NOTHING IN THIS TEST CATCHES THAT CASE and nothing here should claim to — a
//     census whose bound went short is exactly as quiet as the unguarded claim it
//     hides. Verified by mutation: with dropClaimNames decoupled to a slice so the
//     compile error could not mask the runtime question, that reorder leaves
//     dropClaimThemeImage unwalked and this test still PASSES.
//   - Sentinel too LARGE: the empty-name loop at the foot of this test. A short
//     fixed-size array literal is legal Go and leaves the extra slots "", so the
//     compiler is silent here and the loop is the entire guarantee.
//   - A claim added with no name: the same empty-name loop, for the same reason. It
//     is one deletion away from silence. Do not remove it.
//
// TWO MORE CLAUSES GUARD THE WALK, and they are not the same check twice:
// `seen != int(dropClaimCount)` asks whether the loop is still bounded by the sentinel
// (it fires the moment the bound reverts to a named member — the W10 shape), and
// dropClaimCensusMin asks whether the roster is big enough to be worth walking at all.
// Neither audits the sentinel; see dropClaimCensusMin's declaration for the split.
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
	a := &App{}
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
	a := &App{}
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
