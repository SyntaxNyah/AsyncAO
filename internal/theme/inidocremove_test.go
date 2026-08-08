package theme

// THE SECTION DELETE (v1.90.0 W9). One concern: INIDoc.RemoveSection, driven
// directly, at the line level. What the delete is FOR — a preset tier replace
// that no longer leaves the previous tier in the file — is gated where the act
// lives (preset_test.go, themegallery_test.go); this file is the primitive.

import (
	"strings"
	"testing"
)

// removeFixture is three sections, comment-dominated, in the shape a
// hand-authored sidecar actually has: prose above each header, a trailing
// comment introducing the NEXT section, and a section opened twice.
const removeFixture = `; the file's own preamble
format = 1

; ---- the first element ----
[element.keep_me]
kind = shape   ; with a trailing comment
fill = #ff0000

; ---- the doomed element ----
[element.drop_me]
kind = gen
generator = noise

; ---- and this comment introduces the LAST section ----
[element.keep_me_too]
kind = text
`

func mustParseDoc(t *testing.T, src string) *INIDoc {
	t.Helper()
	d, err := ParseINIDoc([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

// TestINIDocRemoveSectionTakesTheHeaderAndItsKeys is the primitive's whole point.
// A section the model has dropped must stop being in the file, or the next read
// hands it straight back.
func TestINIDocRemoveSectionTakesTheHeaderAndItsKeys(t *testing.T) {
	d := mustParseDoc(t, removeFixture)
	if err := d.RemoveSection("element.drop_me"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out := string(d.Bytes())
	for _, gone := range []string{"[element.drop_me]", "generator = noise"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q survived the removal:\n%s", gone, out)
		}
	}
	// The neighbours are untouched, comments and all — a delete that rewrote the
	// rest of the file would be the data loss this type exists to prevent.
	for _, kept := range []string{
		"; the file's own preamble", "format = 1",
		"[element.keep_me]", "kind = shape   ; with a trailing comment", "fill = #ff0000",
		"[element.keep_me_too]", "kind = text",
	} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q did not survive the removal:\n%s", kept, out)
		}
	}
	// And the document's own view agrees with what a reader makes of the bytes it
	// just emitted. Without this the index could still hold the removed keys and
	// every later Set would target a line that no longer exists.
	assertReaderParity(t, "after remove", out, d)
	if _, still := d.Get("element.drop_me", "generator"); still {
		t.Error("the document still resolves a key of the section it removed")
	}
}

// TestINIDocRemoveSectionKeepsTheProseThatIntroducesTheNextSection pins the one
// judgement call in the delete. A comment between a section's last key and the
// next header is as likely to introduce the section BELOW as to footnote the one
// above (sectionEnd's comment accepts the same ambiguity from the other side), so
// the delete gives it back rather than guessing — and then re-parents it, or the
// next key written into the removed section would land with no header at all.
func TestINIDocRemoveSectionKeepsTheProseThatIntroducesTheNextSection(t *testing.T) {
	d := mustParseDoc(t, removeFixture)
	if err := d.RemoveSection("element.drop_me"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	const intro = "; ---- and this comment introduces the LAST section ----"
	if !strings.Contains(string(d.Bytes()), intro) {
		t.Fatalf("the delete ate the author's introduction to the next section:\n%s", d.Bytes())
	}

	// THE RE-PARENT, proved through the writer rather than by reading a field: put
	// the section back and it must come back with a header of its own.
	if err := d.Set("element.drop_me", "kind", "shape"); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	out := string(d.Bytes())
	if !strings.Contains(out, "[element.drop_me]") {
		t.Fatalf("a key written into a removed section got no header — every key under it "+
			"silently reparents into whatever section precedes it:\n%s", out)
	}
	back, err := ParseSidecar([]byte(out))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if _, ok := back.FindElement("drop_me"); !ok {
		t.Fatal("the re-added element does not read back — its key landed outside its own section")
	}
	if _, ok := back.FindElement("keep_me_too"); !ok {
		t.Fatal("the section after the removed one stopped reading back")
	}
}

// TestINIDocRemoveSectionTakesEveryRunOfARepeatedSection: the reader treats two
// runs of one section as one section, so a delete that stopped at the first
// header would leave the rest of the keys standing under nothing.
func TestINIDocRemoveSectionTakesEveryRunOfARepeatedSection(t *testing.T) {
	d := mustParseDoc(t, "[a]\nx = 1\n[b]\ny = 2\n[a]\nz = 3\n")
	if err := d.RemoveSection("a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out := string(d.Bytes())
	if strings.Contains(out, "[a]") || strings.Contains(out, "x = 1") || strings.Contains(out, "z = 3") {
		t.Fatalf("a second run of the section survived:\n%s", out)
	}
	if !strings.Contains(out, "[b]") || !strings.Contains(out, "y = 2") {
		t.Fatalf("the section between the two runs was taken with them:\n%s", out)
	}
}

// TestINIDocRemoveSectionKeepsTheMissingFinalNewline: deleting the last section
// of a file that never ended with a newline must not invent one, exactly as
// inserting past the end must not (insertLine's own rule, from the other side).
func TestINIDocRemoveSectionKeepsTheMissingFinalNewline(t *testing.T) {
	d := mustParseDoc(t, "[a]\nx = 1\n[b]\ny = 2")
	if err := d.RemoveSection("b"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := string(d.Bytes()); got != "[a]\nx = 1" {
		t.Fatalf("emitted %q, want %q", got, "[a]\nx = 1")
	}
}

// TestINIDocRemoveSectionRefusesWhatItCannotMean is the primitive's fence. The
// ROOT section has no header to remove, so "removing" it could only mean deleting
// every key above the first header — and an absent section is not a failure at
// all, which is the case a caller relies on for an element authored this session.
func TestINIDocRemoveSectionRefusesWhatItCannotMean(t *testing.T) {
	d := mustParseDoc(t, removeFixture)
	for _, c := range []struct {
		name string
		want error
		why  string
	}{
		{"", ErrINIDocBadKey, "the root section has no header, so there is nothing to delete"},
		{"  ", ErrINIDocBadKey, "whitespace is the root section spelled differently"},
		{"a=b", ErrINIDocBadKey, "a name the reader could not read back is not a section"},
		{"element.never_existed", ErrINIDocNoSection, "nothing to delete is not a failure"},
	} {
		if got := d.RemoveSection(c.name); got != c.want {
			t.Errorf("RemoveSection(%q) = %v, want %v — %s", c.name, got, c.want, c.why)
		}
	}
	// And none of the refusals touched anything.
	if got := string(d.Bytes()); got != removeFixture {
		t.Errorf("a refused removal changed the file:\n%s", got)
	}
}

// TestINIDocRemoveSectionMovesPendingEdits pins the bookkeeping that is invisible
// until it is wrong: `touched` is keyed by LINE NUMBER, so a removal that did not
// renumber it would splice a pending value into whatever line took the index.
func TestINIDocRemoveSectionMovesPendingEdits(t *testing.T) {
	d := mustParseDoc(t, "[a]\nx = 1\n[b]\ny = 2\n[c]\nz = 3\n")
	if err := d.Set("c", "z", "99"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := d.RemoveSection("a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	want := "[b]\ny = 2\n[c]\nz = 99\n"
	if got := string(d.Bytes()); got != want {
		t.Fatalf("emitted %q, want %q — the pending edit did not move with its line", got, want)
	}
}
