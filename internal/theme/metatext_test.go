package theme

// THE METADATA-CAP RULING's gates (v1.90.0 W8).
//
// The ruling has two halves and both are load-bearing:
//   - a metadata value past its cap DEGRADES — the theme loads, the string is
//     shortened, a note says so;
//   - a structural value past its cap still REFUSES, naming the line.
//
// Every gate here DRIVES the shipped path (ParseSidecar / Sidecar.Bytes /
// Sidecar.Validate) rather than restating truncMetaRunes, because a mirror-test
// of a truncation is a second implementation of it and would stay green with the
// reader wired back to a refusal.

import (
	"regexp"
	"strings"
	"testing"
)

// metaDocRowMarker is the metadata row of §7's exception table, matched by its
// bold class name. The document is checked verbatim below.
const metaDocRowMarker = "| **Metadata** |"

// docBackticked matches one `identifier` inside a markdown cell.
var docBackticked = regexp.MustCompile("`([^`]+)`")

// overCap is a string one rune past NameRuneCap, in a MULTI-BYTE rune so a byte
// truncation anywhere in the chain would produce invalid UTF-8 and be caught.
func overCap(n int) string { return strings.Repeat("é", NameRuneCap+n) }

// TestMetadataCapsDegradeAndStructuralCapsRefuse is the ruling, both halves, in
// both directions.
func TestMetadataCapsDegradeAndStructuralCapsRefuse(t *testing.T) {
	t.Run("read: a long credit loads the theme", func(t *testing.T) {
		src := "[theme]\nformat = 1\ncredit = " + overCap(1) + "\n\n" +
			"[element.mark]\nkind = text\ntext = hello\n"
		s, err := ParseSidecar([]byte(src))
		if err != nil {
			t.Fatalf("a 241-rune credit refused the whole theme: %v\n"+
				"That is the exact silent-total-loss shape the ruling exists to kill: nothing resolves a "+
				"credit line, nothing paints it, and the user loses every element over a sentence.", err)
		}
		if len(s.Elements) != 1 {
			t.Fatalf("the theme loaded %d elements, want the one it declares", len(s.Elements))
		}
		if n := len([]rune(s.Meta.Credit)); n != NameRuneCap {
			t.Errorf("credit is %d runes after the degrade, want it shortened to %d", n, NameRuneCap)
		}
		if !strings.HasPrefix(s.Meta.Credit, "é") || strings.ContainsRune(s.Meta.Credit, '�') {
			t.Error("the truncation cut a multi-byte rune in half — it must cut by RUNES")
		}
		if !noteMentions(s, "credit") {
			t.Errorf("nothing in the import report names the shortened key.\n"+
				"Notes() = %q — design §3.3: name the offender, name the limit.", s.Notes())
		}
	})

	t.Run("read: a long base refuses, and says which key", func(t *testing.T) {
		src := "[theme]\nformat = 1\nbase = " + overCap(1) + "\n"
		_, err := ParseSidecar([]byte(src))
		if err == nil {
			t.Fatal("[theme] base past the cap loaded — base names the terminal AO2 fallback FOLDER, " +
				"so a shortened one resolves a different theme or none at all; that is a substitution, not a degrade")
		}
		if !strings.Contains(err.Error(), "base") {
			t.Errorf("the refusal is %q and does not name the key — an import report must say WHY", err)
		}
	})

	t.Run("write: a long credit in the MODEL still saves", func(t *testing.T) {
		s := parsedSidecar(t, "[theme]\nformat = 1\n")
		s.Meta.Credit = overCap(20)
		b, err := s.Bytes()
		if err != nil {
			t.Fatalf("Bytes refused a model with a long credit: %v — a preset, an import stamp or a "+
				"copy-for-editing provenance line must not be able to make a save fail", err)
		}
		if len(b) == 0 {
			t.Fatal("Bytes wrote nothing")
		}
		if n := len([]rune(s.Meta.Credit)); n != NameRuneCap {
			t.Errorf("the model still holds %d runes after the save — the degrade must land in the model", n)
		}
		if !noteMentions(s, "credit") {
			t.Errorf("the save shortened a value and said nothing; Notes() = %q", s.Notes())
		}
	})

	t.Run("write: Validate alone agrees with the save", func(t *testing.T) {
		// THE ONE GATE IN THIS PACKAGE THAT CALLS Validate DIRECTLY, and it is
		// deliberate: Validate is exported, and a verdict it gives that Bytes does not
		// is two rules wearing one name. It is what validateText's isMetaTextKey skip
		// is for, and without this the skip would be deletable with the suite green
		// (inside Bytes, degradeMeta has already run).
		s := parsedSidecar(t, "[theme]\nformat = 1\n")
		s.Meta.Credit = overCap(1)
		if err := s.Validate(); err != nil {
			t.Errorf("Validate refused a long credit (%v) while Bytes accepts one — "+
				"the guard and the save must give one answer", err)
		}
		s.Meta.Base = overCap(1)
		if err := s.Validate(); err == nil {
			t.Error("Validate accepted a long [theme] base — the structural half of the ruling is gone")
		}
	})
}

// TestMetadataDegradeNeverRewritesTheAuthorsLine is the half that keeps this from
// being the truncate-then-save data loss §7's caps block was written to prevent.
//
// The reader shortens the MODEL. The file must come back byte-identical, so a
// build with a larger cap reads the author's whole sentence.
func TestMetadataDegradeNeverRewritesTheAuthorsLine(t *testing.T) {
	long := overCap(60)
	src := "[theme]\nformat = 1\n; the author's own note\ncredit = " + long + "\ndescription = " + long + "\n"
	s := parsedSidecar(t, src)
	b, err := s.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "credit = "+long) {
		t.Errorf("the save rewrote the author's credit line to the shortened value.\n"+
			"§7: a value the reader degraded already MEANS the degraded thing, so the writer must leave "+
			"the line alone — canonMetaFree/canonMetaPlain are what make setKey's comparison come out equal.\n"+
			"got:\n%s", out)
	}
	if !strings.Contains(out, "description = "+long) {
		t.Error("the description line was rewritten too")
	}
	// And the round trip is stable: reading the saved bytes back gives the same
	// degraded model and the same file again.
	again := parsedSidecar(t, out)
	if again.Meta.Credit != s.Meta.Credit {
		t.Error("the second read produced a different credit — the degrade is not idempotent")
	}
	b2, err := again.Bytes()
	if err != nil {
		t.Fatalf("second Bytes: %v", err)
	}
	if string(b2) != out {
		t.Error("save → load → save is not byte-stable for a degraded metadata value")
	}
}

// TestEveryMetadataFieldDegradesThroughTheOneTable drives ALL of metaTextFields
// at once: a file with every metadata key over the cap must load, shorten every
// one of them, and report each by name.
//
// It is the seam's encapsulation gate. A field dropped from metaTextFields, or a
// reader assignment that quietly goes back to the refusing helper, fails here and
// cannot be argued away as an oversight in one key.
func TestEveryMetadataFieldDegradesThroughTheOneTable(t *testing.T) {
	long := overCap(3)
	var b strings.Builder
	b.WriteString("[theme]\nformat = 1\n")
	for _, f := range metaTextFields {
		if f.section == secTheme {
			b.WriteString(f.key + " = " + long + "\n")
		}
	}
	b.WriteString("\n[import]\n")
	for _, f := range metaTextFields {
		if f.section == secImport {
			b.WriteString(f.key + " = " + long + "\n")
		}
	}
	s, err := ParseSidecar([]byte(b.String()))
	if err != nil {
		t.Fatalf("a file whose every metadata value is long refused to load: %v", err)
	}
	for _, f := range metaTextFields {
		got := *f.ptr(s)
		if n := len([]rune(got)); n != NameRuneCap {
			t.Errorf("[%s] %s is %d runes after the parse, want %d — it is not going through degradeMeta",
				f.section, f.key, n, NameRuneCap)
		}
		if !noteMentions(s, f.key) {
			t.Errorf("[%s] %s was shortened with no note", f.section, f.key)
		}
	}
}

// TestTheDocumentPublishesExactlyTheMetadataClass pins docs/THEME-FORMAT.md's
// metadata row against metaTextFields, in BOTH directions.
//
// The spec is written so a third party can implement a reader without this
// source, so "which keys degrade" is a promise; a promise nothing checks drifts,
// and a third-party writer would then emit a file we refuse for a key our own
// document says is safe.
func TestTheDocumentPublishesExactlyTheMetadataClass(t *testing.T) {
	doc := readFormatDoc(t)
	var row string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), metaDocRowMarker) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("%s no longer publishes a row starting %q — the ruling has to be normative "+
			"somewhere a third party can read it", themeFormatDocPath, metaDocRowMarker)
	}
	published := map[string]bool{}
	for _, m := range docBackticked.FindAllStringSubmatch(row, -1) {
		published[m[1]] = true
	}
	// The two section labels are spelled in the same cell; they are not keys.
	for _, sec := range []string{"[" + secTheme + "]", "[" + secImport + "]"} {
		if !published[sec] {
			t.Errorf("the metadata row does not name the section %s", sec)
		}
		delete(published, sec)
	}
	for _, f := range metaTextFields {
		if !published[f.key] {
			t.Errorf("metaTextFields degrades [%s] %s but %s does not publish it",
				f.section, f.key, themeFormatDocPath)
		}
		delete(published, f.key)
	}
	for k := range published {
		t.Errorf("%s publishes %q as metadata, but metaTextFields does not degrade it — "+
			"the document promises a load this build refuses", themeFormatDocPath, k)
	}
}

// noteMentions reports whether any degrade note names key.
func noteMentions(s *Sidecar, key string) bool {
	for _, n := range s.Notes() {
		if strings.Contains(n, key) {
			return true
		}
	}
	return false
}

// parsedSidecar parses src or fails the test.
func parsedSidecar(t *testing.T, src string) *Sidecar {
	t.Helper()
	s, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatalf("ParseSidecar(%q): %v", src, err)
	}
	return s
}
