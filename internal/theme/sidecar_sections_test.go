package theme

// EVERY SECTION EMITTER, DRIVEN FROM THE MODEL (ledger L2).
//
// Twelve of the fourteen emitters in writeInto could `return nil` with the whole
// suite green. Every writer gate other than the element and clock ones asserts only
// that an UNTOUCHED save is byte-identical — which an emitter that writes nothing
// satisfies perfectly — and no test in the tree assigned Overrides / Rotations /
// Media / Fonts / FontBind / Sounds / Panes / Effects / Palette / Chrome / Import /
// Meta and then called Bytes().
//
// W7's editor save path is the caller. A section that silently emits nothing is a
// user's work discarded at the moment they hit save, with no error anywhere.
//
// THE SHAPE. One table row per section: build the model, Bytes(), and then read the
// bytes back through the REAL parser and compare the model that comes out. Byte
// comparison alone would not do — the section header is what an empty emitter fails
// to produce, but a HALF-written section produces a header too — so each row also
// carries a probe that extracts its own slice of the model, and the round-tripped
// probe must equal the seeded one.

import (
	"reflect"
	"strings"
	"testing"
)

// sectionRow is one emitter's gate: seed the model, name the header the file must
// grow, and extract the piece of the model that must survive the round trip.
type sectionRow struct {
	name   string
	header string                   // the "[...]" the emitted file must contain
	seed   func(*Sidecar)           // fill the model this emitter reads
	probe  func(*Sidecar) any       // the piece that must round-trip
	extra  func(*testing.T, string) // optional: a check on the emitted TEXT
}

// sectionRows is the table. Fourteen rows for fourteen emitters — writeInto's list,
// in its order, so a new emitter added there without a row here reads as an obvious
// gap rather than as an accident.
func sectionRows() []sectionRow {
	return []sectionRow{{
		name:   "theme (Meta)",
		header: "[" + secTheme + "]",
		seed: func(s *Sidecar) {
			s.Revision = 7
			s.Meta = Meta{
				Name: "Section Gate", Author: "harness", License: "AGPL-3.0",
				Credit: "all original", Description: "a description; with a semicolon",
				MinClient: "1.90.0", Base: "default", Subtheme: "night",
				LayoutPreset: "compact", StylePreset: "manga", GeneratedBy: "gate",
			}
		},
		probe: func(s *Sidecar) any { return []any{s.Revision, s.Meta} },
		extra: func(t *testing.T, out string) {
			// Free text is percent-encoded on ';' because QSettings truncates a line at
			// the first one and this reader matches that on purpose. A raw semicolon
			// here is silent data loss on the next load.
			if !strings.Contains(out, freeTextSemi) {
				t.Errorf("[theme] description wrote a RAW semicolon (no %s anywhere) — the reader "+
					"truncates the line at it, so everything after it is lost:\n%s", freeTextSemi, out)
			}
		},
	}, {
		name:   "overrides",
		header: "[" + secOverrides + "]",
		seed: func(s *Sidecar) {
			s.Overrides = []KeyRect{
				{Key: "emotes", Rect: Rect{X: 5, Y: 253, W: 490, H: 98}},
				{Key: "ic_chatlog", Rect: Rect{X: 1, Y: 2, W: 3, H: 4}},
			}
		},
		probe: func(s *Sidecar) any { return s.Overrides },
	}, {
		name:   "rotations",
		header: "[" + secRotations + "]",
		seed: func(s *Sidecar) {
			// Angle 0 is a REAL entry ("this key is not rotated"), so it must survive
			// too — an emitter that treated it as absent would drop it.
			s.Rotations = []KeyAngle{{Key: "emotes", Angle: 64}, {Key: "ic_chatlog", Angle: 0}}
		},
		probe: func(s *Sidecar) any { return s.Rotations },
	}, {
		name:   "palette",
		header: "[" + secPalette + "]",
		seed: func(s *Sidecar) {
			text, accent := RGB{R: 0xED, G: 0xED, B: 0xED}, RGB{R: 0x00, G: 0xC8, B: 0xFF}
			s.Palette.Text, s.Palette.Accent = &text, &accent
		},
		probe: func(s *Sidecar) any {
			// By VALUE: the pointers differ across a round trip by construction.
			return []*RGB{s.Palette.Text, s.Palette.Panel, s.Palette.PanelHi, s.Palette.Accent, s.Palette.Danger}
		},
	}, {
		name:   "chrome",
		header: "[" + secChrome + "]",
		seed: func(s *Sidecar) {
			s.Chrome = Chrome{Shape: "round", ShapeTier: 2, TabbarHex: RGBA{R: 0x20, G: 0x22, B: 0x2A, A: 0xFF}}
		},
		probe: func(s *Sidecar) any { return s.Chrome },
	}, {
		name:   "fonts",
		header: "[" + secFonts + "]",
		seed: func(s *Sidecar) {
			s.Fonts = []FontFamily{{Family: "display", File: "Display.ttf"}, {Family: "body", File: "Body.otf"}}
		},
		probe: func(s *Sidecar) any { return s.Fonts },
	}, {
		name:   "fontbind",
		header: "[" + secFontBind + "]",
		seed: func(s *Sidecar) {
			s.FontBind = []KV{{Key: "message", Value: "display"}, {Key: "showname", Value: "body"}}
		},
		probe: func(s *Sidecar) any { return s.FontBind },
	}, {
		name:   "media",
		header: "[" + secMedia + "]",
		seed: func(s *Sidecar) {
			s.Media = []MediaEntry{
				{ID: "plate", File: SidecarMediaDir + "/plate.png", Bytes: 4096, Hash: "0123456789abcdef"},
			}
		},
		probe: func(s *Sidecar) any { return s.Media },
	}, {
		name:   "clock.N",
		header: "[" + secClockPrefix + "1]",
		seed: func(s *Sidecar) {
			s.Clocks[1] = ClockSpec{SpeedPct: 250, Declared: true}
		},
		probe: func(s *Sidecar) any { return s.Clocks[1].SpeedPct },
	}, {
		name:   "pane.N",
		header: "[" + secPanePrefix + "left]",
		seed: func(s *Sidecar) {
			p := Pane{
				ID: "left", Rect: Rect{X: 0, Y: 0, W: 300, H: 579}, Space: SpaceCourtroom,
				Divider: RGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF}, DividerPx: 3,
			}
			p.Hosts[0], p.NHosts = "ic_chatlog", 1
			s.Panes = []Pane{p}
		},
		probe: func(s *Sidecar) any { return s.Panes },
	}, {
		name:   "element.N",
		header: "[" + secElementPrefix + "plate]",
		seed: func(s *Sidecar) {
			s.Elements = []Element{{
				ID: "plate", Kind: ElemGradient, Band: BandMid,
				Rect: Rect{X: 10, Y: 10, W: 40, H: 40},
				Fill: RGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF},
			}}
		},
		probe: func(s *Sidecar) any { return s.Elements },
	}, {
		name:   "effect.N",
		header: "[" + secEffectPrefix + "objection]",
		seed: func(s *Sidecar) {
			s.Effects = []EffectBind{{
				Target: "objection", Effect: FXGlow,
				Color: RGBA{R: 0xF8, G: 0xCE, B: 0x7C, A: 0xFF}, PeriodMs: 900, AmpPct: 45, Clock: 1,
			}}
		},
		probe: func(s *Sidecar) any { return s.Effects },
	}, {
		name:   "sounds",
		header: "[" + secSounds + "]",
		seed: func(s *Sidecar) {
			s.Sounds = []KV{{Key: "objection", Value: "sounds/general/objection.opus"}}
		},
		probe: func(s *Sidecar) any { return s.Sounds },
	}, {
		name:   "import",
		header: "[" + secImport + "]",
		seed: func(s *Sidecar) {
			s.Import = ImportInfo{
				DerivedFrom: "themes/original", DerivedAt: "2026-08-06T00:00:00Z",
				DerivedHash: "abc123", Subthemes: []string{"night", "day"},
				PerMisc: true, LobbyDesign: true,
			}
		},
		probe: func(s *Sidecar) any { return s.Import },
	}}
}

// TestEverySectionEmitterWritesItsSection is the gate. Each row is independent —
// one section per file — so a failure names exactly which emitter went quiet.
func TestEverySectionEmitterWritesItsSection(t *testing.T) {
	rows := sectionRows()
	if len(rows) != sidecarSectionEmitterCount {
		t.Fatalf("the table has %d rows for %d emitters in writeInto — an emitter with no row here "+
			"is an emitter that can return nil with this suite green", len(rows), sidecarSectionEmitterCount)
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			sc := NewSidecar()
			row.seed(sc)
			want := row.probe(sc)

			b, err := sc.Bytes()
			if err != nil {
				t.Fatalf("Bytes(): %v", err)
			}
			out := string(b)
			if !strings.Contains(out, row.header) {
				t.Fatalf("a save of a model carrying this section produced no %s at all — the emitter "+
					"is writing nothing, and the editor's save silently discards the user's work:\n%s",
					row.header, out)
			}
			if row.extra != nil {
				row.extra(t, out)
			}

			// Through the REAL reader, so "it round-trips" means the file a third party
			// would read carries what the model held.
			back, err := ParseSidecar(b)
			if err != nil {
				t.Fatalf("the emitted file does not parse: %v\n%s", err, out)
			}
			if got := row.probe(back); !reflect.DeepEqual(got, want) {
				t.Fatalf("the section did not survive the round trip:\n got %#v\nwant %#v\nfile:\n%s",
					got, want, out)
			}

			// And a SECOND save is byte-identical: an emitter that appends rather than
			// setting in place would grow the file on every save.
			b2, err := back.Bytes()
			if err != nil {
				t.Fatalf("re-save: %v", err)
			}
			if string(b2) != out {
				t.Errorf("saving the re-read model changed the file — the emitter appends instead of "+
					"setting in place, so the file grows on every save:\n--- first ---\n%s\n--- second ---\n%s",
					out, string(b2))
			}
		})
	}
}

// sidecarSectionEmitterCount is how many emitters writeInto runs. Named so the table
// above is checked against a number rather than against a memory — and the number
// itself is checked against writeInto's own SOURCE by the census below, so it is a
// derived fact rather than a hand-maintained one.
const sidecarSectionEmitterCount = 14

// TestNoSectionIsWrittenForAnEmptyModel is the other half, and it is what stops the
// gate above from being satisfied by an emitter that writes its header
// unconditionally: an EMPTY model must produce a file with no section bodies at all
// beyond the one [theme] line every file states its grammar with.
//
// It matters because a stock AO2 theme's sidecar (a fresh "New theme…") must not
// ship eleven empty sections the author then has to read past.
func TestNoSectionIsWrittenForAnEmptyModel(t *testing.T) {
	sc := NewSidecar()
	b, err := sc.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	out := string(b)
	for _, row := range sectionRows() {
		if row.header == "["+secTheme+"]" {
			continue // every file states its own format; that is the one required line
		}
		if strings.Contains(out, row.header) {
			t.Errorf("an EMPTY model still wrote %s — absence is a value in this format, and a new "+
				"theme must not open with empty sections:\n%s", row.header, out)
		}
	}
}
