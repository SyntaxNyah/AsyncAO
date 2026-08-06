package theme

// EVERY CAPPED COLLECTION, PUT TO BOTH DIRECTIONS (ledger L3, round-4 gap).
//
// THE GAP THIS CLOSES. The write guard's cap rows are what stand between an
// editor session and a theme that refuses to LOAD: one collection over its named
// cap and ParseSidecar answers ErrSidecarCap for the WHOLE file, which is the
// precise data loss the caps block exists to prevent. Seven of validateCaps' ten
// collection rows shipped DELETABLE with this package green — [rotations],
// [fonts], [fontbind], [sounds], [pane.*], [effect.*] and [import] subthemes had
// no gate at all. Proven by deleting them and watching the suite pass, not by
// reading. They are also exactly the collections a theme editor authors most: 25
// fonts, 33 sounds, 9 panes, 17 effect binds or 5 subthemes saved cleanly and the
// theme then never opened again.
//
// THE SHAPE, and why it is a derived table rather than eleven hand-written rows.
// One row per capped collection, and the row is the ONE place that collection's
// cap constant is named. Each row is put to BOTH directions of the production
// code:
//
//   - the MODEL at exactly the cap must SAVE, and one entry past it must be
//     refused before a byte is written;
//   - the equivalent FILE at exactly the cap must LOAD, and one entry past it
//     must be refused.
//
// Those four are what make the (collection -> cap constant) mapping inside
// validateCaps ENFORCED rather than merely written down a second time beside the
// reader's inline caps: a row carrying a cap the reader does not use fails one of
// the four, whichever side moved. Neither side restates a limit — both are
// called.
//
// And the table itself cannot quietly lose a row, which is how seven went missing
// the first time: TestEveryModelCollectionHasACapGate derives the REQUIRED set
// from the Sidecar type by reflection, so a collection added to the model with no
// cap gate is a red test the day the field lands rather than the day somebody's
// theme grows past 24 sounds.
//
// COLD PATH, like everything else on the sidecar: these gates build models and
// format strings, and none of it runs on a frame.

import (
	"errors"
	"reflect"
	"testing"
)

// sidecarCapRow is ONE capped collection, stated once for both directions.
type sidecarCapRow struct {
	// field is the collection's path inside the Sidecar model, in the spelling
	// the reflection census produces ("Elements", "Import.Subthemes"). It is what
	// binds this table to the TYPE: a collection the census finds with no row
	// here is a gap, and a row here naming nothing in the model is stale.
	field string
	// what names the collection the way a refusal names it, for subtest names
	// and messages.
	what string
	// limit is the named cap both directions must agree on. The only place this
	// table spells a cap constant.
	limit int
	// fill puts exactly n legal entries in the MODEL, replacing whatever was
	// there. "Exactly n" is what lets one row test the cap and one past it.
	fill func(s *Sidecar, n int)
	// file is the same n entries as a FILE body, for the reader's half.
	file func(n int) string
}

// sidecarCapRows is the table: the ten growable collections of the model, plus
// the one cap that is an int beside a fixed array rather than a slice length.
//
// Order follows validateCaps' own rows, so a reader can put the two side by side.
func sidecarCapRows() []sidecarCapRow {
	return []sidecarCapRow{{
		field: "Overrides",
		what:  "[" + secOverrides + "]",
		limit: OverrideCap,
		fill: func(s *Sidecar, n int) {
			s.Overrides = make([]KeyRect, n)
			for i := range s.Overrides {
				s.Overrides[i] = KeyRect{Key: capEntryID("k", i), Rect: Rect{X: 1, Y: 2, W: 3, H: 4}}
			}
		},
		file: func(n int) string { return "[" + secOverrides + "]\n" + repeatKeys("k%d", "1, 2, 3, 4", n) },
	}, {
		field: "Rotations",
		what:  "[" + secRotations + "]",
		limit: OverrideCap,
		fill: func(s *Sidecar, n int) {
			s.Rotations = make([]KeyAngle, n)
			for i := range s.Rotations {
				s.Rotations[i] = KeyAngle{Key: capEntryID("k", i), Angle: 64}
			}
		},
		file: func(n int) string { return "[" + secRotations + "]\n" + repeatKeys("k%d", "64", n) },
	}, {
		field: "Fonts",
		what:  "[" + secFonts + "]",
		limit: FontCap,
		fill: func(s *Sidecar, n int) {
			s.Fonts = make([]FontFamily, n)
			for i := range s.Fonts {
				s.Fonts[i] = FontFamily{Family: capEntryID("fam", i), File: capEntryID("face", i) + ".ttf"}
			}
		},
		file: func(n int) string { return "[" + secFonts + "]\n" + repeatKeys("fam%d", "face%d.ttf", n) },
	}, {
		field: "FontBind",
		what:  "[" + secFontBind + "]",
		limit: FontBindCap,
		fill: func(s *Sidecar, n int) {
			s.FontBind = make([]KV, n)
			for i := range s.FontBind {
				s.FontBind[i] = KV{Key: capEntryID("el", i), Value: capEntryID("fam", i)}
			}
		},
		file: func(n int) string { return "[" + secFontBind + "]\n" + repeatKeys("el%d", "fam%d", n) },
	}, {
		field: "Media",
		what:  "[" + secMedia + "]",
		limit: MediaCap,
		fill: func(s *Sidecar, n int) {
			s.Media = make([]MediaEntry, n)
			for i := range s.Media {
				s.Media[i] = MediaEntry{ID: capEntryID("m", i), File: capEntryID("file", i) + ".png"}
			}
		},
		file: func(n int) string { return "[" + secMedia + "]\n" + repeatKeys("m%d", "file%d.png", n) },
	}, {
		field: "Sounds",
		what:  "[" + secSounds + "]",
		limit: SoundCap,
		fill: func(s *Sidecar, n int) {
			s.Sounds = make([]KV, n)
			for i := range s.Sounds {
				s.Sounds[i] = KV{Key: capEntryID("s", i), Value: capEntryID("a", i) + ".opus"}
			}
		},
		file: func(n int) string { return "[" + secSounds + "]\n" + repeatKeys("s%d", "a%d.opus", n) },
	}, {
		field: "Panes",
		what:  "[" + secPanePrefix + "*]",
		limit: PaneCap,
		fill: func(s *Sidecar, n int) {
			s.Panes = make([]Pane, n)
			for i := range s.Panes {
				s.Panes[i] = Pane{ID: capEntryID("p", i), Rect: Rect{X: 1, Y: 2, W: 3, H: 4}}
			}
		},
		file: func(n int) string { return repeatSection(secPanePrefix+"p%d", "rect = 1, 2, 3, 4\n", n) },
	}, {
		field: "Elements",
		what:  "[" + secElementPrefix + "*]",
		limit: ElementCap,
		fill: func(s *Sidecar, n int) {
			s.Elements = make([]Element, n)
			for i := range s.Elements {
				s.Elements[i] = Element{
					ID: capEntryID("e", i), Kind: ElemGradient,
					Fill: RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
				}
			}
		},
		file: func(n int) string {
			return repeatSection(secElementPrefix+"e%d", "kind = gradient\nfill = #ffffff\n", n)
		},
	}, {
		field: "Effects",
		what:  "[" + secEffectPrefix + "*]",
		limit: EffectBindCap,
		fill: func(s *Sidecar, n int) {
			s.Effects = make([]EffectBind, n)
			for i := range s.Effects {
				s.Effects[i] = EffectBind{Target: capEntryID("w", i), Effect: FXGlow, AmpPct: 40}
			}
		},
		file: func(n int) string { return repeatSection(secEffectPrefix+"w%d", "effect = glow\n", n) },
	}, {
		field: "Import.Subthemes",
		what:  "[" + secImport + "] subthemes",
		limit: SubthemeCap,
		fill: func(s *Sidecar, n int) {
			s.Import.Subthemes = make([]string, n)
			for i := range s.Import.Subthemes {
				s.Import.Subthemes[i] = capEntryID("s", i)
			}
		},
		file: func(n int) string {
			return "[" + secImport + "]\nsubthemes = " + repeatList("s%d", n) + "\n"
		},
	}, {
		// The one cap that is not a slice length: PaneHostCap bounds an int that
		// sits beside a FIXED array, so "n entries" means the count, and past the
		// array there is nowhere for the hosts to go.
		field: "Panes[].NHosts",
		what:  "[" + secPanePrefix + "*] hosts",
		limit: PaneHostCap,
		fill: func(s *Sidecar, n int) {
			p := Pane{ID: "p", Rect: Rect{X: 1, Y: 2, W: 3, H: 4}, NHosts: n}
			for i := 0; i < n && i < len(p.Hosts); i++ {
				p.Hosts[i] = capEntryID("h", i)
			}
			s.Panes = []Pane{p}
		},
		file: func(n int) string {
			return "[" + secPanePrefix + "p]\nhosts = " + repeatList("h%d", n) + "\n"
		},
	}}
}

// sidecarCapRowCount is how many capped collections the model has: validateCaps'
// ten collection rows plus its pane-host loop. Named so the table above is
// checked against a number rather than against a memory, exactly as
// sidecarSectionEmitterCount does for the section emitters.
const sidecarCapRowCount = 11

// capEntryID builds a legal, unique id for the fixtures above. The generated ids
// must satisfy validID ([a-z0-9_-]) or the write guard would refuse the AT-cap
// model for the wrong reason and the gate would pass vacuously.
func capEntryID(prefix string, i int) string { return prefix + formatInt(i) }

// TestEveryCappedCollectionIsGuardedInBothDirections is the gate the seven
// missing validateCaps rows died on. For every capped collection: the model at
// the cap saves, the file at the cap loads, and one past it is refused by BOTH,
// with the same sentinel.
//
// Deleting any row from validateCaps turns this red on that collection's
// over-cap half; changing one to a different cap constant turns it red on the
// at-cap half. That is the whole point — the mapping is enforced by execution
// instead of by sitting next to the reader's copy of it.
func TestEveryCappedCollectionIsGuardedInBothDirections(t *testing.T) {
	// Every fixture file states its own format, as a real theme does.
	const header = "[" + secTheme + "]\nformat = 1\n\n"
	for _, row := range sidecarCapRows() {
		t.Run(row.what, func(t *testing.T) {
			// --- exactly AT the cap: both directions must accept ---------------
			atCap := NewSidecar()
			row.fill(atCap, row.limit)
			if _, err := atCap.Bytes(); err != nil {
				t.Fatalf("the WRITER refused %s at exactly its cap of %d: %v — a guard stricter than the reader "+
					"makes a legal theme unsaveable in the editor", row.what, row.limit, err)
			}
			if _, err := ParseSidecar([]byte(header + row.file(row.limit))); err != nil {
				t.Fatalf("the READER refused a file with %s at exactly its cap of %d: %v — the fixture is wrong, "+
					"or the two directions disagree about where the cap is", row.what, row.limit, err)
			}

			// --- one PAST it: both directions must refuse, same sentinel -------
			over := NewSidecar()
			row.fill(over, row.limit+1)
			out, err := over.Bytes()
			if !errors.Is(err, ErrSidecarCap) {
				t.Fatalf("the WRITER accepted %s with %d entries past a cap of %d (err = %v) — that file is "+
					"ErrSidecarCap on the next load, so the WHOLE theme stops opening and the author's work is "+
					"gone at the moment they hit save", row.what, row.limit+1, row.limit, err)
			}
			if out != nil {
				t.Errorf("Bytes() returned %d bytes ALONGSIDE its refusal; a caller that ignores err would write them",
					len(out))
			}
			if _, err := ParseSidecar([]byte(header + row.file(row.limit+1))); !errors.Is(err, ErrSidecarCap) {
				t.Fatalf("the READER accepted %s with %d entries past a cap of %d (err = %v) — the writer's cap and "+
					"the reader's have forked", row.what, row.limit+1, row.limit, err)
			}
		})
	}
}

// sidecarCapCensusDepth bounds the type walk below. The model is a shallow tree
// of plain structs, so the bound exists only so a future self-referential field
// fails loudly instead of hanging the suite.
const sidecarCapCensusDepth = 8

// sidecarCapRowsNotACollectionField holds the rows whose subject is NOT a slice
// in the model, each with the reason it is still a cap a caller can exceed.
// Anything else in the table that the census cannot find is a stale row.
var sidecarCapRowsNotACollectionField = map[string]string{
	"Panes[].NHosts": "a plain int beside a FIXED array: past the array there is nowhere to put the hosts, " +
		"and HostKeys would panic before anyone could diagnose it",
}

// TestEveryModelCollectionHasACapGate is the anti-omission device: the required
// set of rows is DERIVED from the Sidecar type, not remembered.
//
// Writing the rows out by hand is what failed the first time — seven of eleven
// were never written, and the seven matching rules in validateCaps were therefore
// deletable with this package green. A field added to the model now fails here on
// the day it lands, which is the only moment anybody is looking.
func TestEveryModelCollectionHasACapGate(t *testing.T) {
	rows := sidecarCapRows()
	if len(rows) != sidecarCapRowCount {
		t.Fatalf("the table has %d rows for %d capped collections — a collection with no row here is a "+
			"validateCaps rule that can be deleted with this package green", len(rows), sidecarCapRowCount)
	}
	rowed := make(map[string]bool, len(rows))
	for _, r := range rows {
		if rowed[r.field] {
			t.Fatalf("two rows claim %s — one of them is testing the other's collection", r.field)
		}
		rowed[r.field] = true
	}

	inModel := make(map[string]bool, len(rows))
	for _, path := range sidecarModelCollections(t) {
		inModel[path] = true
		if !rowed[path] {
			t.Errorf("Sidecar.%s is a growable collection with NO cap gate: an editor can fill it past its cap, "+
				"save cleanly, and the whole theme then refuses to load with ErrSidecarCap — add a row to "+
				"sidecarCapRows (and the matching rule to validateCaps)", path)
		}
	}
	for _, r := range rows {
		if inModel[r.field] {
			continue
		}
		if sidecarCapRowsNotACollectionField[r.field] == "" {
			t.Errorf("the table has a row for %q, which is no longer a collection in the model — a stale row "+
				"tests nothing; if the subject is deliberately not a slice, say why in "+
				"sidecarCapRowsNotACollectionField", r.field)
		}
	}
}

// sidecarModelCollections walks the Sidecar TYPE and returns the path of every
// exported slice a holder of the model can grow ("Elements", "Import.Subthemes").
//
// Slices only: a fixed array is bounded by its own type and cannot be overfilled
// — except where an int count rides beside one, which is the one row the map
// above accounts for. Unexported fields are skipped because no editor holds them.
func sidecarModelCollections(t *testing.T) []string {
	t.Helper()
	var out []string
	var walk func(rt reflect.Type, path string, depth int)
	walk = func(rt reflect.Type, path string, depth int) {
		if depth > sidecarCapCensusDepth {
			t.Fatalf("the model walk passed depth %d at %q — a self-referential field would hang this census",
				sidecarCapCensusDepth, path)
		}
		switch rt.Kind() {
		case reflect.Pointer, reflect.Array:
			walk(rt.Elem(), path, depth+1)
		case reflect.Slice:
			out = append(out, path)
			walk(rt.Elem(), path+"[]", depth+1)
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if f.PkgPath != "" {
					continue // unexported: not reachable by whoever holds the model
				}
				name := f.Name
				if path != "" {
					name = path + "." + f.Name
				}
				walk(f.Type, name, depth+1)
			}
		}
	}
	walk(reflect.TypeOf(Sidecar{}), "", 0)
	return out
}
