package theme

// The WRITE GUARD's gates.
//
// They drive Sidecar.Bytes(), never Sidecar.Validate() directly — the guard is
// only worth anything if it is on the path the editor actually takes, and a
// gate that called Validate() itself would leave the `if err := s.Validate()`
// line in Bytes deletable with the suite green. That is the exact shape rule 11
// was written about.

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sidecarRefusalCase is ONE class of model the writer must refuse: mutate a
// legal, parsed sidecar into it, and Bytes() must fail before it touches a byte.
type sidecarRefusalCase struct {
	name string
	want error
	// build mutates a freshly parsed, legal sidecar into the refusal class.
	build func(*Sidecar)
	// why names the damage a green save would have done.
	why string
}

// TestBytesRefusesAModelTheReaderWouldRefuse is the W7 prerequisite stated as a
// test: for every class of file ParseSidecar refuses or silently drops, the
// WRITER refuses the equivalent MODEL before it touches a byte.
//
// Without it the editor bricks a theme at save time, silently: an over-cap
// collection or one over-long watermark makes the whole theme refuse to load
// the next time it is opened, and a duplicate id makes the editor edit one
// element and save another.
//
// The table below carries the id, uniqueness and per-STRING cap classes by hand.
// The COLLECTION-cap classes are appended from sidecarCapRows() instead of being
// written out, because writing them out is what failed: four of the eleven were
// listed, the other seven were not, and validateCaps' seven matching rules were
// therefore deletable with this package green. Deriving them makes an omission
// impossible on either side — see sidecar_caps_test.go.
func TestBytesRefusesAModelTheReaderWouldRefuse(t *testing.T) {
	cases := []sidecarRefusalCase{
		{
			name:  "element id outside the grammar",
			want:  ErrSidecarInvalid,
			build: func(s *Sidecar) { s.Elements = append(s.Elements, Element{ID: "my plate", Kind: ElemText, Text: "x"}) },
			why:   "[element.my plate] is skipped on the next load, so the element is gone from the model and still in the file",
		},
		{
			name:  "element with no id at all",
			want:  ErrSidecarInvalid,
			build: func(s *Sidecar) { s.Elements = append(s.Elements, Element{Kind: ElemText, Text: "x"}) },
			why:   "[element.] is not a section any reader resolves",
		},
		{
			name: "two elements sharing one id",
			want: ErrSidecarInvalid,
			build: func(s *Sidecar) {
				s.Elements = append(s.Elements,
					Element{ID: "dup", Kind: ElemText, Text: "first"},
					Element{ID: "dup", Kind: ElemText, Text: "second"})
			},
			why: "one section cannot carry two elements: FindElement returns the first, the writer saves the last",
		},
		{
			name: "two panes sharing one id",
			want: ErrSidecarInvalid,
			build: func(s *Sidecar) {
				s.Panes = append(s.Panes, Pane{ID: "split"}, Pane{ID: "split"})
			},
			why: "same collision, same silent overwrite",
		},
		{
			name:  "media id outside the grammar",
			want:  ErrSidecarInvalid,
			build: func(s *Sidecar) { s.Media = append(s.Media, MediaEntry{ID: "Big Plate", File: "a.png"}) },
			why:   "readMedia skips the row and notes it, so the element pointing at it resolves nothing",
		},
		{
			name:  "effect target with no widget key at all",
			want:  ErrSidecarInvalid,
			build: func(s *Sidecar) { s.Effects = append(s.Effects, EffectBind{Effect: FXGlow, AmpPct: 40}) },
			why:   "[effect.] is preserved and ignored on the next load, so the binding is gone from the model and still in the file",
		},
		{
			name: "effect target past AnchorRuneCap",
			want: ErrSidecarInvalid,
			build: func(s *Sidecar) {
				s.Effects = append(s.Effects, EffectBind{Target: strings.Repeat("w", AnchorRuneCap+1), Effect: FXGlow, AmpPct: 40})
			},
			why: "readEffectBind drops a target past the widget-key bound exactly as it drops an empty one",
		},
		{
			name: "two effects binding one widget",
			want: ErrSidecarInvalid,
			build: func(s *Sidecar) {
				s.Effects = append(s.Effects,
					EffectBind{Target: "viewport", Effect: FXGlow, PeriodMs: 900, AmpPct: 40},
					EffectBind{Target: "viewport", Effect: FXPulse, PeriodMs: 300, AmpPct: 10})
			},
			why: "one [effect.viewport] cannot carry two binds: EffectFor returns the first, the writer saves the last — the editor edits one effect and saves another",
		},
		{
			name:  "clock spelled as another clock",
			want:  ErrSidecarInvalid,
			build: func(s *Sidecar) { s.Clocks[3] = ClockSpec{ID: "7", SpeedPct: 200, Declared: true} },
			why:   "[clock.7] reads back as group 7: the whole group moves on a save nobody asked for",
		},
		{
			name: "watermark past TextRuneCap",
			want: ErrSidecarCap,
			build: func(s *Sidecar) {
				s.Elements = append(s.Elements, Element{ID: "mark", Kind: ElemText, Text: strings.Repeat("a", TextRuneCap+1)})
			},
			why: "ONE over-long line and the theme stops loading — the headline W7 hazard",
		},
		{
			name:  "theme name past NameRuneCap",
			want:  ErrSidecarCap,
			build: func(s *Sidecar) { s.Meta.Name = strings.Repeat("n", NameRuneCap+1) },
			why:   "[theme] name is length-bounded on read; the writer must not emit past it",
		},
		{
			name: "anchor past AnchorRuneCap",
			want: ErrSidecarCap,
			build: func(s *Sidecar) {
				s.Elements = append(s.Elements, Element{ID: "anch", Kind: ElemText, Text: "x", Anchor: strings.Repeat("a", AnchorRuneCap+1)})
			},
			why: "readElement bounds the anchor separately from every other string",
		},
		{
			name: "visible_when value past ConditionValueRuneCap",
			want: ErrSidecarCap,
			build: func(s *Sidecar) {
				s.Elements = append(s.Elements, Element{ID: "cond", Kind: ElemText, Text: "x",
					VisibleWhen: Condition{Axis: CondChar, Value: strings.Repeat("c", ConditionValueRuneCap+1)}})
			},
			why: "conditionOf refuses it on read",
		},
		{
			name: "generator parameter past GenParamRuneCap",
			want: ErrSidecarCap,
			build: func(s *Sidecar) {
				e := Element{ID: "gen", Kind: ElemGen, Gen: "scanlines"}
				e.GenParams[0] = KV{Key: "pitch", Value: strings.Repeat("9", GenParamRuneCap+1)}
				s.Elements = append(s.Elements, e)
			},
			why: "genParamsOf refuses it on read",
		},
	}
	// One derived row per capped COLLECTION — the eleven of sidecarCapRows(),
	// each grown one entry past its own named cap. The seven that were missing
	// here ([rotations], [fonts], [fontbind], [sounds], [pane.*], [effect.*],
	// [import] subthemes) are the collections a theme editor authors most, and
	// their absence made the matching rules in validateCaps deletable green.
	for _, row := range sidecarCapRows() {
		cases = append(cases, sidecarRefusalCase{
			name:  row.what + " past its cap",
			want:  ErrSidecarCap,
			build: func(s *Sidecar) { row.fill(s, row.limit+1) },
			why: "the next load is ErrSidecarCap — the WHOLE theme refuses, which is the data loss the caps " +
				"block exists to prevent",
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := legalSidecar(t)
			before, err := s.Bytes()
			if err != nil {
				t.Fatalf("the legal baseline does not even save: %v", err)
			}
			tc.build(s)
			out, err := s.Bytes()
			if err == nil {
				t.Fatalf("Bytes() wrote %d bytes for a model the reader refuses — %s", len(out), tc.why)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("Bytes() = %v, want a %v — the class decides what the user is told to fix", err, tc.want)
			}
			if out != nil {
				t.Errorf("Bytes() returned %d bytes ALONGSIDE its refusal; a caller that ignores err would write them", len(out))
			}
			// The refusal must not have half-edited the document underneath: a
			// guard that ran after the first section emitter would leave the
			// model's other edits on disk if the caller retried.
			if after := s.rawDocBytes(); string(after) != string(before) {
				t.Errorf("the refused save still mutated the document:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// rawDocBytes serialises the lossless document WITHOUT the model, so the gate
// above can ask "did the refused save leave the file alone?" without going
// through the guard it is testing.
//
// TEST-ONLY, and on a _test.go file rather than on the type: the whole point of
// the guard is that there is no unvalidated way out, and this method is the
// shape that breaks it. When this comment was first written the claim was
// FALSE — Sidecar.Doc() handed the same document to anybody — so it is now
// carried by TestNoExportedPathWritesASidecarWithoutTheGuard (below) rather than
// by a promise in a comment.
func (s *Sidecar) rawDocBytes() []byte {
	if s == nil || s.doc == nil {
		return nil
	}
	return s.doc.Bytes()
}

// elemID builds a legal, unique id for the cap fixtures.
func elemID(i int) string {
	return "e" + formatInt(i)
}

// legalSidecar is a small, REAL parsed sidecar — the baseline every mutation
// above is applied to, so each case differs from a passing save by exactly the
// one thing it names.
func legalSidecar(t *testing.T) *Sidecar {
	t.Helper()
	s, err := ParseSidecar([]byte(strings.Join([]string{
		"[theme]",
		"format = 1",
		"name = Paper Lantern",
		"",
		"[element.plate]",
		"kind = gradient",
		"z = 4",
		"",
	}, "\n")))
	if err != nil {
		t.Fatalf("the baseline fixture does not parse: %v", err)
	}
	return s
}

// TestTheWriterRefusesExactlyWhatTheReaderRefuses is the ONE-SOURCE evidence: for
// the cap classes, the same over-cap collection is put to BOTH directions of the
// production code — the model through Bytes(), the equivalent FILE through
// ParseSidecar — and the two must agree, with the same sentinel.
//
// It is not a mirror: neither side restates a limit, both sides are called.
func TestTheWriterRefusesExactlyWhatTheReaderRefuses(t *testing.T) {
	// One [overrides] row per line, one past the cap.
	var b strings.Builder
	b.WriteString("[theme]\nformat = 1\n\n[overrides]\n")
	for i := 0; i <= OverrideCap; i++ {
		b.WriteString(elemID(i) + " = 1, 2, 3, 4\n")
	}
	if _, err := ParseSidecar([]byte(b.String())); !errors.Is(err, ErrSidecarCap) {
		t.Fatalf("the READER accepted %d overrides (cap %d): err = %v", OverrideCap+1, OverrideCap, err)
	}

	s := legalSidecar(t)
	for i := 0; i <= OverrideCap; i++ {
		s.Overrides = append(s.Overrides, KeyRect{Key: elemID(i), Rect: Rect{1, 2, 3, 4}})
	}
	if _, err := s.Bytes(); !errors.Is(err, ErrSidecarCap) {
		t.Fatalf("the WRITER accepted %d overrides the reader refuses: err = %v — that file could never be opened again", OverrideCap+1, err)
	}
}

// TestTheEffectTargetRuleHasOneSpelling is the ONE-SOURCE evidence for the
// [effect.<target>] family, whose section suffix is a WIDGET KEY rather than an
// id and so is the one family checkSectionID cannot speak for.
//
// The same three targets are put to BOTH directions of the production code — a
// FILE through ParseSidecar, the equivalent MODEL through Bytes() — and the two
// must agree on every one of them. Neither side restates the rule: they call
// validEffectTarget, once each, so forking it (or deleting either call) turns
// this red. Deleting the reader's call makes it accept a bind it can never
// resolve; deleting the writer's puts the editor back to saving effects that
// vanish on the next load.
func TestTheEffectTargetRuleHasOneSpelling(t *testing.T) {
	const legalTarget = "viewport"
	atTheBound := strings.Repeat("w", AnchorRuneCap) // the last target both sides must keep
	for _, tc := range []struct {
		name   string
		target string
		want   bool // does the binding survive a load, and may the writer emit it?
	}{
		{"no widget key", "", false},
		{"one rune past the widget-key bound", strings.Repeat("w", AnchorRuneCap+1), false},
		{"exactly at the widget-key bound", atTheBound, true},
		{"an ordinary widget key", legalTarget, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The READER's answer, taken from a real file.
			src := "[theme]\nformat = 1\n\n[" + secEffectPrefix + tc.target + "]\neffect = glow\namp_pct = 40\n"
			parsed, err := ParseSidecar([]byte(src))
			if err != nil {
				t.Fatalf("ParseSidecar refused the whole file over one section: %v", err)
			}
			_, kept := parsed.EffectFor(tc.target)
			if kept != tc.want {
				t.Fatalf("ParseSidecar kept the [%s%s] binding = %v, want %v", secEffectPrefix, tc.target, kept, tc.want)
			}

			// The WRITER's answer, taken from the equivalent model.
			s := legalSidecar(t)
			s.Effects = append(s.Effects, EffectBind{Target: tc.target, Effect: FXGlow, AmpPct: 40})
			out, err := s.Bytes()
			switch {
			case tc.want && err != nil:
				t.Fatalf("the reader keeps [%s%s] but the writer refuses to emit it: %v — the guard is stricter than the reader",
					secEffectPrefix, tc.target, err)
			case !tc.want && err == nil:
				t.Fatalf("the writer emitted %d bytes for [%s%s], which the reader drops — that effect disappears the next time the theme is opened",
					len(out), secEffectPrefix, tc.target)
			case !tc.want && !errors.Is(err, ErrSidecarInvalid):
				t.Fatalf("Bytes() = %v, want %v — the class decides what the author is told to fix", err, ErrSidecarInvalid)
			}
		})
	}
}

// TestTheGuardIsNeverStricterThanTheReader is the open–closed half stated
// directly: ANYTHING ParseSidecar accepts, Bytes() must still write.
//
// It walks the whole shipped fixture corpus rather than a hand-built model,
// because those files are what the reader was built against — the alien
// spellings, the alias keys, the future-format file and the vendor sections all
// live there. TestHandAuthoredFixturesRoundTripByteIdentical is the other half
// and is UNCHANGED by this wave, which is the evidence that the guard added a
// refusal and moved nothing else.
func TestTheGuardIsNeverStricterThanTheReader(t *testing.T) {
	dirs, err := os.ReadDir(themesFixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		src, err := os.ReadFile(filepath.Join(themesFixtureDir, d.Name(), SidecarFileName))
		if err != nil {
			continue // a fixture theme with no sidecar is a stock AO2 theme
		}
		s, err := ParseSidecar(src)
		if err != nil {
			continue // a refusal fixture is somebody else's gate
		}
		seen++
		if _, err := s.Bytes(); err != nil {
			t.Errorf("%s parses but the write guard refuses to save it: %v — the guard is stricter than the reader, which makes a legal theme unopenable in the editor",
				d.Name(), err)
		}
	}
	if seen == 0 {
		t.Fatal("no fixture sidecar was exercised — this gate is vacuous; point it at where the corpus moved")
	}
}

// TestNewSidecarSavesCleanly keeps the from-scratch authoring path open: the
// empty model the editor starts from must pass its own guard, or W7's "New
// theme" button fails on its first save.
func TestNewSidecarSavesCleanly(t *testing.T) {
	s := NewSidecar()
	if err := s.Validate(); err != nil {
		t.Fatalf("a fresh NewSidecar does not validate: %v", err)
	}
	if _, err := s.Bytes(); err != nil {
		t.Fatalf("a fresh NewSidecar does not save: %v", err)
	}
	// And a nil sidecar — the stock-AO2-theme case — is trivially valid, so no
	// caller needs a branch.
	var nilSC *Sidecar
	if err := nilSC.Validate(); err != nil {
		t.Errorf("a nil *Sidecar must validate (it is the stock AO2 theme): %v", err)
	}
}

// ---------------------------------------------------------------------------
// The guard's REACH: an API census (ledger L32)
// ---------------------------------------------------------------------------

// iniDocProducers names every exported symbol allowed to hand back an *INIDoc,
// and why each is not a way around the write guard.
//
// It is deliberately an ALLOW-LIST rather than a rule about names: the property
// that matters is "can a caller obtain a document a Sidecar is going to write?",
// and that is a fact about the seam's PURPOSE, which only a human can state.
// W7's undo ring and autosave extend this map, with their reason, in the wave
// that adds their call sites.
var iniDocProducers = map[string]string{
	"ParseINIDoc": "builds a NEW document out of bytes the caller already had. " +
		"No Sidecar is reachable from it, so what it returns is nobody's live document " +
		"and writing it back is writing what you already have",
}

// sidecarGuardCalls are the two names a byte-emitting path may reach the guard
// through: Validate itself, or Bytes (which is the guard's own front door).
var sidecarGuardCalls = map[string]bool{"Validate": true, "Bytes": true}

// TestNoExportedPathWritesASidecarWithoutTheGuard is the census behind Bytes'
// claim that "there is no exported path around it".
//
// THE CLAIM WAS FALSE. Sidecar.Doc() returned the LIVE *INIDoc, whose Set, Bytes
// and Write are all exported — so os.WriteFile(p, sc.Doc().Bytes(), 0o644) wrote
// a sidecar the guard never saw, and sc.Doc().Set(…) was a second writer with no
// arbitration against the model. The comment asserting otherwise sat two lines
// above the guard, and a second copy of it sat in this file. Both survived
// review because nothing executed them. This does.
//
// TWO HALVES, both structural, because the failure is structural:
//
//   - NOBODY HANDS OUT THE DOCUMENT. An exported symbol may return *INIDoc only
//     if it is on iniDocProducers, and no such symbol may have a Sidecar in
//     reach (receiver or parameter) — that is what makes it "a new document"
//     rather than "somebody's live one". Exported *INIDoc struct FIELDS are
//     refused outright: a field needs no accessor to be assigned through. So are
//     exported package-level VARS and CONSTS — the arm the census was missing
//     entirely. `var Scratch = &INIDoc{}` or `var Docs []*INIDoc` needs neither a
//     method nor a struct to hand every importer a writable document with a
//     public Set/Bytes/Write on it, and it is the cheapest possible way to
//     reintroduce Doc() under a name this file had no opinion about.
//   - EVERY BYTE-EMITTING PATH GOES THROUGH THE GUARD. Any exported function or
//     method with a Sidecar in reach that returns []byte or takes an io.Writer
//     must call Validate or Bytes. Sidecar.Write is exactly that shape and
//     satisfies it by delegating; a third emitter that formatted the model
//     itself would not.
//
// A source census rather than a behavioural test because the thing being pinned
// is the ABSENCE of an API: no value can be constructed to prove a method that
// does not exist stays that way.
func TestNoExportedPathWritesASidecarWithoutTheGuard(t *testing.T) {
	fset, sources := parsePackageSources(t)
	var emitters []string
	for _, file := range sources {
		// Half one, the field case: an exported *INIDoc field is a writable
		// document with no method to look for.
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || !spec.Name.IsExported() {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, f := range st.Fields.List {
				if !isINIDocPtr(f.Type) {
					continue
				}
				for _, fieldName := range f.Names {
					if fieldName.IsExported() {
						t.Errorf("%s.%s is an exported *INIDoc field (%s) — anything holding the struct can "+
							"Set and Bytes the document straight past the write guard",
							spec.Name.Name, fieldName.Name, fset.Position(f.Pos()))
					}
				}
			}
			return true
		})

		// Half one, the package-level VAR / CONST case: no accessor needed at all.
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				exported := false
				for _, n := range vs.Names {
					if n.IsExported() {
						exported = true
					}
				}
				if !exported {
					continue
				}
				hands := vs.Type != nil && mentionsINIDocPtr(vs.Type)
				for _, v := range vs.Values {
					if makesINIDoc(v) {
						hands = true
					}
				}
				if hands {
					t.Errorf("%s (%s) is an exported package-level *INIDoc — INIDoc.Set, Bytes and Write are "+
						"exported, so every importer can write the document straight past the guard, with no "+
						"method for the accessor census above to see. If a wave needs the document, add the "+
						"narrow path its call sites need and list it in iniDocProducers with the reason",
						vs.Names[0].Name, fset.Position(vs.Pos()))
				}
			}
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || !fn.Name.IsExported() {
				continue
			}
			sidecarInReach := funcHasSidecarInReach(fn)

			// Half one, the accessor case.
			if funcReturns(fn, isINIDocPtr) {
				why, allowed := iniDocProducers[fn.Name.Name]
				switch {
				case !allowed:
					t.Errorf("%s (%s) returns a writable *INIDoc — INIDoc.Set, Bytes and Write are exported, so "+
						"this is a save path with no Validate in it and a second writer against the model. "+
						"That is exactly what Sidecar.Doc() was. If a wave needs the document, add the narrow "+
						"path its call sites need and list it in iniDocProducers with the reason",
						fn.Name.Name, fset.Position(fn.Pos()))
				case sidecarInReach:
					t.Errorf("%s (%s) is allow-listed as an *INIDoc producer (%s) but now has a Sidecar in reach — "+
						"the allowance was for a document nobody else owns, and this hands out a LIVE one",
						fn.Name.Name, fset.Position(fn.Pos()), why)
				}
			}

			// Half two: byte emitters that can see a Sidecar.
			if !sidecarInReach || !(funcReturns(fn, isByteSlice) || funcTakes(fn, isIOWriter)) {
				continue
			}
			emitters = append(emitters, fn.Name.Name)
			if !callsAnyOn(fn.Body, sidecarIdents(fn), sidecarGuardCalls) {
				t.Errorf("%s (%s) emits sidecar bytes without calling Validate or Bytes — an editor holding a live "+
					"model can write a file the reader then refuses, and the failure is silent at the moment of saving",
					fn.Name.Name, fset.Position(fn.Pos()))
			}
		}
	}

	// Non-vacuity: the census must have FOUND the emitters it claims to be
	// checking (parsePackageSources already refuses an empty scan). A census that
	// matched nothing would pass a package with the guard deleted.
	sort.Strings(emitters)
	const wantEmitters = "Bytes,Write"
	if got := strings.Join(emitters, ","); got != wantEmitters {
		t.Fatalf("the exported byte-emitting paths with a Sidecar in reach are [%s], want [%s] — a new one is "+
			"fine, but add it here so this gate is known to be covering it (and zero means the census stopped "+
			"matching anything at all)", got, wantEmitters)
	}
	// And the front door really is the guard: Bytes must call Validate by name,
	// not merely be on the list above (Write satisfies the census by delegating to
	// Bytes, so without this the pair could satisfy each other).
	if !strings.Contains(funcBodySource(t, "func (s *Sidecar) Bytes("), "s.Validate()") {
		t.Error("Sidecar.Bytes no longer calls s.Validate() — every other gate in this file drives Bytes, so " +
			"the guard would be deleted with the whole suite green")
	}
}

// parsePackageSources parses every PRODUCTION file in the package directory —
// the one place the census walk is written, shared by this file's API census and
// by the emitter census in sidecar_sections_test.go.
//
// By directory rather than by filename on purpose: which file a function lives
// in is not part of what any census here claims, so splitting sidecar_write.go
// (it is the package's fattest file) must stay a pure move.
//
// An empty scan is a FAILURE, not an empty pass: a census that read nothing
// passes everything.
func parsePackageSources(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("the scan found no production files — every census built on it would be vacuous")
	}
	return fset, files
}

// isINIDocPtr reports the type expression *INIDoc, which is the only spelling
// the package uses (INIDoc is never passed by value — it owns maps).
func isINIDocPtr(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "INIDoc"
}

// isSidecar reports *Sidecar or Sidecar: both put the model in reach.
func isSidecar(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "Sidecar"
}

func isByteSlice(expr ast.Expr) bool {
	arr, ok := expr.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	id, ok := arr.Elt.(*ast.Ident)
	return ok && id.Name == "byte"
}

func isIOWriter(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "Writer" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "io"
}

// funcHasSidecarInReach reports a Sidecar arriving as the receiver or as a
// parameter — the two ways a body can be holding one.
func funcHasSidecarInReach(fn *ast.FuncDecl) bool {
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			if isSidecar(f.Type) {
				return true
			}
		}
	}
	return funcTakes(fn, isSidecar)
}

func funcTakes(fn *ast.FuncDecl, match func(ast.Expr) bool) bool {
	if fn.Type == nil || fn.Type.Params == nil {
		return false
	}
	for _, f := range fn.Type.Params.List {
		if match(f.Type) {
			return true
		}
	}
	return false
}

func funcReturns(fn *ast.FuncDecl, match func(ast.Expr) bool) bool {
	if fn.Type == nil || fn.Type.Results == nil {
		return false
	}
	for _, f := range fn.Type.Results.List {
		if match(f.Type) {
			return true
		}
	}
	return false
}

// sidecarIdents names the local variables in a function that ARE a Sidecar: its
// receiver, and every parameter of Sidecar (or *Sidecar) type.
//
// It is the second half of callsAnyOn's question. "Which names is the guard
// allowed to hang off" cannot be answered by the call site alone, and answering
// it with "any name" is what let s.doc.Bytes() pass for a guard call.
func sidecarIdents(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	add := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			if !isSidecar(f.Type) {
				continue
			}
			for _, n := range f.Names {
				if n != nil {
					out[n.Name] = true
				}
			}
		}
	}
	if fn != nil {
		add(fn.Recv)
		if fn.Type != nil {
			add(fn.Type.Params)
		}
	}
	return out
}

// callsAnyOn reports a call to any of the named methods ON A SIDECAR anywhere in
// the body, including inside a closure — a guard reached through a deferred or
// nested call is still reached.
//
// THE RECEIVER IS PART OF THE QUESTION, and matching on the method name alone was
// a hole big enough to drive the whole defect back through. `s.doc.Bytes()` is a
// call to a selector named Bytes — so a probe method that reached PAST the model
// into the lossless document, which is precisely the bypass
// TestNoExportedPathWritesASidecarWithoutTheGuard exists to forbid, satisfied the
// guard census by spelling. Only a call rooted on a name that IS a Sidecar counts:
// s.Validate() and s.Bytes() pass, s.doc.Bytes() and d.Bytes() do not.
//
// A bare function call (an *ast.Ident callee) is deliberately NOT accepted either.
// There is no package-level Validate, so accepting one would only ever match a
// future helper that this census could not prove anything about — and the answer
// to "my emitter reaches the guard through a helper" is to name that helper here,
// with its reason, exactly as iniDocProducers names its allowances.
//
// TestCallsAnyOnRequiresASidecarReceiver drives both halves against synthetic
// sources, because the production package (correctly) contains no bypass to catch.
func callsAnyOn(body *ast.BlockStmt, recv map[string]bool, names map[string]bool) bool {
	if body == nil || len(recv) == 0 {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || !names[sel.Sel.Name] {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && recv[id.Name] {
			found = true
		}
		return !found
	})
	return found
}

// funcBodySource returns one function's source text, found by declaration across
// every production file in the package rather than in a named one — the file it
// lives in is not part of what any census here claims.
//
// A SECOND DECLARING FILE IS A FATAL, not a first-match. It scanned in directory
// order and returned the first hit, so if the string it searches for ever matched
// in two files (a copy-paste of the doc comment above the real one, or the same
// declaration text appearing inside a block comment) it would silently census a
// body nobody meant — and the failure mode is the WORST one available to a source
// census: a green pass on the wrong text. Its internal/ui twin funcBodyInPackage
// already refuses that; this is the same refusal, in the same words.
func funcBodySource(t *testing.T, decl string) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var body, foundIn string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		i := strings.Index(string(src), decl)
		if i < 0 {
			continue
		}
		if foundIn != "" {
			t.Fatalf("%q is declared in both %s and %s — this census would be reading one of two bodies", decl, foundIn, name)
		}
		foundIn = name
		body = string(src)[i+len(decl):]
		if j := strings.Index(body, "\nfunc "); j >= 0 {
			body = body[:j]
		}
	}
	if foundIn == "" {
		t.Fatalf("no production file in this package declares %q — update this census to name what replaced it", decl)
	}
	return body
}

// mentionsINIDocPtr reports a type expression that puts a writable *INIDoc in the
// caller's hands ANYWHERE inside it: the bare pointer, but also []*INIDoc,
// map[string]*INIDoc, [2]*INIDoc and a struct field of any of those. A container
// of live documents is exactly as writable as one document.
func mentionsINIDocPtr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok && isINIDocPtr(e) {
			found = true
		}
		return !found
	})
	return found
}

// makesINIDoc reports a VALUE expression that constructs or fetches a document:
// &INIDoc{…}, or a call to one of the allow-listed producers. It is the arm that
// catches an exported package-level var with no written type —
// `var Scratch = &INIDoc{}` declares its type in its value.
func makesINIDoc(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.UnaryExpr:
			if v.Op == token.AND {
				if lit, ok := v.X.(*ast.CompositeLit); ok {
					if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "INIDoc" {
						found = true
					}
				}
			}
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok {
				if _, isProducer := iniDocProducers[id.Name]; isProducer {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
