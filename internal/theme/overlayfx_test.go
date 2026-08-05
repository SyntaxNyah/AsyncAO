package theme

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Fixtures are HAND-AUTHORED Go constants, not checked-in reference files.
// The two real corpora used to verify this parser (KFO's stock effects.ini and a
// third-party misc/ transitions pack) ship credits naming individual authors and
// third-party game audio with no licence grant — a redistribution question this
// repo does not need to answer. A hand-authored fixture is strictly better
// anyway: it carries the pathological cases a shipped file does not.

// fxV2Fixture exercises every key, every layer spelling (including the two canon
// defects), the case-sensitivity of the boolean parse, names and sound paths with
// spaces, sparse out-of-order indices, a non-numeric group, a nameless group, an
// empty-named group and a duplicate name.
const fxV2Fixture = `
[version]
major = 2

[7]
name = fade to black          ; a name with SPACES is real: transitions packs ship them
sound = misc sfx/switch tv sound
cull = true
layer = behind
loop = true
max_duration = 60
respect_flip = true
respect_offset = true
scaling = pixel
sticky = true
stretch = true

[2]
name = bare

[40]
name = casing
layer = character

[3]
name = shouty
layer = over

[4]
name = talky
layer = chat

[5]
name = underdog
layer = under                 ; DEFECT: "under" is documented but unimplemented -> chat

[6]
name = gloom
layer = beho                  ; DEFECT: truncation typo shipped by KFO and AO2 both -> chat

[8]
name = blanklayer
layer =

[9]
name = shouting
cull = TRUE
loop = True
stretch = true
respect_offset = TRUE

[10]
name = quiet
sound = 0

[notanumber]
name = ignored

[11]
sound = orphan

[12]
name =

[13]
name = bare
loop = true
`

const fxV1Fixture = `
realization = realizationsound
realization_stretch = false
realization_scaling = smooth
hearts = heartsfx
hearts_under_chatbox = false
realization_alt = altsound
zzlast = zzsfx
zzlast_ignore_offset = true
`

const fxV1WithVersion1 = `
[version]
major = 1

[0]
name = ignoredbecausev1
`

func parseFX(t *testing.T, src string) []OverlayFX {
	t.Helper()
	out, err := ParseOverlayFX(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseOverlayFX: %v", err)
	}
	return out
}

func fxByName(t *testing.T, file []OverlayFX, name string) OverlayFX {
	t.Helper()
	for _, fx := range file {
		if strings.EqualFold(fx.Name, name) {
			return fx
		}
	}
	t.Fatalf("effect %q not parsed (got %v)", name, OverlayFXNames(file))
	return OverlayFX{}
}

// TestParseOverlayFXAllKeys — the full-value group round-trips every field.
func TestParseOverlayFXAllKeys(t *testing.T) {
	fx := fxByName(t, parseFX(t, fxV2Fixture), "fade to black")
	if fx.Name != "fade to black" {
		t.Errorf("Name = %q, want the AUTHORED spelling with spaces", fx.Name)
	}
	if fx.Sound != "misc sfx/switch tv sound" {
		t.Errorf("Sound = %q — a nested sound path with spaces must survive verbatim", fx.Sound)
	}
	if !fx.Cull || !fx.Loop || !fx.RespectFlip || !fx.RespectOffset || !fx.Sticky || !fx.Stretch {
		t.Errorf("booleans not all true: %+v", fx)
	}
	if fx.Layer != OverlayLayerBehind {
		t.Errorf("Layer = %v, want behind", fx.Layer)
	}
	if fx.MaxFrameMS != 60 {
		t.Errorf("MaxFrameMS = %d, want 60", fx.MaxFrameMS)
	}
	if fx.Scaling != "pixel" {
		t.Errorf("Scaling = %q, want the RAW get_scaling token", fx.Scaling)
	}
}

// TestOverlayFXDefaultsMatchDoEffectCoercions — one row per key. A group with
// ONLY a name must coerce to do_effect's defaults (courtroom.cpp:3206-3216).
func TestOverlayFXDefaultsMatchDoEffectCoercions(t *testing.T) {
	fx := fxByName(t, parseFX(t, fxV2Fixture), "bare")
	for _, tc := range []struct {
		key  string
		got  any
		want any
	}{
		{"sound", fx.Sound, ""},
		{"cull", fx.Cull, false},
		{"loop", fx.Loop, false},
		{"stretch", fx.Stretch, false},
		{"respect_flip", fx.RespectFlip, false},
		{"respect_offset", fx.RespectOffset, false},
		{"sticky", fx.Sticky, false},
		{"scaling", fx.Scaling, ""},
		{"max_duration", fx.MaxFrameMS, 0},
		// The else branch of do_effect's layer switch (courtroom.cpp:3233) — an
		// ABSENT layer is chat, NOT behind. This is the one default that is easy
		// to get backwards.
		{"layer", fx.Layer, OverlayLayerChat},
	} {
		if tc.got != tc.want {
			t.Errorf("default for %q = %v, want %v (courtroom.cpp:3206-3237)", tc.key, tc.got, tc.want)
		}
	}
}

// TestOverlayFXBooleansAreCaseSensitiveLowercase — "TRUE"/"True" are FALSE in
// AO2 (startsWith("true") is case-sensitive, courtroom.cpp:3214/:3210).
func TestOverlayFXBooleansAreCaseSensitiveLowercase(t *testing.T) {
	fx := fxByName(t, parseFX(t, fxV2Fixture), "shouting")
	if fx.Cull {
		t.Error(`cull = TRUE must parse FALSE: startsWith("true") is case-sensitive`)
	}
	if fx.Loop {
		t.Error(`loop = True must parse FALSE`)
	}
	if !fx.Stretch {
		t.Error(`stretch = true must parse TRUE`)
	}
}

// TestRespectOffsetRequiresExactTrue — respect_offset is the ONE exact-equality
// test (courtroom.cpp:3249) against every other boolean's prefix test (:3206).
func TestRespectOffsetRequiresExactTrue(t *testing.T) {
	if fx := fxByName(t, parseFX(t, fxV2Fixture), "shouting"); fx.RespectOffset {
		t.Error(`respect_offset = TRUE must be FALSE — courtroom.cpp:3249 is == "true", not startsWith`)
	}
	if fx := fxByName(t, parseFX(t, fxV2Fixture), "fade to black"); !fx.RespectOffset {
		t.Error(`respect_offset = true must be TRUE`)
	}
	// No trailing-whitespace case here: it cannot reach coerce(). ParseINI stores
	// strings.TrimSpace(value) (ini.go:120), and canon does the same — AO2 reads
	// this through QSettings IniFormat (text_file_functions.cpp:869) whose
	// readIniLine strips trailing whitespace before the == "true" compare. A
	// `respect_offset = true ` line is TRUE in both clients.
	for _, raw := range []string{"truely", "TRUE", "True"} {
		src := "[version]\nmajor = 2\n[0]\nname = x\nrespect_offset = " + raw + "\n"
		if fx := fxByName(t, parseFX(t, src), "x"); fx.RespectOffset {
			t.Errorf("respect_offset = %q must be FALSE (exact match only)", raw)
		}
	}
}

// TestLayerUnderAndBehoFallToChat — the two canon defects, reproduced not fixed.
func TestLayerUnderAndBehoFallToChat(t *testing.T) {
	file := parseFX(t, fxV2Fixture)
	for _, name := range []string{"underdog", "gloom", "blanklayer"} {
		if got := fxByName(t, file, name).Layer; got != OverlayLayerChat {
			t.Errorf("%s layer = %v, want chat. "+
				`"under" is documented in the shipped header but NOT implemented, and "beho" is a `+
				`truncation typo present in AO2's own bin/base/themes/default/effects/effects.ini:151. `+
				"Both fall to do_effect's else branch (courtroom.cpp:3233). Do not normalise them.", name, got)
		}
	}
	for _, tc := range []struct {
		name string
		want OverlayLayer
	}{
		{"casing", OverlayLayerCharacter},
		{"shouty", OverlayLayerOver},
		{"talky", OverlayLayerChat},
		{"fade to black", OverlayLayerBehind},
	} {
		if got := fxByName(t, file, tc.name).Layer; got != tc.want {
			t.Errorf("%s layer = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestMaxDurationIsPerFrameNotTotal — the parse keeps the value in the PER-FRAME
// unit (animationlayer.cpp:281-286 clamps each frame's delay), so a 3-frame clip
// authored at 200 ms/frame with max_duration = 60 runs 3x60 ms, not one 60 ms
// clip. The clamp arithmetic itself lives render-side; this pins the contract.
func TestMaxDurationIsPerFrameNotTotal(t *testing.T) {
	fx := fxByName(t, parseFX(t, fxV2Fixture), "fade to black")
	const frames, authored = 3, 200
	total := 0
	for i := 0; i < frames; i++ {
		d := authored
		if fx.MaxFrameMS > 0 && d > fx.MaxFrameMS {
			d = fx.MaxFrameMS
		}
		total += d
	}
	if want := frames * fx.MaxFrameMS; total != want {
		t.Errorf("clamped total = %d ms, want %d ms (per-FRAME clamp, animationlayer.cpp:281-286)", total, want)
	}
	if total == fx.MaxFrameMS {
		t.Error("max_duration was applied to the whole clip — that is the shipped comment, not the code")
	}
}

// TestOverlayFXOrderIsNumericNotLexical — [2] before [7] before [10] before
// [40]. Qt's childGroups() is alphabetical ("0","1","10",...,"2") and AO2 sorts
// it numerically for the list (text_file_functions.cpp:812); the property walk
// does NOT sort, which only diverges for a file carrying duplicate names.
// Numeric order is the defensible choice and is what this parser uses for both.
func TestOverlayFXOrderIsNumericNotLexical(t *testing.T) {
	got := OverlayFXNames(parseFX(t, fxV2Fixture))
	want := []string{"bare", "shouty", "talky", "underdog", "gloom", "fade to black", "blanklayer", "shouting", "quiet", "bare", "casing"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v (numeric group order)", got, want)
		}
	}
}

// TestOverlayFXSkipsNonNumericAndNamelessGroups (text_file_functions.cpp:814-829).
func TestOverlayFXSkipsNonNumericAndNamelessGroups(t *testing.T) {
	for _, name := range OverlayFXNames(parseFX(t, fxV2Fixture)) {
		if name == "ignored" {
			t.Error("[notanumber] must be skipped: the group name has to parse as an int")
		}
		if name == "" {
			t.Error("a group with an empty name must be skipped (:823-826)")
		}
	}
	// [11] has a sound but no name, [12] has an empty name: neither may appear.
	if n := len(parseFX(t, fxV2Fixture)); n != 11 {
		t.Errorf("parsed %d effects, want 11 (13 groups minus the nameless, the empty-named and the non-numeric)", n)
	}
}

// TestOverlayFXIndexScanIsCapped — the charEmoteScanCap idiom: a group past the
// scan bound is not found, and the scan terminates.
func TestOverlayFXIndexScanIsCapped(t *testing.T) {
	src := "[version]\nmajor = 2\n[9999]\nname = faraway\n[0]\nname = near\n"
	names := OverlayFXNames(parseFX(t, src))
	if len(names) != 1 || names[0] != "near" {
		t.Errorf("names = %v, want just [near] — index 9999 is past OverlayFXIndexScanCap=%d", names, OverlayFXIndexScanCap)
	}
}

// TestOverlayFXCapBoundsTheList — a pathological file cannot grow the roster.
func TestOverlayFXCapBoundsTheList(t *testing.T) {
	var b strings.Builder
	b.WriteString("[version]\nmajor = 2\n")
	for i := 0; i < OverlayFXIndexScanCap; i++ {
		b.WriteString("[" + strconv.Itoa(i) + "]\nname = fx" + strconv.Itoa(i) + "\n")
	}
	if n := len(parseFX(t, b.String())); n != OverlayFXCap {
		t.Errorf("parsed %d effects, want the cap %d", n, OverlayFXCap)
	}
}

// TestOverlayFXNameLengthIsBounded — the name becomes a URL segment and a T1 key.
func TestOverlayFXNameLengthIsBounded(t *testing.T) {
	src := "[version]\nmajor = 2\n[0]\nname = " + strings.Repeat("x", OverlayFXNameLen+1) + "\n[1]\nname = ok\n"
	names := OverlayFXNames(parseFX(t, src))
	if len(names) != 1 || names[0] != "ok" {
		t.Errorf("names = %v, want just [ok]: an over-long name is refused, never truncated", names)
	}
}

// TestPropertyMergeIsLastFileWins — the merge asymmetry
// (text_file_functions.cpp:864-888 never breaks the outer file loop).
func TestPropertyMergeIsLastFileWins(t *testing.T) {
	themeFile := parseFX(t, "[version]\nmajor = 2\n[0]\nname = flash\nsound = themesound\nlayer = behind\n")
	miscFile := parseFX(t, "[version]\nmajor = 2\n[0]\nname = flash\nsound = miscsound\nlayer = over\n")
	set := MergeOverlayFX([]string{"flash"}, themeFile, miscFile)
	fx, ok := set.Get("FLASH") // case-insensitive lookup (:877)
	if !ok {
		t.Fatal("merged set lost the effect")
	}
	if fx.Sound != "miscsound" || fx.Layer != OverlayLayerOver {
		t.Errorf("got sound=%q layer=%v; the LAST file in the ladder wins (this is the OPPOSITE of art resolution and of loadFirstINI)", fx.Sound, fx.Layer)
	}
}

// TestLaterFileWithMissingKeyClobbersToDefault — the :879-before-:880 bug: the
// assignment precedes the emptiness test, so a later file that HAS the group but
// OMITS the property resets it to "" (and thus to the coerced default).
func TestLaterFileWithMissingKeyClobbersToDefault(t *testing.T) {
	themeFile := parseFX(t, "[version]\nmajor = 2\n[0]\nname = flash\nsound = themesound\nloop = true\n")
	miscFile := parseFX(t, "[version]\nmajor = 2\n[0]\nname = flash\nlayer = behind\n")
	fx, _ := MergeOverlayFX([]string{"flash"}, themeFile, miscFile).Get("flash")
	if fx.Sound != "" {
		t.Errorf("Sound = %q, want \"\" — a later file with the group but no `sound` clobbers it (text_file_functions.cpp:879 assigns before :880 tests)", fx.Sound)
	}
	if fx.Loop {
		t.Error("Loop survived the clobber; the same rule applies to every property")
	}
	if fx.Layer != OverlayLayerBehind {
		t.Errorf("Layer = %v, want behind (the later file's own value)", fx.Layer)
	}
}

// TestMergeSkipsFilesWithoutTheGroup — a file that never mentions the effect
// leaves the previous file's properties alone (the outer loop only assigns
// inside the name match).
func TestMergeSkipsFilesWithoutTheGroup(t *testing.T) {
	themeFile := parseFX(t, "[version]\nmajor = 2\n[0]\nname = flash\nsound = themesound\n")
	otherFile := parseFX(t, "[version]\nmajor = 2\n[0]\nname = somethingelse\nsound = nope\n")
	fx, _ := MergeOverlayFX([]string{"flash"}, themeFile, otherFile).Get("flash")
	if fx.Sound != "themesound" {
		t.Errorf("Sound = %q, want themesound", fx.Sound)
	}
}

// TestDuplicateNameInOneFileTakesTheFirstNonEmpty — the INNER loop breaks only
// on a non-empty value (text_file_functions.cpp:880-885), so a later duplicate
// group only fills what the earlier one left blank.
func TestDuplicateNameInOneFileTakesTheFirstNonEmpty(t *testing.T) {
	file := parseFX(t, fxV2Fixture) // [2] name=bare (nothing), [13] name=bare loop=true
	fx, ok := MergeOverlayFX(nil, file).Get("bare")
	if !ok {
		t.Fatal("merged set lost the duplicate-named effect")
	}
	if !fx.Loop {
		t.Error("the duplicate group's loop=true should fill the first group's empty slot")
	}
}

// TestListIsThemeThenMiscConcatenatedWithDuplicates — get_effects parity
// (text_file_functions.cpp:770-831): no dedupe, no re-sort, theme first.
func TestListIsThemeThenMiscConcatenatedWithDuplicates(t *testing.T) {
	themeNames := OverlayFXNames(parseFX(t, "[version]\nmajor = 2\n[0]\nname = flash\n[1]\nname = realization\n"))
	miscNames := OverlayFXNames(parseFX(t, "[version]\nmajor = 2\n[0]\nname = realization\n[1]\nname = tv on\n"))
	set := MergeOverlayFX(append(append([]string{}, themeNames...), miscNames...))
	want := []string{"flash", "realization", "realization", "tv on"}
	got := set.Names()
	if len(got) != len(want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order = %v, want %v (theme first, duplicates PRESERVED — the wire carries the NAME, not the index)", got, want)
		}
	}
}

// TestOverlayFXListIsBounded.
func TestOverlayFXListIsBounded(t *testing.T) {
	long := make([]string, OverlayFXListCap+50)
	for i := range long {
		long[i] = "fx" + strconv.Itoa(i)
	}
	if n := len(MergeOverlayFX(long).Order); n != OverlayFXListCap {
		t.Errorf("Order length %d, want the cap %d", n, OverlayFXListCap)
	}
}

// TestOverlayFXFileCapKeepsTheLastFiles — the property pass is LAST-WINS
// (text_file_functions.cpp:860-888 never breaks the outer file loop), so the
// files at the END of the ladder are the ones whose values survive: the
// character's own misc/<folder>/effects.ini, then the origin manifest. Truncating
// from the front of the list — the obvious `i >= cap: break` — would keep exactly
// the files that every later one overrides and drop the ones that win, silently
// inverting the ladder for any character whose ladder is deeper than the cap.
func TestOverlayFXFileCapKeepsTheLastFiles(t *testing.T) {
	const over = 4
	files := make([][]OverlayFX, 0, OverlayFXFileCap+over)
	for i := 0; i < OverlayFXFileCap+over; i++ {
		files = append(files, []OverlayFX{{Name: "realization", Sound: "sound" + strconv.Itoa(i)}})
	}
	set := MergeOverlayFX([]string{"realization"}, files...)
	fx, ok := set.Get("realization")
	if !ok {
		t.Fatal("the merged set lost the effect entirely")
	}
	last := "sound" + strconv.Itoa(OverlayFXFileCap+over-1)
	if fx.Sound != last {
		t.Errorf("Sound = %q, want %q — the cap must drop the FRONT of the ladder, "+
			"where last-wins means the least specific files live", fx.Sound, last)
	}
	// Exactly at the cap, nothing is dropped at all.
	exact := files[:OverlayFXFileCap]
	if fx, _ := MergeOverlayFX([]string{"realization"}, exact...).Get("realization"); fx.Sound != "sound"+strconv.Itoa(OverlayFXFileCap-1) {
		t.Errorf("a ladder exactly at the cap merged to %q", fx.Sound)
	}
}

// TestLegacyV1MigratesInMemory — aoutils.cpp:10-88.
func TestLegacyV1MigratesInMemory(t *testing.T) {
	file := parseFX(t, fxV1Fixture)
	names := OverlayFXNames(file)
	// Alphabetical, because AO2 iterates a QMap. realization_scaling and
	// realization_stretch are PROPERTIES (they match the regex at :49);
	// realization_alt is an EFFECT (its suffix is not a property) and so is
	// zzlast, whose _ignore_offset IS a property.
	want := []string{"hearts", "realization", "realization_alt", "zzlast"}
	if len(names) != len(want) {
		t.Fatalf("migrated names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("migrated names = %v, want %v (alphabetical; the authored order is lost upstream too)", names, want)
		}
	}
	real := fxByName(t, file, "realization")
	if real.Sound != "realizationsound" {
		t.Errorf("realization sound = %q", real.Sound)
	}
	if !real.Cull {
		t.Error("every migrated effect gets cull=true (aoutils.cpp:65)")
	}
	if real.Layer != OverlayLayerChat {
		t.Errorf("realization layer = %v, want chat (the one special case, aoutils.cpp:71)", real.Layer)
	}
	if real.Stretch {
		t.Error("realization_stretch = false must OVERWRITE the migration's stretch=true (:74-82 runs after :70)")
	}
	if real.Scaling != "smooth" {
		t.Errorf("realization scaling = %q, want smooth", real.Scaling)
	}
	hearts := fxByName(t, file, "hearts")
	if hearts.Layer != OverlayLayerCharacter {
		t.Errorf("hearts layer = %v — under_chatbox maps to the FIXED pair (\"layer\",\"character\") "+
			"REGARDLESS of its value (aoutils.cpp:38-40,:80-81), so even `= false` yields character", hearts.Layer)
	}
	if zz := fxByName(t, file, "zzlast"); zz.RespectOffset {
		t.Error("ignore_offset is DEAD after migration: v2 reads respect_offset")
	}
}

// TestV1IsTriggeredByAMissingOrLowVersion (text_file_functions.cpp:788-799).
func TestV1IsTriggeredByAMissingOrLowVersion(t *testing.T) {
	names := OverlayFXNames(parseFX(t, fxV1WithVersion1))
	for _, n := range names {
		if n == "ignoredbecausev1" {
			t.Error("[version] major = 1 must take the v1 path: the [0] group is not read")
		}
	}
}

// TestV1MigrationNeverWritesToDisk — AsyncAO deliberately diverges: canon copies
// the file to <file>.old and rewrites it in place (aborting if the copy fails).
// A streaming client has no business rewriting a user's theme folder.
func TestV1MigrationNeverWritesToDisk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, overlayFXFileName)
	if err := os.WriteFile(p, []byte(fxV1Fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOverlayFXFile(p); err != nil {
		t.Fatalf("readOverlayFXFile: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the v1 file was rewritten — migration is IN MEMORY only")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("directory now holds %d entries — no .old file may ever be written", len(ents))
	}
}

// TestSoundZeroSurvivesTheParse — "0" is the community "no sound" idiom, not a
// coded sentinel in AO2 (the file 0.* simply never resolves, aosfxplayer.cpp:63-70).
// It must survive the parse; filtering happens at the play site.
func TestSoundZeroSurvivesTheParse(t *testing.T) {
	if fx := fxByName(t, parseFX(t, fxV2Fixture), "quiet"); fx.Sound != "0" {
		t.Errorf("sound = %q, want \"0\" verbatim", fx.Sound)
	}
}

// TestOverlayFXInlineCommentIsStripped — the shared INI reader truncates at the
// first ';' (Qt parity, ini.go's iniCommentStart).
func TestOverlayFXInlineCommentIsStripped(t *testing.T) {
	if fx := fxByName(t, parseFX(t, fxV2Fixture), "casing"); fx.Layer != OverlayLayerCharacter {
		t.Errorf("layer = %v; an inline ';' comment must not leak into the value", fx.Layer)
	}
}

func FuzzParseOverlayFX(f *testing.F) {
	f.Add(fxV2Fixture)
	f.Add(fxV1Fixture)
	f.Add("[version]\nmajor = 2\n[0]\nname=\xff\xfe\n")
	f.Fuzz(func(t *testing.T, src string) {
		out, err := ParseOverlayFX(strings.NewReader(src))
		if err != nil {
			return
		}
		if len(out) > OverlayFXCap {
			t.Fatalf("parsed %d effects, past the cap %d", len(out), OverlayFXCap)
		}
		for _, fx := range out {
			if fx.Name == "" || len(fx.Name) > OverlayFXNameLen {
				t.Fatalf("effect name %q escaped the bounds", fx.Name)
			}
		}
		MergeOverlayFX(OverlayFXNames(out), out)
	})
}

// --- ladder tests ------------------------------------------------------------

// overlayLadderRung is one rung of the §1.f probe table, relative to base/.
type overlayLadderRung struct {
	n   int
	rel string // "%T" = active theme, "%S" = subtheme, "%M" = misc folder
}

// overlayLadderTable is AO2's get_effect ladder verified against
// path_functions.cpp:175-216 + text_file_functions.cpp:841-849, with NO subtheme
// set: rungs 1, 5 and 7 are guarded by `p_subtheme != ""` upstream and so do not
// exist here at all. overlaySubthemeLadderTable is the full eleven.
var overlayLadderTable = []overlayLadderRung{
	{2, "themes/%T/effects"},
	{3, "themes/default/effects"},
	{4, "effects"},
	{6, "themes/%T/misc/%M"},
	{8, "misc/%M"},
	{9, "themes/%T"},
	{10, "themes/default"},
	{11, ""},
}

// overlaySubthemeLadderTable is the SAME ladder with a subtheme active — all
// eleven rungs, in get_asset_paths order (path_functions.cpp:181-205). Note where
// canon puts them: the subtheme MISC dir is the very first probe (tier 2), and the
// bare subtheme dir (rung 7) comes BEFORE the base misc dir (rung 8), not after.
var overlaySubthemeLadderTable = []overlayLadderRung{
	{1, "themes/%T/%S/effects"},
	{2, "themes/%T/effects"},
	{3, "themes/default/effects"},
	{4, "effects"},
	{5, "themes/%T/%S/misc/%M"},
	{6, "themes/%T/misc/%M"},
	{7, "themes/%T/%S"},
	{8, "misc/%M"},
	{9, "themes/%T"},
	{10, "themes/default"},
	{11, ""},
}

// runOverlayLadder plants a file at every rung of the table, then removes them one
// at a time: the resolved path must walk the table in order and nothing may
// resolve once the last one is gone.
func runOverlayLadder(t *testing.T, table []overlayLadderRung, themeName, sub, miscName, effect string) {
	t.Helper()
	root := t.TempDir()
	rep := strings.NewReplacer("%T", themeName, "%S", sub, "%M", miscName)
	paths := make([]string, 0, len(table))
	for _, rung := range table {
		dir := filepath.Join(root, filepath.FromSlash(rep.Replace(rung.rel)))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, effect+".png")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	th, err := Load(themeName, sub, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range paths {
		got, ok := th.FindOverlayElement(miscName, effect)
		if !ok {
			t.Fatalf("rung %d: nothing resolved, want %s", table[i].n, want)
		}
		if got != want {
			t.Fatalf("rung %d: resolved %s, want %s (path_functions.cpp:175-216)", table[i].n, got, want)
		}
		if err := os.Remove(want); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := th.FindOverlayElement(miscName, effect); ok {
		t.Error("every rung removed but something still resolved")
	}
}

// TestOverlayArtProbeOrderMatchesAO2 — the eight rungs a themeless-subtheme
// install has.
func TestOverlayArtProbeOrderMatchesAO2(t *testing.T) {
	runOverlayLadder(t, overlayLadderTable, "mytheme", "", "transitions", "fadeout")
}

// TestOverlayArtProbeOrderWithSubtheme — the same walk with a subtheme set, which
// is the shape that caught the activeDirs/defaultDirs base-name filter: the
// subtheme dir's BASE is the SUBTHEME name, so a base-only test filed every
// subtheme directory under themes/default and deleted rungs 1, 5 and 7.
func TestOverlayArtProbeOrderWithSubtheme(t *testing.T) {
	runOverlayLadder(t, overlaySubthemeLadderTable, "mytheme", "night", "transitions", "fadeout")
}

// TestOverlayExtensionLadderIsWebPApngGifPng (text_file_functions.cpp:381-404).
func TestOverlayExtensionLadderIsWebPApngGifPng(t *testing.T) {
	const themeName = "t"
	root := t.TempDir()
	dir := filepath.Join(root, "themes", themeName, overlayFXDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	want := []string{".webp", ".apng", ".gif", ".png"}
	for _, ext := range want {
		if err := os.WriteFile(filepath.Join(dir, "fx"+ext), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load(themeName, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range want {
		got, ok := th.FindOverlayArt("fx")
		if !ok {
			t.Fatalf("nothing resolved, want %s", ext)
		}
		if filepath.Ext(got) != ext {
			t.Fatalf("resolved %s, want the %s rung", got, ext)
		}
		if err := os.Remove(got); err != nil {
			t.Fatal(err)
		}
	}
	if got := OverlayImageExts(); len(got) != 4 || got[0] != ".webp" || got[3] != ".png" {
		t.Errorf("OverlayImageExts = %v, want .webp .apng .gif .png", got)
	}
}

// TestOverlayMiscRungsUseFindAssetMisc — structural: rungs 5-6 must go through
// the ONE shared misc resolver (the theme editor's W8 seam), never a private
// second implementation.
func TestOverlayMiscRungsUseFindAssetMisc(t *testing.T) {
	const themeName, miscName = "t", "pack"
	root := t.TempDir()
	dir := filepath.Join(root, "themes", themeName, overlayMiscDir, miscName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fx.webp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	th, err := Load(themeName, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	viaShared, ok := th.FindAssetMisc(miscName, "fx", OverlayImageExts())
	if !ok {
		t.Fatal("FindAssetMisc missed the theme-local misc rung (path_functions.cpp:181-189)")
	}
	viaOverlay, ok := th.FindOverlayMisc(miscName, "fx")
	if !ok || viaOverlay != viaShared {
		t.Errorf("FindOverlayMisc = %q, FindAssetMisc = %q — rungs 5-6 must ride the shared helper", viaOverlay, viaShared)
	}
	if _, ok := th.FindOverlayElement(miscName, "fx"); !ok {
		t.Error("the full ladder must include the misc rungs")
	}
}

// TestFindAssetMiscProbeOrderMatchesAO2 — the W8 gate, kept here because this
// wave lands the helper. SUBTHEME misc before theme misc before nothing: the
// default theme has NO misc rung at all (path_functions.cpp:181-189 passes
// p_theme and p_theme+"/"+p_subtheme, never p_default_theme).
func TestFindAssetMiscProbeOrderMatchesAO2(t *testing.T) {
	const themeName, subName, miscName = "mytheme", "night", "yttd"
	root := t.TempDir()
	sub := filepath.Join(root, "themes", themeName, subName, overlayMiscDir, miscName)
	active := filepath.Join(root, "themes", themeName, overlayMiscDir, miscName)
	fallback := filepath.Join(root, "themes", "default", overlayMiscDir, miscName)
	for _, d := range []string{sub, active, fallback} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "chat.png"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load(themeName, subName, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	// Rung 2: the SUBTHEME's own misc folder wins outright.
	got, ok := th.FindAssetMisc(miscName, "chat", []string{".png"})
	if !ok || got != filepath.Join(sub, "chat.png") {
		t.Fatalf("resolved %q, want the SUBTHEME misc dir %q (get_asset_paths tier 2)", got, sub)
	}
	if err := os.Remove(filepath.Join(sub, "chat.png")); err != nil {
		t.Fatal(err)
	}
	// Rung 3: the theme's own misc folder.
	if got, ok := th.FindAssetMisc(miscName, "chat", []string{".png"}); !ok || got != filepath.Join(active, "chat.png") {
		t.Fatalf("resolved %q, want the ACTIVE theme's misc dir", got)
	}
	if err := os.Remove(filepath.Join(active, "chat.png")); err != nil {
		t.Fatal(err)
	}
	if got, ok := th.FindAssetMisc(miscName, "chat", []string{".png"}); ok {
		t.Errorf("resolved %q — themes/default has no misc rung in get_asset_paths", got)
	}
	// And with no subtheme set the rung disappears rather than resolving into
	// somebody else's folder.
	plain, err := Load(themeName, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "chat.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := plain.FindAssetMisc(miscName, "chat", []string{".png"}); ok {
		t.Errorf("resolved %q with no subtheme set — that rung is guarded by p_subtheme != \"\"", got)
	}
}

// TestFindAssetMiscRefusesTraversal — the misc folder is server-supplied
// char.ini text; "../.." must be a refusal, not a read.
func TestFindAssetMiscRefusesTraversal(t *testing.T) {
	root := t.TempDir()
	secretDir := filepath.Join(root, "secret")
	if err := os.MkdirAll(secretDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "chat.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	th, err := Load("t", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := th.FindAssetMisc("../../secret", "chat", []string{".png"}); ok {
		t.Errorf("traversal resolved %q — safepath must refuse it", got)
	}
	if got, ok := th.FindOverlayElement("", "../../secret/chat"); ok {
		t.Errorf("a traversing EFFECT NAME resolved %q — it arrives on the wire", got)
	}
}

// TestOverlayIconLadderPrefixesIcons — courtroom.cpp:5608 asks for the element
// "icons/<name>", so the icon rides the same ladder one folder down.
func TestOverlayIconLadderPrefixesIcons(t *testing.T) {
	const themeName, miscName = "t", "pack"
	root := t.TempDir()
	themeIcons := filepath.Join(root, "themes", themeName, overlayFXDirName, overlayFXIconDir)
	miscIcons := filepath.Join(root, "themes", themeName, overlayMiscDir, miscName, overlayFXIconDir)
	for _, d := range []string{themeIcons, miscIcons} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "fx.png"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load(themeName, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := th.FindOverlayIcon("fx")
	if !ok || got != filepath.Join(themeIcons, "fx.png") {
		t.Fatalf("theme icon resolved %q, want %q", got, filepath.Join(themeIcons, "fx.png"))
	}
	if err := os.Remove(filepath.Join(themeIcons, "fx.png")); err != nil {
		t.Fatal(err)
	}
	got, ok = th.FindOverlayElementIcon(miscName, "fx")
	if !ok || got != filepath.Join(miscIcons, "fx.png") {
		t.Fatalf("misc icon resolved %q, want %q", got, filepath.Join(miscIcons, "fx.png"))
	}
}

// TestOverlayFXPathsWalkBothTiers — the LIST pass reads the first file of each
// tier, the PROPERTY pass every file of both.
func TestOverlayFXPathsWalkBothTiers(t *testing.T) {
	const themeName, miscName = "t", "pack"
	root := t.TempDir()
	write := func(rel, body string) string {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, overlayFXFileName)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	themeIni := write("themes/"+themeName+"/effects", "[version]\nmajor = 2\n[0]\nname = flash\nsound = themesound\n")
	defaultIni := write("themes/default/effects", "[version]\nmajor = 2\n[0]\nname = flash\nsound = defaultsound\n")
	miscIni := write(overlayMiscDir+"/"+miscName, "[version]\nmajor = 2\n[0]\nname = tv on\n[1]\nname = flash\n")
	th, err := Load(themeName, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	themeTier, miscTier := th.OverlayFXPaths(miscName)
	if len(themeTier) != 2 || themeTier[0] != themeIni || themeTier[1] != defaultIni {
		t.Fatalf("theme tier = %v, want [%s %s]", themeTier, themeIni, defaultIni)
	}
	if len(miscTier) != 1 || miscTier[0] != miscIni {
		t.Fatalf("misc tier = %v, want [%s]", miscTier, miscIni)
	}
	themeFirst, themeAll := th.OverlayFXFiles()
	miscFirst, miscAll := th.OverlayFXMiscFiles(miscName)
	if len(themeAll) != 2 || len(miscAll) != 1 {
		t.Fatalf("property pass sizes = %d/%d, want 2/1", len(themeAll), len(miscAll))
	}
	list := append(OverlayFXNames(themeFirst), OverlayFXNames(miscFirst)...)
	set := MergeOverlayFX(list, append(themeAll, miscAll...)...)
	if got := set.Names(); len(got) != 3 || got[0] != "flash" || got[1] != "tv on" || got[2] != "flash" {
		t.Errorf("display list = %v, want [flash, tv on, flash]", got)
	}
	fx, _ := set.Get("flash")
	if fx.Sound != "" {
		t.Errorf("flash sound = %q — the LAST file (the misc one, which omits `sound`) clobbers it", fx.Sound)
	}
}
