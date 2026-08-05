package theme

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// themesFixtureDir holds the hand-authored theme fixtures. They were written
// AGAINST docs/THEME-FORMAT.md BY HAND, never exported by our own writer:
// hand-writing the format is what exposes its mistakes while they are still
// cheap, and a fixture the writer produced would only prove the writer agrees
// with itself.
const themesFixtureDir = "testdata/themes"

func readThemeFixture(t *testing.T, theme, file string) []byte {
	t.Helper()
	p := filepath.Join(themesFixtureDir, theme, file)
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("theme fixture %s is unreadable (%v). It is checked in at %s; "+
			"if it moved, repoint the constant rather than skipping the gate.", file, err, p)
	}
	return src
}

// ---------------------------------------------------------------------------
// Wire names are the format
// ---------------------------------------------------------------------------

// TestElementKindNamesAreAppendOnly freezes the kind table. The NAMES are the
// format: a value inserted in the middle silently re-maps every element in
// every theme already on disk, and a renamed one orphans them. New kinds are
// appended AT THE END, and this test's literal grows by exactly that much.
func TestElementKindNamesAreAppendOnly(t *testing.T) {
	frozen := []string{"image", "shape", "gradient", "gen", "text", "pane"}
	assertAppendOnly(t, "ElementKind", frozen, elementKindNames[:])
	if int(ElementKindCount) != len(elementKindNames) {
		t.Errorf("ElementKindCount = %d but the name table holds %d", ElementKindCount, len(elementKindNames))
	}
	// The enum's own values must still line up with their names, or the table
	// would be append-only about the wrong thing.
	for i, want := range []struct {
		k    ElementKind
		name string
	}{
		{ElemImage, "image"}, {ElemShape, "shape"}, {ElemGradient, "gradient"},
		{ElemGen, "gen"}, {ElemText, "text"}, {ElemPane, "pane"},
	} {
		if int(want.k) != i {
			t.Errorf("%s moved to position %d", want.name, want.k)
		}
		if got := want.k.String(); got != want.name {
			t.Errorf("kind %d String() = %q, want %q", i, got, want.name)
		}
	}
}

// TestElementBandNamesAreAppendOnly freezes the band table. Bands are worse
// than kinds to reorder: a band IS a position in the client's hand-ordered draw
// pass, so inserting one between two existing bands re-orders the paint of
// every shipped theme at once.
func TestElementBandNamesAreAppendOnly(t *testing.T) {
	frozen := []string{"backdrop", "mid", "overlay"}
	assertAppendOnly(t, "ElementBand", frozen, elementBandNames[:])
	if int(ElementBandCount) != len(elementBandNames) {
		t.Errorf("ElementBandCount = %d but the name table holds %d", ElementBandCount, len(elementBandNames))
	}
	if BandBackdrop != 0 || BandMid != 1 || BandOverlay != 2 {
		t.Errorf("band order changed: backdrop=%d mid=%d overlay=%d", BandBackdrop, BandMid, BandOverlay)
	}
}

// TestTheOtherWireNameTablesAreAppendOnly holds the same line for every
// remaining enum in the format. They are cheaper to get wrong than kinds and
// bands — nothing about a space or a fit LOOKS load-bearing — and exactly as
// destructive: the index is what themes on disk resolve through.
func TestTheOtherWireNameTablesAreAppendOnly(t *testing.T) {
	assertAppendOnly(t, "Space", []string{
		"courtroom", "chatbox", "viewport", "music_display", "window", "pane",
	}, spaceNames[:])
	assertAppendOnly(t, "ElementFit", []string{
		"stretch", "tile", "contain", "cover", "nine",
	}, fitNames[:])
	assertAppendOnly(t, "EffectKind", []string{
		"none", "fade", "pulse", "breathe", "glow", "drift", "spin", "shake", "slam",
	}, effectNames[:])
	assertAppendOnly(t, "TextAlign", []string{"left", "center", "right"}, alignNames[:])
	assertAppendOnly(t, "ConditionAxis", []string{
		"always", "pos", "char", "side", "speaking", "shout",
	}, conditionAxisNames[:])
	assertAppendOnly(t, "palette roles", []string{
		"text", "panel", "panel_hi", "accent", "danger",
	}, func() []string {
		out := make([]string, 0, len(paletteRoleKeys))
		for _, r := range paletteRoleKeys {
			out = append(out, r.key)
		}
		return out
	}())
}

// assertAppendOnly checks that live starts with frozen, entry for entry.
func assertAppendOnly(t *testing.T, what string, frozen, live []string) {
	t.Helper()
	if len(live) < len(frozen) {
		t.Fatalf("%s: %d wire names, was %d — a name was REMOVED, which orphans every theme using it",
			what, len(live), len(frozen))
	}
	for i, want := range frozen {
		if live[i] != want {
			t.Fatalf("%s[%d] = %q, want %q — wire names are APPEND-ONLY, at the END",
				what, i, live[i], want)
		}
	}
	// A new name must be unique, or two spellings would resolve to one value.
	seen := map[string]int{}
	for i, n := range live {
		if n == "" {
			t.Errorf("%s[%d] has no wire name", what, i)
		}
		if prev, dup := seen[n]; dup {
			t.Errorf("%s: %q is used by both %d and %d", what, n, prev, i)
		}
		seen[n] = i
	}
}

// ---------------------------------------------------------------------------
// Preservation
// ---------------------------------------------------------------------------

// TestUnknownKeysAndSectionsSurviveARoundTrip is forward compatibility's whole
// mechanism (§7.3): an OLDER build saving a NEWER build's theme must not delete
// what it could not read. The edit is real, and exactly one line moves.
func TestUnknownKeysAndSectionsSurviveARoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"; a comment the author wrote",
		"[theme]",
		"format = 1",
		"name = Keeper",
		"mood_engine = volumetric      ; a key this build does not define",
		"",
		"[element.plate]",
		"kind = gradient",
		"z = 4",
		"refraction = 0.8",
		"",
		"[shader.bloom]",
		"strength = 60",
		"",
		"[x.somebodyelse.tool]",
		"whatever = 42",
		"",
	}, "\n")

	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// An untouched save is a no-op, byte for byte.
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Fatalf("an untouched save rewrote the file:\n%s", out)
	}

	// Now a REAL edit, of the kind the editor makes.
	e, ok := s.FindElement("plate")
	if !ok {
		t.Fatal("the element did not parse")
	}
	e.Z = 9
	out, err = s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	inLines, outLines := strings.Split(src, "\n"), strings.Split(string(out), "\n")
	if len(inLines) != len(outLines) {
		t.Fatalf("line count changed %d -> %d:\n%s", len(inLines), len(outLines), out)
	}
	changed := 0
	for i := range inLines {
		if inLines[i] != outLines[i] {
			changed++
			if outLines[i] != "z = 9" {
				t.Errorf("line %d = %q, want %q", i, outLines[i], "z = 9")
			}
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want exactly 1:\n%s", changed, out)
	}

	// And the unknowns are ENUMERATED, so the editor can say what it is holding.
	want := []string{
		"theme/mood_engine",
		"element.plate/refraction",
		"shader.bloom/",
		"shader.bloom/strength",
		"x.somebodyelse.tool/",
		"x.somebodyelse.tool/whatever",
	}
	got := s.Unknown()
	for _, w := range want {
		if !containsString(got, w) {
			t.Errorf("Unknown() = %v, missing %q", got, w)
		}
	}
}

// TestUnknownEnumValuesDegradeAndPreserve pins §7.4, whose second half is the
// one that is easy to lose: degrading is not enough, the ORIGINAL TEXT must
// still be in the file afterwards. Otherwise the first save by an older build
// quietly rewrites a newer build's element into a duller one.
func TestUnknownEnumValuesDegradeAndPreserve(t *testing.T) {
	src := strings.Join([]string{
		"[theme]",
		"format = 1",
		"",
		"[element.plate]",
		"kind = gradient",
		"space = holodeck",
		"fit = quantum",
		"effect = shimmer",
		"align = justify",
		"visible_when = weather:rain",
		"",
		"[element.hologram]",
		"kind = hologram",
		"z = 30",
		"",
	}, "\n")

	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s.FindElement("plate")
	if !ok {
		t.Fatal("an element with unknown enum VALUES must still parse")
	}
	for _, c := range []struct {
		what string
		got  string
		want string
	}{
		{"space", e.Space.String(), "courtroom"},
		{"fit", e.Fit.String(), "stretch"},
		{"effect", e.Effect.String(), "none"},
		{"align", e.Align.String(), "left"},
		{"visible_when", e.VisibleWhen.Axis.String(), "always"},
	} {
		if c.got != c.want {
			t.Errorf("%s degraded to %q, want the safe primitive %q", c.what, c.got, c.want)
		}
	}
	// An unknown KIND is different: we cannot draw what we cannot name, so the
	// element is skipped — and its section is still preserved below.
	if _, ok := s.FindElement("hologram"); ok {
		t.Error("an element with an unknown kind must be SKIPPED, not guessed at")
	}
	if len(s.Notes()) == 0 {
		t.Error("nothing was noted; the import report would show a silently different theme")
	}

	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Fatalf("a save rewrote degraded values instead of preserving them:\n%s", out)
	}
}

// TestFutureFormatRendersAndRoundTrips is the frozen fixture's gate. The file
// is fabricated by hand and MUST NEVER be regenerated by our writer: a fixture
// the writer produced proves only that the writer agrees with itself.
func TestFutureFormatRendersAndRoundTrips(t *testing.T) {
	src := readThemeFixture(t, "future", SidecarFileName)

	// Guard the "frozen" property itself. These are shapes our writer cannot
	// produce — an unknown section family and an unknown kind — so their
	// presence proves the file was not regenerated.
	for _, marker := range []string{"[shader.bloom]", "hologram", "x_vendor_stamp", "twilight"} {
		if !bytes.Contains(src, []byte(marker)) {
			t.Fatalf("the frozen fixture lost %q — it looks REGENERATED, which makes this gate self-fulfilling", marker)
		}
	}

	s, err := ParseSidecar(src)
	if err != nil {
		t.Fatalf("a format-2 theme must never fail to load: %v", err)
	}
	if s.Format != 2 || !s.IsFutureFormat() {
		t.Errorf("Format = %d, IsFutureFormat = %v; want 2, true", s.Format, s.IsFutureFormat())
	}
	// It RENDERS: the parts this build understands are all there.
	if s.Meta.Name != "Tomorrow's Theme" {
		t.Errorf("name = %q", s.Meta.Name)
	}
	if _, ok := s.OverrideRect("ic_chatlog"); !ok {
		t.Error("a known override in a future-format theme did not parse")
	}
	if s.Palette.Accent == nil {
		t.Error("a known palette role in a future-format theme did not parse")
	}
	plate, ok := s.FindElement("plate")
	if !ok {
		t.Fatal("the drawable element of a future-format theme was dropped")
	}
	if plate.Kind != ElemGradient || plate.Space != SpaceCourtroom || plate.Fit != FitStretch {
		t.Errorf("plate = kind %v space %v fit %v; the unknown values must degrade, not fail",
			plate.Kind, plate.Space, plate.Fit)
	}
	if _, ok := s.FindElement("hologram"); ok {
		t.Error("the unknown-kind element must be skipped")
	}
	if p, ok := s.FindPane("tomorrow"); !ok || p.NHosts != 1 {
		t.Errorf("the pane did not parse: %v", ok)
	}

	// And a save is byte-identical, including every line we did not understand.
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("saving a future-format theme changed %d bytes in / %d out; first diff at %d",
			len(src), len(out), firstDiff(src, out))
	}
}

// TestVendorNamespaceIsPreservedAndNeverAssigned holds both halves of the
// reservation: third-party state survives, and AsyncAO never defines a key or
// section of that shape — which is what makes squatting there safe forever.
func TestVendorNamespaceIsPreservedAndNeverAssigned(t *testing.T) {
	official := []string{
		secTheme, secOverrides, secRotations, secPalette, secChrome, secFonts,
		secFontBind, secMedia, secSounds, secImport,
		secElementPrefix, secPanePrefix, secClockPrefix, secEffectPrefix,
	}
	for _, name := range official {
		if strings.HasPrefix(name, VendorSectionPrefix) {
			t.Errorf("official section %q squats in the reserved vendor namespace", name)
		}
	}
	var officialKeys []string
	for _, e := range elementFieldKeys {
		if e.key != "" {
			officialKeys = append(officialKeys, e.key)
		}
	}
	officialKeys = append(officialKeys,
		"format", "revision", "name", "author", "license", "credit", "description",
		"min_client", "base", "subtheme", "layout_preset", "style_preset", "generated_by",
		"shape", "shape_tier", "tabbar_hex", "speed_pct", "rect", "space", "hosts",
		"divider", "divider_px", "period_ms", "amp_pct", "clock",
		"derived_from", "derived_at", "derived_hash", "subthemes", "per_misc", "lobby_design")
	for _, r := range paletteRoleKeys {
		officialKeys = append(officialKeys, r.key)
	}
	for _, k := range officialKeys {
		if strings.HasPrefix(k, VendorKeyPrefix) {
			t.Errorf("official key %q squats in the reserved vendor key namespace", k)
		}
	}

	src := "[theme]\nformat = 1\nx_stamp = 0xdeadbeef\n\n[x.vendor.tool]\nstate = whatever\n"
	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("the vendor namespace did not survive a round trip:\n%s", out)
	}
	if !containsString(s.Unknown(), "x.vendor.tool/state") || !containsString(s.Unknown(), "theme/x_stamp") {
		t.Errorf("Unknown() = %v, want the vendor entries enumerated", s.Unknown())
	}
}

// ---------------------------------------------------------------------------
// Free text
// ---------------------------------------------------------------------------

// TestFreeTextRoundTripsEverySemicolon drives the ONE character this reader
// cannot carry raw through every position in a string, and takes it all the way
// through the real INI writer and the real INI reader — not just through the
// encoder, which is where a test like this quietly stops being about anything.
func TestFreeTextRoundTripsEverySemicolon(t *testing.T) {
	base := "red truth"
	for i := 0; i <= len(base); i++ {
		for _, injected := range []string{";", ";;", "%", "%3B", "\n", "\r\n", "; ; ;"} {
			want := base[:i] + injected + base[i:]
			assertFreeTextSurvivesAnINI(t, want)
		}
	}
	// The published example, verbatim from the format doc.
	assertFreeTextSurvivesAnINI(t, "Butterfly tile by Nyah; fonts EB Garamond (OFL).")
}

// assertFreeTextSurvivesAnINI writes s as a free-text value, reads the file back
// with the SHIPPING reader, and requires the value to come back unchanged.
func assertFreeTextSurvivesAnINI(t *testing.T, s string) {
	t.Helper()
	encoded := encodeFreeText(s)
	if strings.ContainsAny(encoded, ";\r\n") {
		t.Fatalf("encodeFreeText(%q) = %q still holds a character the reader truncates on", s, encoded)
	}
	d, err := ParseINIDoc([]byte("[theme]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set(secTheme, "credit", encoded); err != nil {
		t.Fatalf("the writer refused %q (encoded %q): %v", s, encoded, err)
	}
	ini, err := ParseINI(strings.NewReader(string(d.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := ini.GetSection(secTheme, "credit")
	if !ok {
		t.Fatalf("the key vanished writing %q", s)
	}
	if got := decodeFreeText(raw); got != strings.TrimSpace(s) {
		t.Errorf("free text round trip: %q -> %q -> %q", s, encoded, got)
	}
}

// TestFreeTextRoundTripSeeds runs the fuzz body over its seeds as a PLAIN test,
// so `go test -race` covers the cases the fuzzer only reaches under -fuzz.
func TestFreeTextRoundTripSeeds(t *testing.T) {
	for _, s := range freeTextFuzzSeeds {
		freeTextRoundTrip(t, s)
	}
}

// FuzzFreeTextRoundTrip drives the encoder with hostile bytes. Two contracts:
// the encoded form is always writable (it can never carry a ';', a newline or
// edge whitespace), and decoding it returns exactly what was encoded.
func FuzzFreeTextRoundTrip(f *testing.F) {
	for _, s := range freeTextFuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) { freeTextRoundTrip(t, raw) })
}

var freeTextFuzzSeeds = []string{
	"",
	" ",
	";",
	"%",
	"%25",
	"%3B",
	"%3b",
	"%0A%0D",
	"a;b",
	"a%b;c\nd\re",
	"  padded  ",
	"\n\n\n",
	"Golden butterflies; red truth.",
	"100% sure; maybe",
	"emoji \U0001F98B and ; and %",
	"\x00;\x00",
	"%%%;;;",
}

func freeTextRoundTrip(t *testing.T, raw string) {
	t.Helper()
	encoded := encodeFreeText(raw)
	if badINIValue(encoded) {
		t.Fatalf("encodeFreeText(%q) = %q, which the writer would refuse", raw, encoded)
	}
	if got := decodeFreeText(encoded); got != strings.TrimSpace(raw) {
		t.Fatalf("decode(encode(%q)) = %q, want %q", raw, got, strings.TrimSpace(raw))
	}
	// Idempotence of the canonical form: canonicalising twice must not escape
	// the escapes again, or every save would grow the file.
	if twice := canonFree(encoded); twice != encoded {
		t.Fatalf("canonFree is not idempotent: %q -> %q", encoded, twice)
	}
}

// ---------------------------------------------------------------------------
// The writer
// ---------------------------------------------------------------------------

// fullElement is an Element with EVERY field set to something that is not its
// default, so a writer that forgets one is caught by the round trip rather than
// by a user noticing a setting does not stick.
func fullElement() Element {
	e := Element{
		ID: "full", Kind: ElemShape, Band: BandOverlay, Z: -300, Space: SpaceWindow,
		Anchor: "ao2_chatbox", Rect: Rect{X: -8, Y: 12, W: 296, H: 180}, Rot: 64,
		Media: "butterflies", Shape: "ribbon", Gen: "halftone",
		Fit: FitNine, Slice: [4]int16{1, 2, 3, 4},
		Fill: RGBA{26, 18, 6, 255}, Fill2: RGBA{200, 163, 74, 128},
		GradAngle: 192, GradRadial: true,
		Stroke: RGBA{200, 163, 74, 255}, StrokePx: 2,
		Tint: RGBA{1, 2, 3, 4}, Opacity: 190,
		Text: "...and so the game begins; again.", Font: "message", Size: 18,
		Color: RGBA{200, 163, 74, 255}, Align: AlignRight,
		Clock: 5, PhaseMs: 250, Loop: true,
		Effect: FXSlam, EffectPeriodMs: 2400, EffectAmpPct: 60,
		NoDecimate:  true,
		VisibleWhen: Condition{Axis: CondChar, Value: "phoenix"},
		Locked:      true, Hidden: true,
	}
	e.GenParams[0] = KV{Key: "dot", Value: "4"}
	e.GenParams[1] = KV{Key: "tint", Value: "#c8a34a"}
	return e
}

// TestWriterNeverEmitsAValueThatReadsAsZero is the writer's half of the parity
// contract. atoiTrim swallows parse errors ON PURPOSE (AO2 parity), so a
// malformed number we emit does not fail — it silently becomes 0, and an
// element quietly moves to the origin. Nothing we write may depend on that
// leniency.
func TestWriterNeverEmitsAValueThatReadsAsZero(t *testing.T) {
	s := NewSidecar()
	s.Elements = append(s.Elements, fullElement())
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	ini, err := ParseINI(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	// Every numeric key whose model value is non-zero must read back non-zero.
	numeric := map[string]int{
		"z": -300, "rot": 64, "grad_angle": 192, "stroke_px": 2, "opacity": 190,
		"size": 18, "clock": 5, "phase_ms": 250, "effect_period_ms": 2400,
		"effect_amp_pct": 60,
	}
	for key, want := range numeric {
		raw, ok := ini.GetSection("element.full", key)
		if !ok {
			t.Errorf("%s was never written", key)
			continue
		}
		if got := atoiTrim(raw); got != want {
			t.Errorf("%s = %q reads back as %d, want %d", key, raw, got, want)
		}
	}
	// Tuples are written in AO2's exact shape and must survive AO2's own reader.
	if raw, _ := ini.GetSection("element.full", "rect"); raw != "-8, 12, 296, 180" {
		t.Errorf("rect = %q, want AO2's exact tuple shape", raw)
	}

	// The whole element, through the real reader, field for field.
	back, err := ParseSidecar(out)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := back.FindElement("full")
	if !ok {
		t.Fatal("the written element did not read back at all")
	}
	want := fullElement()
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("element did not survive write+read:\n got %+v\nwant %+v", *got, want)
	}
}

// TestSidecarSchemaMatchesTheElementStruct closes the model-vs-format drift
// class: a field added to Element without a wire key would compile, save as
// nothing, and be noticed only when a user's setting failed to stick. It is
// TestEverySavedPreferenceIsAlsoLoaded's trick, applied to the theme format.
func TestSidecarSchemaMatchesTheElementStruct(t *testing.T) {
	typ := reflect.TypeOf(Element{})
	table := map[string]string{}
	for _, e := range elementFieldKeys {
		if _, dup := table[e.field]; dup {
			t.Errorf("elementFieldKeys lists %q twice", e.field)
		}
		table[e.field] = e.key
	}
	// Every FIELD has an entry.
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := table[name]; !ok {
			t.Errorf("Element.%s has no entry in elementFieldKeys — it would save as nothing. "+
				"Add its wire key (or \"\" if the section name carries it) and a writer row.", name)
		}
	}
	// Every ENTRY is a field.
	for field := range table {
		if _, ok := typ.FieldByName(field); !ok {
			t.Errorf("elementFieldKeys names %q, which is not a field of Element", field)
		}
	}
	// Wire keys are unique.
	seen := map[string]string{}
	for _, e := range elementFieldKeys {
		if e.key == "" {
			continue
		}
		if prev, dup := seen[e.key]; dup {
			t.Errorf("wire key %q is claimed by both %s and %s", e.key, prev, e.field)
		}
		seen[e.key] = e.field
	}

	// And every wire key is actually EMITTED. A key in the table that the
	// writer never writes is the same silent loss with an extra step.
	s := NewSidecar()
	s.Elements = append(s.Elements, fullElement())
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	ini, err := ParseINI(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for key, field := range seen {
		if _, ok := ini.GetSection("element.full", key); !ok {
			t.Errorf("Element.%s has wire key %q, but a fully-populated element never wrote it", field, key)
		}
	}
	// The reader must call every emitted key KNOWN, or a save would report its
	// own output as somebody else's unknown extension.
	for key := range seen {
		if !elementKeyKnown(key) {
			t.Errorf("the writer emits %q but the reader treats it as unknown", key)
		}
	}
}

// TestSidecarWritesOnlyWhatTheAuthorChose keeps fresh files small and diffs
// readable: a default-valued key is not written at all, so an author's file
// lists their decisions and nothing else.
func TestSidecarWritesOnlyWhatTheAuthorChose(t *testing.T) {
	s := NewSidecar()
	s.Elements = append(s.Elements, Element{ID: "bare", Kind: ElemText, Text: "hi"})
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, unwanted := range []string{"opacity", "grad_angle", "locked", "hidden", "slice", "rect"} {
		if strings.Contains(got, unwanted+" =") {
			t.Errorf("a default-valued %q was written:\n%s", unwanted, got)
		}
	}
	for _, wanted := range []string{"[element.bare]", "kind = text", "text = hi"} {
		if !strings.Contains(got, wanted) {
			t.Errorf("missing %q:\n%s", wanted, got)
		}
	}
}

// elementFile is the smallest legal file that carries one element key.
func elementFile(line string) string {
	return "[theme]\nformat = 1\n\n[element.e]\n" + line + "\n"
}

// onlyElement returns the single element such a file declares.
func onlyElement(t *testing.T, s *Sidecar) *Element {
	t.Helper()
	if len(s.Elements) != 1 {
		t.Fatalf("%d elements parsed, want the one the file declares", len(s.Elements))
	}
	return &s.Elements[0]
}

// TestOutOfRangeNumbersClampAndAreLeftInTheFile is the writer's hardest promise
// (sidecar_write.go's header, and §7's "emit a key only when the file does not
// already mean what you are writing"): a number the reader had to NARROW still
// means what the file says, so an untouched save must not touch it.
//
// Every narrowing the reader performs therefore needs a canonicaliser that
// performs the SAME one. The drift is invisible until a user opens a theme,
// changes nothing, saves, and finds a diff — and for the millisecond fields it
// was worse than a diff: a bare int32 conversion rewrote a legal 3000000000 as
// a NEGATIVE phase, silently changing what the element does.
//
// A NEW numeric key that clamps, wraps or truncates needs a row here.
func TestOutOfRangeNumbersClampAndAreLeftInTheFile(t *testing.T) {
	cases := []struct {
		what  string
		src   string
		check func(*testing.T, *Sidecar) (got, want int)
	}{
		{"z in range (the control: this must be boring)", elementFile("z = 100"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Z), 100 }},
		{"z over int16", elementFile("z = 40000"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Z), ZMax }},
		{"z under int16", elementFile("z = -40000"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Z), ZMin }},
		{"slice over int16", elementFile("slice = 40000, 0, 0, 0"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Slice[0]), ZMax }},
		{"nine_slice over int16", elementFile("nine_slice = 40000"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Slice[0]), ZMax }},
		{"phase_ms over int32", elementFile("phase_ms = 3000000000"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).PhaseMs), TimeMsMax }},
		{"phase_ms under int32", elementFile("phase_ms = -3000000000"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).PhaseMs), TimeMsMin }},
		{"effect_period_ms over int32", elementFile("effect_period_ms = 3000000000"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).EffectPeriodMs), TimeMsMax }},
		{"opacity over a byte", elementFile("opacity = 300"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Opacity), 255 }},
		{"stroke_px over a byte", elementFile("stroke_px = 300"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).StrokePx), 255 }},
		{"effect_amp_pct over the range", elementFile("effect_amp_pct = 400"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).EffectAmpPct), AmpMaxPct }},
		{"size over the point cap", elementFile("size = 9000"),
			func(t *testing.T, s *Sidecar) (int, int) { return onlyElement(t, s).Size, TextSizeMaxPt }},
		{"clock past the pool", elementFile("clock = 99"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Clock), 0 }},
		{"rot wraps", elementFile("rot = 300"),
			func(t *testing.T, s *Sidecar) (int, int) { return int(onlyElement(t, s).Rot), 300 % AngleCount }},
		{"[effect.*] period_ms over int32", "[theme]\nformat = 1\n\n[effect.ao2_chatbox]\nperiod_ms = 3000000000\n",
			func(t *testing.T, s *Sidecar) (int, int) {
				fx, ok := s.EffectFor("ao2_chatbox")
				if !ok {
					t.Fatal("the widget effect did not parse")
				}
				return int(fx.PeriodMs), TimeMsMax
			}},
		{"[effect.*] amp_pct over the range", "[theme]\nformat = 1\n\n[effect.ao2_chatbox]\namp_pct = 400\n",
			func(t *testing.T, s *Sidecar) (int, int) {
				fx, _ := s.EffectFor("ao2_chatbox")
				return int(fx.AmpPct), AmpMaxPct
			}},
		{"speed_pct over the pool maximum", "[theme]\nformat = 1\n\n[clock.2]\nspeed_pct = 999\n",
			func(t *testing.T, s *Sidecar) (int, int) { return int(s.ClockSpeedPct(2)), SpeedMaxPct }},
		{"divider_px over a byte", "[theme]\nformat = 1\n\n[pane.p]\ndivider_px = 300\n",
			func(t *testing.T, s *Sidecar) (int, int) {
				p, ok := s.FindPane("p")
				if !ok {
					t.Fatal("the pane did not parse")
				}
				return int(p.DividerPx), 255
			}},
	}
	for _, c := range cases {
		s, err := ParseSidecar([]byte(c.src))
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if got, want := c.check(t, s); got != want {
			t.Errorf("%s: the reader stored %d, want %d", c.what, got, want)
		}
		out, err := s.Bytes()
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if string(out) != c.src {
			t.Errorf("%s: an UNTOUCHED save rewrote the file. The canonicaliser is not running "+
				"the reader's own conversion, so the writer thinks the file means something else.\n"+
				" in: %q\nout: %q", c.what, c.src, string(out))
		}
	}
}

// TestALegalClockSpellingIsNeverDuplicated pins the other half of "an untouched
// save changes nothing": the SECTION NAME is the author's too.
//
// "01" matches the format's id grammar and reads as clock 1. A writer that
// rebuilt the name from the array index would not find [clock.01], would append
// a [clock.1] beside it, and the file would then say the same thing twice — a
// mutation of a theme nobody edited.
func TestALegalClockSpellingIsNeverDuplicated(t *testing.T) {
	for _, spelling := range []string{"1", "01", "0000001"} {
		head := "[theme]\nformat = 1\n\n[clock." + spelling + "]\n"
		src := head + "speed_pct = 60\n"
		s, err := ParseSidecar([]byte(src))
		if err != nil {
			t.Errorf("[clock.%s]: %v", spelling, err)
			continue
		}
		if got := s.ClockSpeedPct(1); got != 60 {
			t.Errorf("[clock.%s] resolved to clock speed %d, want 60", spelling, got)
		}
		out, err := s.Bytes()
		if err != nil {
			t.Errorf("[clock.%s]: %v", spelling, err)
			continue
		}
		if string(out) != src {
			t.Errorf("[clock.%s]: an untouched save rewrote the file:\n%q", spelling, string(out))
		}
		// And an EDIT lands on the author's own section rather than beside it.
		s.Clocks[1].SpeedPct = 120
		out, err = s.Bytes()
		if err != nil {
			t.Errorf("[clock.%s]: %v", spelling, err)
			continue
		}
		if want := head + "speed_pct = 120\n"; string(out) != want {
			t.Errorf("[clock.%s]: an edit did not land on the author's section:\n%q", spelling, string(out))
		}
	}
	// A group the EDITOR declared has no source spelling, so it gets the
	// canonical one — there is no author's choice to preserve.
	s := NewSidecar()
	s.Clocks[2] = ClockSpec{SpeedPct: 140, Declared: true}
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[clock.2]") || !strings.Contains(string(out), "speed_pct = 140") {
		t.Errorf("a newly declared clock group was not written canonically:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Caps, panes, zero values, migration
// ---------------------------------------------------------------------------

// TestSidecarCapsRefuseNotTruncate holds hard rule 4's second half. Refusing is
// louder than truncating and it is the only safe answer: a truncated theme that
// then gets SAVED has lost the author's work, permanently and silently.
func TestSidecarCapsRefuseNotTruncate(t *testing.T) {
	cases := []struct {
		what string
		at   string // a file exactly AT the cap: must parse
		over string // one past it: must be refused
	}{
		{"elements", repeatSection("element.e%d", "kind = gradient\nfill = #ffffff\n", ElementCap),
			repeatSection("element.e%d", "kind = gradient\nfill = #ffffff\n", ElementCap+1)},
		{"panes", repeatSection("pane.p%d", "rect = 1, 2, 3, 4\n", PaneCap),
			repeatSection("pane.p%d", "rect = 1, 2, 3, 4\n", PaneCap+1)},
		{"effects", repeatSection("effect.w%d", "effect = glow\n", EffectBindCap),
			repeatSection("effect.w%d", "effect = glow\n", EffectBindCap+1)},
		{"media", "[media]\n" + repeatKeys("m%d", "file%d.png", MediaCap),
			"[media]\n" + repeatKeys("m%d", "file%d.png", MediaCap+1)},
		{"fonts", "[fonts]\n" + repeatKeys("fam%d", "face%d.ttf", FontCap),
			"[fonts]\n" + repeatKeys("fam%d", "face%d.ttf", FontCap+1)},
		{"fontbind", "[fontbind]\n" + repeatKeys("el%d", "fam%d", FontBindCap),
			"[fontbind]\n" + repeatKeys("el%d", "fam%d", FontBindCap+1)},
		{"overrides", "[overrides]\n" + repeatKeys("k%d", "1, 2, 3, 4", OverrideCap),
			"[overrides]\n" + repeatKeys("k%d", "1, 2, 3, 4", OverrideCap+1)},
		{"rotations", "[rotations]\n" + repeatKeys("k%d", "64", OverrideCap),
			"[rotations]\n" + repeatKeys("k%d", "64", OverrideCap+1)},
		{"sounds", "[sounds]\n" + repeatKeys("s%d", "a%d.opus", SoundCap),
			"[sounds]\n" + repeatKeys("s%d", "a%d.opus", SoundCap+1)},
		{"pane hosts", "[pane.p]\nhosts = " + repeatList("h%d", PaneHostCap) + "\n",
			"[pane.p]\nhosts = " + repeatList("h%d", PaneHostCap+1) + "\n"},
		{"gen params", "[element.g]\nkind = gen\ngen_params = " + repeatPairs(GenParamCap) + "\n",
			"[element.g]\nkind = gen\ngen_params = " + repeatPairs(GenParamCap+1) + "\n"},
		{"element text", "[element.t]\nkind = text\ntext = " + strings.Repeat("x", TextRuneCap) + "\n",
			"[element.t]\nkind = text\ntext = " + strings.Repeat("x", TextRuneCap+1) + "\n"},
		{"unknown entries", "[unknown]\n" + repeatKeys("k%d", "1", PreservedCap-1),
			"[unknown]\n" + repeatKeys("k%d", "1", PreservedCap+1)},
		{"subthemes", "[import]\nsubthemes = " + repeatList("s%d", SubthemeCap) + "\n",
			"[import]\nsubthemes = " + repeatList("s%d", SubthemeCap+1) + "\n"},
	}
	for _, c := range cases {
		if _, err := ParseSidecar([]byte(c.at)); err != nil {
			t.Errorf("%s: a file exactly AT the cap was refused: %v", c.what, err)
		}
		s, err := ParseSidecar([]byte(c.over))
		if !errors.Is(err, ErrSidecarCap) {
			t.Errorf("%s: one past the cap gave (%v, %v), want ErrSidecarCap", c.what, s != nil, err)
		}
		if s != nil {
			t.Errorf("%s: a refused file still produced a sidecar — that is the truncation this rule forbids", c.what)
		}
	}
}

func repeatSection(nameFmt, body string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("[" + strings.Replace(nameFmt, "%d", strconv.Itoa(i), 1) + "]\n")
		b.WriteString(body)
	}
	return b.String()
}

func repeatKeys(keyFmt, valFmt string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(strings.Replace(keyFmt, "%d", strconv.Itoa(i), 1))
		b.WriteString(" = ")
		b.WriteString(strings.Replace(valFmt, "%d", strconv.Itoa(i), 1))
		b.WriteString("\n")
	}
	return b.String()
}

func repeatList(itemFmt string, n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = strings.Replace(itemFmt, "%d", strconv.Itoa(i), 1)
	}
	return strings.Join(items, ", ")
}

func repeatPairs(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = "k" + strconv.Itoa(i) + "=" + strconv.Itoa(i)
	}
	return strings.Join(items, ", ")
}

// TestUnknownKindsPastNoteCapStillLoad is the OTHER half of the cap rule, and
// it points the opposite way: NoteCap bounds the import REPORT, never the load.
//
// The file below is a perfectly legal format-2 theme. ElementCap elements of a
// kind this build does not draw is EXACTLY what §7.4's forward-compatibility
// path looks like from an older client — and each one costs a note. A reader
// that let the 65th note refuse the parse would reject a legal file for being
// too new, which is the one thing §7.1 says a reader never does.
func TestUnknownKindsPastNoteCapStillLoad(t *testing.T) {
	if ElementCap <= NoteCap {
		t.Fatalf("this gate needs more elements (%d) than notes (%d) to reach the cap at all",
			ElementCap, NoteCap)
	}
	var b strings.Builder
	b.WriteString("[theme]\nformat = 2\n")
	for i := 0; i < ElementCap; i++ {
		fmt.Fprintf(&b, "\n[element.e%d]\nkind = hologram\nz = %d\ndepth_m = 4\n", i, i)
	}
	src := b.String()

	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatalf("a legal format-2 theme with %d unknown-kind elements was REFUSED: %v\n"+
			"NoteCap bounds the report, not the load (THEME-FORMAT §7.1 and §7.4).", ElementCap, err)
	}
	if !s.IsFutureFormat() {
		t.Error("format = 2 did not read as a future format")
	}
	if len(s.Elements) != 0 {
		t.Errorf("%d elements rendered — an unknown kind draws NOTHING (§7.4)", len(s.Elements))
	}
	// Every section is still there: the theme is whole the moment a newer client
	// opens it, which is the entire reason the kind was skipped instead of
	// degraded. An untouched save proves it byte for byte.
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Error("an untouched save of the unknown-kind theme rewrote the file")
	}
	if got := strings.Count(string(out), "[element."); got != ElementCap {
		t.Errorf("%d [element.*] sections survived, want %d", got, ElementCap)
	}
	// ...and the keys of a skipped element are still ENUMERATED, so the import
	// report can name what it is holding for the newer build.
	if got := len(s.Unknown()); got != ElementCap {
		t.Errorf("Unknown() holds %d entries, want %d (one unknown key per skipped element)", got, ElementCap)
	}
	// The REPORT is what the cap bounds, and it is bounded.
	if got := len(s.Notes()); got != NoteCap {
		t.Errorf("Notes() = %d, want exactly NoteCap (%d) — %d problems were reported, so the report is unbounded",
			got, NoteCap, got)
	}
}

// TestInvalidIdsPastNoteCapStillLoad reaches the same rule through the other
// degrade family: section ids the grammar rejects, and clock indices past the
// pool. One past NoteCap must cost a REPORT LINE, never the theme — the author
// of a file like this can no longer be asked to fix it.
func TestInvalidIdsPastNoteCapStillLoad(t *testing.T) {
	const bad = NoteCap + 1
	var b strings.Builder
	b.WriteString("[theme]\nformat = 1\n")
	for i := 0; i < bad; i++ {
		if i%2 == 0 {
			// A legal id that is not a legal clock GROUP: past the pool.
			fmt.Fprintf(&b, "\n[clock.%d]\nspeed_pct = 150\n", ClockCap+i)
		} else {
			// A pane id the [a-z0-9_-] grammar rejects outright.
			fmt.Fprintf(&b, "\n[pane.p!%d]\nrect = 1, 2, 3, 4\n", i)
		}
	}
	src := b.String()

	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatalf("%d invalid pane/clock ids REFUSED the whole theme: %v\n"+
			"NoteCap bounds the report, not the load.", bad, err)
	}
	if len(s.Panes) != 0 {
		t.Errorf("%d panes parsed from invalid ids", len(s.Panes))
	}
	for i, c := range s.Clocks {
		if c.Declared {
			t.Errorf("clock %d was declared by a section past the pool", i)
		}
	}
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Error("an untouched save rewrote a file full of ids it could not use")
	}
	// Each ignored section is preserved AND enumerated: the header entry plus
	// its one key.
	if got, want := len(s.Unknown()), bad*2; got != want {
		t.Errorf("Unknown() holds %d entries, want %d", got, want)
	}
	if got := len(s.Notes()); got != NoteCap {
		t.Errorf("Notes() = %d, want exactly NoteCap (%d)", got, NoteCap)
	}
}

// TestZeroValueElementIsInert pins the property every fixed-array design needs:
// a slot nobody filled in cannot paint. Element{} is an ElemImage with no
// media, so a cleared row, a zeroed array entry or a struct built by a future
// caller is silently harmless instead of silently drawing.
func TestZeroValueElementIsInert(t *testing.T) {
	var zero Element
	if !zero.Inert() {
		t.Error("the zero Element is not inert — a cleared slot would paint")
	}
	if zero.Kind != ElemImage || zero.Band != BandBackdrop || zero.Space != SpaceCourtroom {
		t.Errorf("the zero Element's enums are not the safe primitives: %+v", zero)
	}
	if zero.EffectiveOpacity() != OpacityOpaque {
		t.Errorf("EffectiveOpacity of an absent opacity = %d, want %d", zero.EffectiveOpacity(), OpacityOpaque)
	}
	if got := zero.EffectiveTint(); got != (RGBA{255, 255, 255, 255}) {
		t.Errorf("an absent tint resolves to %v — multiplying by that would black the art out", got)
	}
	if zero.GenParamCount() != 0 {
		t.Error("the zero Element claims generator parameters")
	}
	// An element section with nothing in it parses to the same inert thing.
	s, err := ParseSidecar([]byte("[element.empty]\n"))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s.FindElement("empty")
	if !ok {
		t.Fatal("an empty element section did not parse")
	}
	if !e.Inert() {
		t.Errorf("an empty element section is not inert: %+v", *e)
	}
	// And the other kinds are inert with nothing to draw.
	for _, c := range []Element{
		{Kind: ElemGen}, {Kind: ElemText}, {Kind: ElemShape},
		{Kind: ElemGradient}, {Kind: ElemPane}, {Kind: ElemText, Text: "x", Hidden: true},
	} {
		if !c.Inert() {
			t.Errorf("%+v is not inert but has nothing to paint", c)
		}
	}
	// A filled one is NOT inert, or the property would be vacuous.
	if (&Element{Kind: ElemText, Text: "hello"}).Inert() {
		t.Error("a text element with text is inert — the test would prove nothing")
	}
}

// TestPaneHostsAreDisjoint pins the structural half of split-screen: a widget
// has ONE drag owner and ONE wheel consumer, so it cannot be in two places. A
// second claim on the same key is dropped and reported; the theme still loads.
func TestPaneHostsAreDisjoint(t *testing.T) {
	src := "[theme]\nformat = 1\n\n[pane.left]\nhosts = ic_chatlog, ms_chatlog\n\n" +
		"[pane.right]\nhosts = ms_chatlog, music_list\n"
	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	left, _ := s.FindPane("left")
	right, _ := s.FindPane("right")
	if got := left.HostKeys(); !reflect.DeepEqual(got, []string{"ic_chatlog", "ms_chatlog"}) {
		t.Errorf("the FIRST claim must win: left hosts %v", got)
	}
	if got := right.HostKeys(); !reflect.DeepEqual(got, []string{"music_list"}) {
		t.Errorf("the duplicate claim was not dropped: right hosts %v", got)
	}
	owner, ok := s.PaneHosting("ms_chatlog")
	if !ok || owner.ID != "left" {
		t.Errorf("PaneHosting(ms_chatlog) = %v/%v", owner, ok)
	}
	if len(s.Notes()) == 0 {
		t.Error("the dropped host was not reported — the author would never learn why")
	}
	// A save persists the sanitation, on exactly one line. This is the one place
	// loading changes a value rather than preserving it, and it is deliberate:
	// the conflicting claim can never be honoured (the widget draws once), so
	// carrying it in the file would keep the theme in a state it never renders
	// in. The author is told, in Notes, before they ever press save.
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	inLines, outLines := strings.Split(src, "\n"), strings.Split(string(out), "\n")
	if len(inLines) != len(outLines) {
		t.Fatalf("line count changed %d -> %d:\n%s", len(inLines), len(outLines), out)
	}
	changed := 0
	for i := range inLines {
		if inLines[i] != outLines[i] {
			changed++
			if outLines[i] != "hosts = music_list" {
				t.Errorf("line %d = %q, want the disjoint host set", i, outLines[i])
			}
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want exactly 1:\n%s", changed, out)
	}
}

// TestPaneCannotHostCourtroom pins the other structural rule: "courtroom" is
// the design canvas every other rect is expressed inside and "viewport" is the
// stage, so re-homing either would reparent a space onto itself.
func TestPaneCannotHostCourtroom(t *testing.T) {
	s, err := ParseSidecar([]byte("[pane.p]\nhosts = courtroom, viewport, ic_chatlog\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.FindPane("p")
	if !ok {
		t.Fatal("the pane did not parse")
	}
	if got := p.HostKeys(); !reflect.DeepEqual(got, []string{"ic_chatlog"}) {
		t.Errorf("hosts = %v, want only the legal one", got)
	}
	for _, forbidden := range paneForbiddenHosts {
		if _, hosted := s.PaneHosting(forbidden); hosted {
			t.Errorf("%q was re-homed into a pane", forbidden)
		}
	}
}

// TestMigrationChainIsIdentityAtV1 exists so the machinery is in place BEFORE
// it is needed: the chain is called, tested and empty, which is exactly what a
// first version's migration should be. The alternative is designing it under
// pressure the day a format change is unavoidable.
func TestMigrationChainIsIdentityAtV1(t *testing.T) {
	if len(sidecarMigrations) != ThemeFormatVersion-1 {
		t.Fatalf("the chain holds %d steps for format %d — every version gap needs exactly one",
			len(sidecarMigrations), ThemeFormatVersion)
	}
	src := "; a comment\n[theme]\nformat = 1\nname = Identity\n"
	d, err := ParseINIDoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// Every version a reader can meet: older than 1, exactly 1, and NEWER.
	for _, from := range []int{-1, 0, 1, 2, 99} {
		if err := migrateSidecar(from, d); err != nil {
			t.Fatalf("migrating from %d: %v", from, err)
		}
	}
	if d.Dirty() {
		t.Error("the identity chain marked the document dirty")
	}
	if got := string(d.Bytes()); got != src {
		t.Errorf("the identity chain changed bytes:\n%s", got)
	}
	if err := migrateSidecar(1, nil); err != nil {
		t.Errorf("migrating a nil document: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The hand-authored fixtures
// ---------------------------------------------------------------------------

// TestHandAuthoredFixturesRoundTripByteIdentical is why the fixtures exist at
// all: they were typed by hand against the published spec, and a save must give
// every byte back — comments, alignment, aliases, alien spellings and all.
func TestHandAuthoredFixturesRoundTripByteIdentical(t *testing.T) {
	for _, name := range []string{"umineko-meta-world", "paper-lantern", "future"} {
		src := readThemeFixture(t, name, SidecarFileName)
		s, err := ParseSidecar(src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out, err := s.Bytes()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Equal(out, src) {
			t.Errorf("%s: a save changed the file (%d in, %d out); first diff at byte %d",
				name, len(src), len(out), firstDiff(src, out))
		}
	}
}

// TestTheAO2HalfOfAThemeIsNeverRewritten is the claim the whole sidecar
// decision rests on: geometry edits live in asyncao_theme.ini, so the author's
// courtroom_design.ini is not written in the normal path at all — and in the one
// export mode that does write it, it goes through INIDoc, whose behaviour on any
// line it did not deliberately change is "emit the original bytes".
//
// The AO2 half of the umineko fixture is checked in claiming exactly that in its
// own header. This is the assertion that makes the header true instead of
// decorative — and the second half proves the fixture is not vacuous: the
// override next door really does move the keys this file declares.
func TestTheAO2HalfOfAThemeIsNeverRewritten(t *testing.T) {
	src := readThemeFixture(t, "umineko-meta-world", DesignFileName)
	d, err := ParseINIDoc(src)
	if err != nil {
		t.Fatal(err)
	}
	if d.Dirty() {
		t.Error("merely reading the AO2 half marked it dirty")
	}
	if out := d.Bytes(); !bytes.Equal(out, src) {
		t.Fatalf("the AO2 half changed on a round trip (%d in, %d out); first diff at byte %d",
			len(src), len(out), firstDiff(src, out))
	}

	s, err := ParseSidecar(readThemeFixture(t, "umineko-meta-world", SidecarFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ic_chatlog", "ao2_chatbox"} {
		ov, ok := s.OverrideRect(key)
		if !ok {
			t.Errorf("the sidecar does not override %q — the pair of fixtures would prove nothing", key)
			continue
		}
		raw, ok := d.Get("", key)
		if !ok {
			t.Errorf("%s does not declare %q", DesignFileName, key)
			continue
		}
		if ov == parseRectValue(raw) {
			t.Errorf("%s: the override equals the AO2 rect, so a writer that DID rewrite the AO2 "+
				"file would look identical here", key)
		}
	}
}

// TestUminekoFixtureParsesEveryFeature reads the broad fixture the way the
// client will: if any of this drifts, a hand-authored theme silently loses a
// feature and nothing else notices.
func TestUminekoFixtureParsesEveryFeature(t *testing.T) {
	s, err := ParseSidecar(readThemeFixture(t, "umineko-meta-world", SidecarFileName))
	if err != nil {
		t.Fatal(err)
	}
	if s.Format != 1 || s.Revision != 3 || s.IsFutureFormat() {
		t.Errorf("format/revision = %d/%d", s.Format, s.Revision)
	}
	// Free text: the semicolons and the literal percent came back.
	if want := "Butterfly tile by Nyah; fonts EB Garamond (OFL)."; s.Meta.Credit != want {
		t.Errorf("credit = %q, want %q", s.Meta.Credit, want)
	}
	if want := "Golden butterflies; red truth; 100% hand-authored."; s.Meta.Description != want {
		t.Errorf("description = %q, want %q", s.Meta.Description, want)
	}
	if r, ok := s.OverrideRect("ic_chatlog"); !ok || r != (Rect{24, 320, 460, 260}) {
		t.Errorf("override = %v/%v", r, ok)
	}
	if a, ok := s.RotationFor("ic_chatlog"); !ok || a != 64 {
		t.Errorf("rotation = %v/%v", a, ok)
	}
	if s.Palette.Accent == nil || *s.Palette.Accent != (RGB{200, 163, 74}) {
		t.Errorf("palette accent = %v", s.Palette.Accent)
	}
	if s.Chrome.Shape != "rounded" || s.Chrome.ShapeTier != 2 {
		t.Errorf("chrome = %+v", s.Chrome)
	}
	if f, ok := s.FontFamilyFile("Meta World"); !ok || f != "EBGaramond-Italic.ttf" {
		t.Errorf("font family lookup = %q/%v", f, ok)
	}
	if fam, ok := s.BoundFamily("message"); !ok || fam != "Meta World" {
		t.Errorf("fontbind = %q/%v", fam, ok)
	}
	m, ok := s.FindMedia("butterflies")
	if !ok || m.Bytes != 1843200 || m.Hash != "9f2c1a4b8e0d1177" {
		t.Errorf("media = %+v/%v", m, ok)
	}
	if got := s.ClockSpeedPct(1); got != 60 {
		t.Errorf("clock 1 = %d", got)
	}
	if got := s.ClockSpeedPct(0); got != SpeedNominalPct {
		t.Errorf("clock 0 must be the shared anchor, got %d", got)
	}
	p, ok := s.FindPane("evidence")
	if !ok || p.NHosts != 2 || p.DividerPx != 2 {
		t.Errorf("pane = %+v/%v", p, ok)
	}
	if len(s.Elements) != 6 {
		t.Errorf("%d elements, want 6", len(s.Elements))
	}
	// Declaration order IS the tie-break order, so it must be preserved.
	var ids []string
	for _, e := range s.Elements {
		ids = append(ids, e.ID)
	}
	want := []string{"butterflies", "logpaper", "metaframe", "goldplate", "evidencepane", "verdict"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("element order = %v, want declaration order %v", ids, want)
	}
	e, _ := s.FindElement("butterflies")
	if e.Kind != ElemImage || e.Band != BandMid || e.Fit != FitCover || e.Opacity != 190 ||
		e.Clock != 1 || !e.Loop || e.VisibleWhen.Axis != CondPos || e.VisibleWhen.Value != "def" {
		t.Errorf("butterflies = %+v", *e)
	}
	if e.NoDecimate {
		t.Error("decimate = yes must leave NoDecimate false")
	}
	g, _ := s.FindElement("logpaper")
	if g.Gen != "halftone" || g.GenParamCount() != 4 || g.GenParams[0] != (KV{"dot", "4"}) {
		t.Errorf("generator element = %+v", *g)
	}
	// The read-only alias: "nine_slice = 12" is one inset on all four edges.
	f, _ := s.FindElement("metaframe")
	if f.Slice != [4]int16{12, 12, 12, 12} {
		t.Errorf("nine_slice alias produced %v", f.Slice)
	}
	v, _ := s.FindElement("verdict")
	if v.Kind != ElemText || v.Space != SpaceWindow || v.Align != AlignLeft || v.Effect != FXFade {
		t.Errorf("text element = %+v", *v)
	}
	if want := "...and so the game begins."; v.Text != want {
		t.Errorf("text = %q", v.Text)
	}
	fx, ok := s.EffectFor("ao2_chatbox")
	if !ok || fx.Effect != FXGlow || fx.PeriodMs != 1600 || fx.AmpPct != 60 {
		t.Errorf("widget effect = %+v/%v", fx, ok)
	}
	if snd, ok := s.SoundFile("ambient"); !ok || snd != "cicadas.opus" {
		t.Errorf("sound = %q/%v", snd, ok)
	}
	if !s.Import.PerMisc || !s.Import.LobbyDesign || len(s.Import.Subthemes) != 2 {
		t.Errorf("import = %+v", s.Import)
	}
	if !containsString(s.Unknown(), "x.somebodyelse.tool/whatever") {
		t.Errorf("the vendor section was not enumerated: %v", s.Unknown())
	}
}

// TestPaperLanternFixtureIsTheUglyOne pins the properties that make the second
// fixture worth having: it is spelled the way a human in a hurry spells things,
// and none of it is normalised on save.
func TestPaperLanternFixtureIsTheUglyOne(t *testing.T) {
	src := readThemeFixture(t, "paper-lantern", SidecarFileName)
	s, err := ParseSidecar(src)
	if err != nil {
		t.Fatal(err)
	}
	// Capitalised keys and values still resolve.
	e, ok := s.FindElement("lantern")
	if !ok {
		t.Fatal("the element did not parse")
	}
	if e.Kind != ElemShape || e.Band != BandOverlay || e.Space != SpaceMusicDisplay {
		t.Errorf("capitalised enum values did not resolve: %+v", *e)
	}
	if e.Rect != (Rect{4, 4, 120, 40}) {
		t.Errorf("a space-less tuple did not parse: %v", e.Rect)
	}
	if !e.Hidden {
		t.Error("hidden = true did not parse")
	}
	if e.Clock != 0 {
		t.Errorf("clock = 99 resolved to %d, want the shared anchor", e.Clock)
	}
	g, _ := s.FindElement("glow")
	if g.Opacity != 255 {
		t.Errorf("opacity = 300 clamped to %d, want 255", g.Opacity)
	}
	if g.Z != 3 {
		t.Errorf("z = %d, want the LAST declaration (3)", g.Z)
	}
	if g.VisibleWhen.Axis != CondShout || g.VisibleWhen.Value != "objection" {
		t.Errorf("visible_when = %+v", g.VisibleWhen)
	}
	if got := s.ClockSpeedPct(3); got != SpeedMaxPct {
		t.Errorf("speed_pct = 999 clamped to %d, want %d", got, SpeedMaxPct)
	}
	if want := "Lantern glow; hand-cut paper."; s.Meta.Credit != want {
		t.Errorf("the lowercase escape did not decode: %q", s.Meta.Credit)
	}
	if !containsString(s.Unknown(), "theme/mood") {
		t.Errorf("the unknown key was not enumerated: %v", s.Unknown())
	}
}

// TestASidecarWithWindowsLineEndingsRoundTrips is the shape most of the real
// theme corpus is in: CRLF, and no final newline. It is BUILT IN GO rather than
// checked in, deliberately — a fixture's line endings are a property of how the
// repository was checked out, and a gate that can be flipped by a git setting
// is a gate about git.
//
// bufio.ScanLines eats a trailing '\r' silently, so a writer that rebuilt lines
// would turn every Windows-authored theme into a whole-file diff, and one that
// appended a newline would touch the last line of every one of them.
func TestASidecarWithWindowsLineEndingsRoundTrips(t *testing.T) {
	src := strings.Join([]string{
		"; a Windows-authored sidecar",
		"[theme]",
		"format = 1",
		"name = Notepad",
		"",
		"[element.plate]",
		"kind = gradient",
		"z = 4",
		"fill = #201a12",
	}, "\r\n") // NOTE: no trailing terminator — the last line ends the file

	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.Name != "Notepad" {
		t.Errorf("name = %q — a '\\r' leaked into the value", s.Meta.Name)
	}
	out, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Fatalf("an untouched CRLF save changed the file:\n%q", out)
	}

	// An edit keeps the file's own ending, and an ADDED key does not invent a
	// final newline the author never wrote.
	e, _ := s.FindElement("plate")
	e.Z = 9
	e.Opacity = 128
	out, err = s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for i := 0; i < len(got); i++ {
		if got[i] == '\n' && (i == 0 || got[i-1] != '\r') {
			t.Fatalf("a bare LF was written at byte %d:\n%q", i, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("the save appended a final newline:\n%q", got)
	}
	if !strings.Contains(got, "z = 9\r\n") {
		t.Errorf("the edited line lost its terminator:\n%q", got)
	}
	if !strings.HasSuffix(got, "opacity = 128") {
		t.Errorf("the added key did not land at the end of its section:\n%q", got)
	}
}

// TestSidecarAccessorsAreNilSafe pins the stock-AO2-theme case. A theme with no
// sidecar is a nil *Sidecar, and every accessor answers "nothing" — so "zero
// elements, zero cost" needs no branch at any call site.
func TestSidecarAccessorsAreNilSafe(t *testing.T) {
	var s *Sidecar
	if s.IsFutureFormat() || s.Doc() != nil || s.Notes() != nil || s.Unknown() != nil {
		t.Error("a nil sidecar claimed to hold something")
	}
	if _, ok := s.FindElement("x"); ok {
		t.Error("FindElement on nil")
	}
	if _, ok := s.FindPane("x"); ok {
		t.Error("FindPane on nil")
	}
	if _, ok := s.PaneHosting("x"); ok {
		t.Error("PaneHosting on nil")
	}
	if _, ok := s.FindMedia("x"); ok {
		t.Error("FindMedia on nil")
	}
	if _, ok := s.OverrideRect("x"); ok {
		t.Error("OverrideRect on nil")
	}
	if _, ok := s.RotationFor("x"); ok {
		t.Error("RotationFor on nil")
	}
	if _, ok := s.FontFamilyFile("x"); ok {
		t.Error("FontFamilyFile on nil")
	}
	if _, ok := s.BoundFamily("x"); ok {
		t.Error("BoundFamily on nil")
	}
	if _, ok := s.SoundFile("x"); ok {
		t.Error("SoundFile on nil")
	}
	if _, ok := s.EffectFor("x"); ok {
		t.Error("EffectFor on nil")
	}
	if got := s.ClockSpeedPct(3); got != SpeedNominalPct {
		t.Errorf("ClockSpeedPct on nil = %d", got)
	}
	if b, err := s.Bytes(); b != nil || err != nil {
		t.Errorf("Bytes on nil = %v/%v", b, err)
	}
	// Note what is NOT claimed: the exported FIELDS are ordinary fields, and
	// reading one off a nil pointer panics like any other. The nil-safety
	// contract is about the METHODS, which is what a draw path calls.
}

// TestLoadSidecarTreatsAMissingFileAsAStockTheme: the overwhelmingly common
// case is a theme with no sidecar at all, and it must not look like an error.
func TestLoadSidecarTreatsAMissingFileAsAStockTheme(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSidecar(filepath.Join(dir, SidecarFileName))
	if s != nil || err != nil {
		t.Fatalf("a missing sidecar gave (%v, %v), want (nil, nil)", s != nil, err)
	}
	got, from, err := LoadSidecarIn([]string{dir, filepath.Join(themesFixtureDir, "umineko-meta-world")})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || filepath.Base(from) != "umineko-meta-world" {
		t.Fatalf("LoadSidecarIn picked %q (sidecar %v)", from, got != nil)
	}
	// ATOMIC, never merged: the first directory that HAS one wins outright.
	if len(got.Elements) != 6 {
		t.Errorf("%d elements from the first directory with a sidecar", len(got.Elements))
	}
	if none, _, err := LoadSidecarIn([]string{dir}); none != nil || err != nil {
		t.Errorf("LoadSidecarIn over a sidecar-less ladder = %v/%v", none != nil, err)
	}
}

// TestSidecarColoursAcceptEitherHexCase pins the colour grammar's case-blindness.
//
// It exists because it was NOT case-blind and nobody could see the difference.
// parseRGBAValue hands each digit to hexNibble, which knew '0'-'9' and 'a'-'f' and
// nothing else, so a sidecar written the way humans write hex — `#1C1C1C` — was
// refused, degraded to "keep the stylesheet's value", and noted into an import
// report that had no consumer. `#101010` on the next line parsed, because it
// happens to be all digits, which is exactly the shape of bug that survives a
// hand-check of the file.
//
// Both the '#rrggbb' and '#rrggbbaa' widths are covered, and a genuinely invalid
// digit must still fail — a parser that accepted everything would pass the first
// half of this test and mean nothing.
func TestSidecarColoursAcceptEitherHexCase(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want RGBA
	}{
		{"#1C1C1C", RGBA{0x1C, 0x1C, 0x1C, 255}},
		{"#1c1c1c", RGBA{0x1C, 0x1C, 0x1C, 255}},
		{"#E8EAE8", RGBA{0xE8, 0xEA, 0xE8, 255}},
		{"#eC34e4", RGBA{0xEC, 0x34, 0xE4, 255}}, // mixed, because nothing normalises it
		{"#101010", RGBA{0x10, 0x10, 0x10, 255}},
		{"#FF00FF80", RGBA{0xFF, 0x00, 0xFF, 0x80}},
		{"  #AbCdEf  ", RGBA{0xAB, 0xCD, 0xEF, 255}},
	} {
		got, ok := parseRGBAValue(tc.in)
		if !ok {
			t.Errorf("parseRGBAValue(%q) refused a legal colour — upper-case hex is the spelling every "+
				"shipped theme writes, and a refusal here silently drops the author's value", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("parseRGBAValue(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
	// Still a parser: a non-hex digit, a wrong width and an empty value all refuse.
	for _, bad := range []string{"#GGGGGG", "#12345", "#1234567", "#", "", "not a colour"} {
		if c, ok := parseRGBAValue(bad); ok {
			t.Errorf("parseRGBAValue(%q) = %+v, want a refusal", bad, c)
		}
	}
	// The RGB tuple spelling is unaffected by any of this.
	if c, ok := parseRGBAValue("236, 52, 228"); !ok || c != (RGBA{236, 52, 228, 255}) {
		t.Errorf("parseRGBAValue on the tuple spelling = %+v/%v", c, ok)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
