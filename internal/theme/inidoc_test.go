package theme

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aceattorney2xDir is the checked-in issue-#21 theme fixture: the exact theme
// from the side-by-side screenshot, comment-dominated, authored by a stranger.
//
// It lives under internal/ui because that is where it was first needed (the rect
// census). The round-trip gate reads it from there rather than duplicating 10 KB
// of third-party INI, and it MUST be that copy: internal/ui/testdata is checked
// in with `-text` (.gitattributes), so the bytes on disk are the bytes that were
// committed. A fixture git was allowed to normalise would make a byte-identical
// gate a statement about the checkout instead of about the writer.
const aceattorney2xDir = "../ui/testdata/themes/aceattorney2x"

// aceattorney2xFiles are its INI files. courtroom_fonts.ini deliberately has NO
// trailing newline (that is part of the fixture, and it is the case an
// append-a-newline writer silently breaks); the design INI is comment-heavy.
var aceattorney2xFiles = []string{DesignFileName, FontsFileName}

// TestINIDocRoundTripsByteIdentical is the gate the whole type exists for: a
// theme file parsed and re-emitted without an edit must come back with the same
// bytes, down to the missing final newline. A writer that rebuilt from the map
// would delete every comment in a fifteen-year-old community theme the first
// time a user nudged one box.
func TestINIDocRoundTripsByteIdentical(t *testing.T) {
	for _, name := range aceattorney2xFiles {
		src := readFixtureINI(t, name)
		d, err := ParseINIDoc(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := d.Bytes()
		if !bytes.Equal(got, src) {
			t.Errorf("%s: round trip changed the bytes (%d in, %d out); first diff at %d",
				name, len(src), len(got), firstDiff(src, got))
		}
		if d.Dirty() {
			t.Errorf("%s: an untouched document reports Dirty", name)
		}
		assertReaderParity(t, name, string(src), d)
	}
}

// TestINIDocPreservesTheMissingFinalNewline pins the fixture property that a
// naive writer breaks silently: courtroom_fonts.ini ends without one, and adding
// it would show up as a whole-file diff in the author's git history.
func TestINIDocPreservesTheMissingFinalNewline(t *testing.T) {
	src := readFixtureINI(t, FontsFileName)
	if bytes.HasSuffix(src, []byte("\n")) {
		t.Skip("fixture changed: courtroom_fonts.ini now ends with a newline")
	}
	d, err := ParseINIDoc(src)
	if err != nil {
		t.Fatal(err)
	}
	// Editing an EXISTING key must not invent a terminator either.
	if err := d.Set("", "message", "13"); err != nil {
		t.Fatal(err)
	}
	if out := d.Bytes(); bytes.HasSuffix(out, []byte("\n")) {
		t.Error("an edit appended a final newline the author did not write")
	}
	// Adding a NEW key moves the missing terminator onto the new last line
	// rather than inventing one, so the property survives.
	if err := d.Set("", "asyncao_new_key", "1"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if strings.HasSuffix(out, "\n") {
		t.Error("appending a key added a final newline")
	}
	if !strings.HasSuffix(out, "asyncao_new_key = 1") {
		t.Errorf("appended key landed wrong; tail = %q", tail(out, 40))
	}
}

// TestINIDocPreservesCRLF is the Windows-authored-theme case, which is most of
// the corpus: bufio.ScanLines eats a trailing '\r' silently, so an LF-only
// writer produces a whole-file diff on every one of them.
func TestINIDocPreservesCRLF(t *testing.T) {
	src := "; header\r\ncourtroom = 0, 0, 944, 600\r\n\r\n[Sec]\r\ninner = 5\r\n"
	d, err := ParseINIDoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if d.EOL() != "\r\n" {
		t.Errorf("EOL = %q, want CRLF", d.EOL())
	}
	if got := string(d.Bytes()); got != src {
		t.Fatalf("untouched CRLF round trip = %q", got)
	}
	if err := d.Set("", "courtroom", "0, 0, 1280, 720"); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("sec", "added", "7"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	for i := 0; i < len(out); i++ {
		if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
			t.Fatalf("a bare LF was written at byte %d: %q", i, out)
		}
	}
	if !strings.Contains(out, "courtroom = 0, 0, 1280, 720\r\n") {
		t.Errorf("edited line lost its terminator: %q", out)
	}
	if !strings.Contains(out, "added = 7\r\n") {
		t.Errorf("added line did not use the file's own ending: %q", out)
	}

	// MIXED endings still round-trip: every line re-emits its OWN terminator,
	// and only ADDED lines use the detected one.
	mixed := "a = 1\r\nb = 2\nc = 3\r\n"
	md, err := ParseINIDoc([]byte(mixed))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(md.Bytes()); got != mixed {
		t.Errorf("mixed-ending round trip = %q, want %q", got, mixed)
	}
	if md.EOL() != "\r\n" {
		t.Errorf("mixed-ending EOL = %q, want the FIRST terminator seen", md.EOL())
	}
}

// TestINIDocPreservesCommentsAndUnknownSections pins the forward-compat rule
// (§2.4.3): unknown keys, entire unknown sections and the reserved [x.<vendor>.*]
// namespace are carried through verbatim, and an edit changes exactly one line.
func TestINIDocPreservesCommentsAndUnknownSections(t *testing.T) {
	src := strings.Join([]string{
		"; Client size. Changing it will stretch courtroombackground.png.",
		"courtroom = 0, 0, 944, 600",
		"",
		"; WARNING: music_name x/y coordinates relative to music_display!",
		"music_name = 0, 0, 78, 26  ; keep this note",
		"",
		"[x.somevendor.tool]",
		"; a vendor section we know nothing about and never assign",
		"x_layout = whatever",
		"",
		"[Unknown Section]",
		"weird = 1",
		"",
	}, "\n")
	d, err := ParseINIDoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("", "music_name", "5, 6, 7, 8"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())

	inLines, outLines := strings.Split(src, "\n"), strings.Split(out, "\n")
	if len(inLines) != len(outLines) {
		t.Fatalf("line count changed: %d -> %d\n%s", len(inLines), len(outLines), out)
	}
	changed := 0
	for i := range inLines {
		if inLines[i] != outLines[i] {
			changed++
			if want := "music_name = 5, 6, 7, 8  ; keep this note"; outLines[i] != want {
				t.Errorf("line %d = %q, want %q — the trailing comment must survive the edit",
					i, outLines[i], want)
			}
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want exactly 1:\n%s", changed, out)
	}

	// And the reader agrees about every one of them afterwards.
	ini, err := ParseINI(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ini.Get("music_name"); !ok || v != "5, 6, 7, 8" {
		t.Errorf("music_name reads back as %q ok=%v", v, ok)
	}
	if v, ok := ini.GetSection("x.somevendor.tool", "x_layout"); !ok || v != "whatever" {
		t.Errorf("the vendor section did not survive: %q ok=%v", v, ok)
	}
	if v, ok := ini.GetSection("unknown section", "weird"); !ok || v != "1" {
		t.Errorf("the unknown section did not survive: %q ok=%v", v, ok)
	}
}

// TestINIDocRefusesOversizeAndOverlongFiles pins REFUSE-not-truncate on both
// caps. A truncated theme INI that then gets written back is data loss, which is
// the one failure this feature cannot recover from.
func TestINIDocRefusesOversizeAndOverlongFiles(t *testing.T) {
	oversize := bytes.Repeat([]byte("a"), IniDocByteCap+1)
	if d, err := ParseINIDoc(oversize); err != ErrINIDocTooLarge || d != nil {
		t.Errorf("oversize file: doc=%v err=%v, want (nil, ErrINIDocTooLarge)", d != nil, err)
	}
	// Exactly AT the byte cap is fine: the cap refuses what is PAST it.
	if _, err := ParseINIDoc(bytes.Repeat([]byte("a"), IniDocByteCap)); err != nil {
		t.Errorf("a file exactly at the byte cap was refused: %v", err)
	}

	overlong := []byte(strings.Repeat("k = v\n", IniDocLineCap+1))
	if d, err := ParseINIDoc(overlong); err != ErrINIDocTooManyLines || d != nil {
		t.Errorf("overlong file: doc=%v err=%v, want (nil, ErrINIDocTooManyLines)", d != nil, err)
	}
	full, err := ParseINIDoc([]byte(strings.Repeat("k = v\n", IniDocLineCap)))
	if err != nil {
		t.Fatalf("a file exactly at the line cap was refused: %v", err)
	}
	if full.Lines() != IniDocLineCap {
		t.Errorf("Lines = %d, want %d", full.Lines(), IniDocLineCap)
	}
	// Editing an existing key never grows the document, so it stays legal…
	if err := full.Set("", "k", "w"); err != nil {
		t.Errorf("editing at the line cap: %v", err)
	}
	// …but ADDING one past the cap is refused rather than silently dropped.
	if err := full.Set("", "brand_new", "1"); err != ErrINIDocTooManyLines {
		t.Errorf("adding a key past the line cap: %v, want ErrINIDocTooManyLines", err)
	}
	if full.Lines() != IniDocLineCap {
		t.Errorf("a refused add still grew the document to %d lines", full.Lines())
	}
}

// TestINIDocRefusesAValueTheReaderWouldNotReadBack pins the writer's half of the
// parity contract: this reader truncates a line at ';' (iniCommentStart), so
// writing one would silently lose data. Free text is percent-encoded by the
// caller before it arrives (the format spec's four characters).
func TestINIDocRefusesAValueTheReaderWouldNotReadBack(t *testing.T) {
	d, err := ParseINIDoc([]byte("k = v\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"a;b", "a\nb", "a\r\nb", " padded", "padded "} {
		if err := d.Set("", "k", bad); err != ErrINIDocBadValue {
			t.Errorf("Set(%q) = %v, want ErrINIDocBadValue", bad, err)
		}
	}
	for _, bad := range []string{"", "   ", "a=b", "a;b", "[x]", "a\nb", "#hash"} {
		if err := d.Set("", bad, "1"); err != ErrINIDocBadKey {
			t.Errorf("Set(key=%q) = %v, want ErrINIDocBadKey", bad, err)
		}
	}
	if got := string(d.Bytes()); got != "k = v\n" {
		t.Errorf("a refused Set still changed the document: %q", got)
	}
	// A key with an interior space is ordinary INI and must NOT be refused: the
	// reader trims the ends only, so it reads back as itself. A '#' is ordinary
	// value text in Qt (see iniCommentStart), so that is legal too.
	if err := d.Set("", "spaced key", "1"); err != nil {
		t.Errorf("Set(key=%q) = %v, want it accepted", "spaced key", err)
	}
	if err := d.Set("", "k", "a # b"); err != nil {
		t.Errorf("Set with a '#' in the value: %v", err)
	}
	ini, err := ParseINI(strings.NewReader(string(d.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ini.Get("spaced key"); !ok || v != "1" {
		t.Errorf("spaced key reads back as %q ok=%v", v, ok)
	}
	if v, ok := ini.Get("k"); !ok || v != "a # b" {
		t.Errorf("k reads back as %q ok=%v", v, ok)
	}
}

// TestINIDocAddsARootKeyInsideTheRootSection is the placement case that is
// silently wrong in the obvious implementation: appending at EOF drops a
// root-section key inside whatever section happens to be last, which makes it a
// different key entirely.
func TestINIDocAddsARootKeyInsideTheRootSection(t *testing.T) {
	// A file whose root section has no keys at all.
	d, err := ParseINIDoc([]byte("[Sec]\ninner = 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("", "courtroom", "0, 0, 944, 600"); err != nil {
		t.Fatal(err)
	}
	ini, err := ParseINI(strings.NewReader(string(d.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ini.Get("courtroom"); !ok || v != "0, 0, 944, 600" {
		t.Errorf("root key read back as %q ok=%v, from:\n%s", v, ok, d.Bytes())
	}
	if _, ok := ini.GetSection("sec", "courtroom"); ok {
		t.Error("the root key landed inside [Sec]")
	}
	if v, ok := ini.GetSection("sec", "inner"); !ok || v != "5" {
		t.Errorf("the existing section key was disturbed: %q ok=%v", v, ok)
	}

	// A key for a section that does not exist yet gets a header too.
	if err := d.Set("overrides", "ic_chatlog", "1, 2, 3, 4"); err != nil {
		t.Fatal(err)
	}
	ini, err = ParseINI(strings.NewReader(string(d.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ini.GetSection("overrides", "ic_chatlog"); !ok || v != "1, 2, 3, 4" {
		t.Errorf("new-section key read back as %q ok=%v, from:\n%s", v, ok, d.Bytes())
	}

	// The SAME file with a byte-order mark, which is where the obvious
	// implementation loses a whole section instead of one key. A BOM is
	// positional — it is a BOM only while it is the first thing in the file — so
	// the root key spliced in at line 0 must land BELOW it. Held as content of
	// line 0 the insertion pushes "[Sec]" under the BOM, the reader strips
	// nothing, that line parses as neither a header (no leading '[') nor a key
	// (no '=') and is dropped, and sec/inner silently reparents to the root
	// section. Write-side twin of TestBOMThemeKeepsItsFontSize.
	bd, err := ParseINIDoc([]byte(utf8BOM + "[Sec]\ninner = 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bd.Set("", "courtroom", "0, 0, 944, 600"); err != nil {
		t.Fatal(err)
	}
	out := string(bd.Bytes())
	if !strings.HasPrefix(out, utf8BOM) {
		t.Errorf("the BOM is no longer the first thing in the file: %q", out)
	}
	if n := strings.Count(out, utf8BOM); n != 1 {
		t.Errorf("the file carries %d BOMs, want exactly 1: %q", n, out)
	}
	bini, err := ParseINI(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := bini.Get("courtroom"); !ok || v != "0, 0, 944, 600" {
		t.Errorf("BOM'd root key read back as %q ok=%v, from %q", v, ok, out)
	}
	if v, ok := bini.GetSection("sec", "inner"); !ok || v != "5" {
		t.Errorf("[Sec] was destroyed by the insertion: sec/inner = %q ok=%v, from %q", v, ok, out)
	}
	if _, ok := bini.Get("inner"); ok {
		t.Errorf("sec/inner reparented into the root section: %q", out)
	}
	// And the document still agrees with the reader about every key it wrote.
	assertReaderParity(t, "BOM'd root-key insert", out, bd)
}

// TestINIDocRefusesABOMInAKeyOrSection is the other half of the BOM rule. The
// reader strips a leading U+FEFF from the file's FIRST line (ini.go:34), so a
// BOM-prefixed key added to an empty document would be indexed here as
// utf8BOM+"k" and read back there as "k": a document whose own classification
// disagrees with the reader, which is the one state INIDoc's PARITY-BOUND
// promise says must not exist. The fuzzer cannot reach this — it only ever drives
// ParseINIDoc, never Set — so the refusal is pinned here.
func TestINIDocRefusesABOMInAKeyOrSection(t *testing.T) {
	d, err := ParseINIDoc(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Refused ANYWHERE in the token, not just as a prefix: whether a BOM-carrying
	// token reads back as itself depends on where its line lands, and the writer
	// chooses that. Position-dependent classification is the thing being ruled out.
	for _, bad := range []string{utf8BOM + "k", "k" + utf8BOM, "a" + utf8BOM + "b", utf8BOM} {
		if err := d.Set("", bad, "1"); err != ErrINIDocBadKey {
			t.Errorf("Set(key=%q) = %v, want ErrINIDocBadKey", bad, err)
		}
		if err := d.Set(bad, "k", "1"); err != ErrINIDocBadKey {
			t.Errorf("Set(section=%q) = %v, want ErrINIDocBadKey", bad, err)
		}
	}
	if got := string(d.Bytes()); got != "" {
		t.Errorf("a refused Set still wrote %q", got)
	}
}

// TestINIDocKeepsTheBOMAtTheFrontOfTheFile pins the BOM as a DOCUMENT property
// rather than line content, on the three shapes that can move line 0: an added
// root key, an added section, and an edit to the first line itself.
func TestINIDocKeepsTheBOMAtTheFrontOfTheFile(t *testing.T) {
	// A BOM'd file that is nothing BUT a BOM still round-trips (fuzz seed), and a
	// key added to it lands after the mark, not before.
	only, err := ParseINIDoc([]byte(utf8BOM))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(only.Bytes()); got != utf8BOM {
		t.Errorf("BOM-only file round-tripped to %q", got)
	}
	if err := only.Set("", "message", "12"); err != nil {
		t.Fatal(err)
	}
	// A BOM-only file holds no lines at all, so there is no "last line with no
	// terminator" to move one off: the added line gets the default ending, the
	// same as adding a key to an empty file does.
	if got := string(only.Bytes()); got != utf8BOM+"message = 12"+only.EOL() {
		t.Errorf("BOM-only file + one key = %q", got)
	}

	// The Uminek shape: BOM, then a key on line 0. Editing that key must not
	// disturb the mark, and the first key must keep reading back.
	d, err := ParseINIDoc([]byte(utf8BOM + "showname = 16\nmessage = 12\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := d.Get("", "showname"); !ok || v != "16" {
		t.Fatalf("the BOM ate the first key: %q ok=%v", v, ok)
	}
	if err := d.Set("", "showname", "24"); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("fonts", "added", "1"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.HasPrefix(out, utf8BOM+"showname = 24\n") {
		t.Errorf("edited BOM'd first line = %q", out)
	}
	if n := strings.Count(out, utf8BOM); n != 1 {
		t.Errorf("the file carries %d BOMs, want exactly 1: %q", n, out)
	}
	assertReaderParity(t, "BOM'd edit", out, d)
}

// TestINIDocSetTargetsTheDeclarationTheReaderUses pins last-declaration-wins. A
// document that edited the FIRST of two declarations would write a change the
// reader never sees — a silent no-op edit, which is worse than an error.
func TestINIDocSetTargetsTheDeclarationTheReaderUses(t *testing.T) {
	src := "dupe = first\nother = x\ndupe = second\n"
	d, err := ParseINIDoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Get("", "dupe"); v != "second" {
		t.Errorf("Get = %q, want the LAST declaration (ParseINI's map assignment order)", v)
	}
	if err := d.Set("", "dupe", "edited"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "dupe = first\n") {
		t.Errorf("the shadowed declaration was rewritten: %q", out)
	}
	ini, err := ParseINI(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := ini.Get("dupe"); v != "edited" {
		t.Errorf("the reader sees %q after the edit, want \"edited\"", v)
	}
}

// TestINIDocRoundTripSeeds runs the fuzz body over its own seed corpus as a
// PLAIN test, so `go test -race` covers the cases the fuzzer only reaches under
// `-fuzz` (which the gate does not run).
func TestINIDocRoundTripSeeds(t *testing.T) {
	for _, s := range iniDocFuzzSeeds {
		iniDocRoundTrip(t, s)
	}
}

// FuzzINIDocRoundTrip drives the lossless model with hostile bytes. Two
// contracts, both load-bearing: an untouched document re-emits its source
// EXACTLY, and its classification agrees with ParseINI key for key. A drift in
// the second would make INIDoc rewrite lines the reader ignores.
func FuzzINIDocRoundTrip(f *testing.F) {
	for _, s := range iniDocFuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) { iniDocRoundTrip(t, raw) })
}

// iniDocFuzzSeeds are real INI shapes plus every degenerate line the two
// parsers have to agree on: BOMs, bare CR, mixed endings, a missing final
// newline, ';' before and after the '=', duplicate keys, an unterminated
// section header, and a section name with the index's own separator in it.
var iniDocFuzzSeeds = []string{
	"",
	"\n",
	"\r\n",
	"k=v",
	"k = v\n",
	"key = value with = sign\n",
	"; comment\n# also comment\nbroken-line\n[Sec]\nInner=2\n",
	"message = 16\nshowname_color = 10, 20, 30\n",
	"music_name = 1, 1, 134, 22  ; Relative to music_display.\n",
	"ooc_chat_message = 585, 324, 227, 23;492, 281, 222, 19\n",
	"clock_0_align = center;\n",
	"empty_after = ;justacomment\n",
	"foo;bar = 1\n",
	"[Sec] ; header with a note\ninner = 5\n",
	// Written as concatenation on purpose: a literal BOM in the source is
	// invisible in every editor and diff (ini.go's utf8BOM says the same).
	utf8BOM + "showname = 16\nmessage = 12\n",
	utf8BOM + "; Client size.\ncourtroom = 0, 0, 1072, 600\n",
	utf8BOM,
	"a = 1\r\nb = 2\nc = 3\r\n",
	"a = 1\rb = 2\n",
	"dupe = 1\ndupe = 2\n",
	"[",
	"[]",
	"=",
	"= noval",
	"nokey",
	"[a]\n[b]\nk=v",
	"[a/b]\nk = v\n",
	"   spaced   =   value   \n",
	"[a]\n\n\n[b]\n",
}

// iniDocRoundTrip is the shared body of the fuzz target and its seed test.
func iniDocRoundTrip(t *testing.T, raw string) {
	t.Helper()
	d, err := ParseINIDoc([]byte(raw))
	if err != nil {
		return // a cap refusal is a valid outcome
	}
	if got := string(d.Bytes()); got != raw {
		t.Fatalf("round trip changed the bytes:\n in: %q\nout: %q", raw, got)
	}
	assertReaderParity(t, "fuzz input", raw, d)
}

// assertReaderParity checks that the document agrees with ParseINI — the READ
// path — about every key and value in the source.
func assertReaderParity(t *testing.T, name, raw string, d *INIDoc) {
	t.Helper()
	ini, err := ParseINI(strings.NewReader(raw))
	if err != nil {
		return // bufio's own line-length limit is a valid outcome for the reader
	}
	if len(d.index) != ini.Len() {
		t.Fatalf("%s: document holds %d keys, reader holds %d — classification drifted",
			name, len(d.index), ini.Len())
	}
	for k, want := range ini.values {
		// Every stored key is section+iniSectionSep+key, and Get rejoins them
		// with the same separator, so ANY split point resolves to the same entry
		// (a section or key may itself contain a '/').
		sec, key, _ := strings.Cut(k, iniSectionSep)
		got, ok := d.Get(sec, key)
		if !ok {
			t.Fatalf("%s: reader has %q but the document does not", name, k)
		}
		if got != want {
			t.Fatalf("%s: %q = %q in the document, %q in the reader", name, k, got, want)
		}
	}
}

// readFixtureINI reads one file of the checked-in theme fixture.
func readFixtureINI(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join(aceattorney2xDir, name)
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("theme fixture %s is unreadable (%v). It is checked in at %s; "+
			"if it moved, repoint aceattorney2xDir rather than skipping the gate.", name, err, p)
	}
	return src
}

// firstDiff reports the index of the first differing byte, for a diagnostic that
// points at the line instead of dumping 10 KB.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// tail returns the last n bytes of s, for error messages.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
