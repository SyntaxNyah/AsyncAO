package theme

// The Qt row-pitch reader, held against NUMBERS QT ITSELF PRODUCED.
//
// Every expectation below was measured, not derived: a probe built against the
// Qt 6.5.3 kit on the development box (QGuiApplication under the `windows`
// platform plugin, QFontDatabase::addApplicationFont, QFontMetricsF) printed the
// ascent, descent, leading and lineSpacing of each face at each size, and those
// are the values pinned here. That is the point of the whole file: this reader is
// an emulation of another engine's metric choice, so a test that re-derived the
// expectation from the same table arithmetic the reader uses would prove nothing.
//
// The two faces are the ones the repository ships, so the gate needs no fixture
// and cannot rot when a machine lacks a font. Both happen to have
// USE_TYPO_METRICS SET, which is exactly the branch a win-metrics-only reader
// would get wrong — and would get wrong in the direction that damages the
// client's own text rather than a theme's.

import (
	"os"
	"path/filepath"
	"testing"
)

// repoFontDir is where the bundled faces live, relative to this package.
const repoFontDir = "../ui/fonts"

// TestReadQtRowPitchMatchesQtForTheBundledFaces is the parity gate.
func TestReadQtRowPitchMatchesQtForTheBundledFaces(t *testing.T) {
	cases := []struct {
		file  string
		emPx  int32
		upem  int
		typo  bool // fsSelection bit 7
		pitch int32
		// qtLineSpacing is what the Qt probe printed, for the record. It is the
		// number `pitch` is allowed to differ from by at most one pixel — Qt keeps
		// the LEADING fractional and this reader rounds it (see Pitch).
		qtLineSpacing float64
	}{
		{"Twemoji.ttf", 24, 512, true, 27, 26.62},
		{"Twemoji.ttf", 40, 512, true, 44, 44.38},
		{"OpenDyslexic-Regular.otf", 24, 1000, true, 43, 43.00},
		{"OpenDyslexic-Regular.otf", 40, 1000, true, 73, 73.00},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join(repoFontDir, tc.file))
		if err != nil {
			t.Fatalf("%s: %v — the bundled faces are not optional", tc.file, err)
		}
		q := ReadQtRowPitch(data)
		if !q.Valid() {
			t.Fatalf("%s: no usable OS/2 metrics", tc.file)
		}
		if q.UnitsPerEm != tc.upem {
			t.Errorf("%s: unitsPerEm = %d, want %d", tc.file, q.UnitsPerEm, tc.upem)
		}
		if q.UsesTypoMetrics != tc.typo {
			t.Errorf("%s: UsesTypoMetrics = %v, want %v — the fsSelection branch is the whole point",
				tc.file, q.UsesTypoMetrics, tc.typo)
		}
		if got := q.Pitch(tc.emPx); got != tc.pitch {
			t.Errorf("%s @%dpx: Pitch = %d, want %d (Qt 6.5.3 measured lineSpacing %.2f)",
				tc.file, tc.emPx, got, tc.pitch, tc.qtLineSpacing)
		}
		if d := float64(tc.pitch) - tc.qtLineSpacing; d > 1 || d < -1 {
			t.Errorf("%s @%dpx: the pinned pitch %d is %.2f px from Qt's own %.2f — more than the leading rounding can explain",
				tc.file, tc.emPx, tc.pitch, d, tc.qtLineSpacing)
		}
	}
}

// TestQtRowPitchWinBranchArithmetic pins the OTHER branch — the one no bundled
// face exercises and the one the reported defect lives in.
//
// The numbers are DangitV3IC's, the `message_font` of the shipped DRRA / KFO
// theme family and the face in the capture this work was measured against: upem
// 1000, usWinAscent 1107, usWinDescent 334, sTypo 880 / -120 / 0, fsSelection
// 0x0040. Qt 6.5.3 reports ascent 27, descent 8, leading 0 at 24 px — a 35 px
// row pitch where hhea (and therefore SDL_ttf) says 24.
//
// The face itself is not in the repository (it belongs to a third-party theme
// pack), so the struct is built directly. That is not a mirror test: the
// arithmetic under test is Pitch's, and the field values are the ones the reader
// is separately proven to extract by the bundled-face gate above.
func TestQtRowPitchWinBranchArithmetic(t *testing.T) {
	q := QtRowPitch{UnitsPerEm: 1000, Ascent: 1107, Descent: 334}
	if got := q.Pitch(24); got != 35 {
		t.Errorf("DangitV3IC @24px: Pitch = %d, want 35 (Qt 6.5.3 measured 27 + 8 + 0)", got)
	}
	// The external-leading rule: Arial's 67-unit typo gap is entirely swallowed by
	// win metrics 366 units taller than its typo metrics, so Qt reports leading 0
	// and a 44 px pitch at 40 px.
	arial := ReadQtRowPitch(synthOS2Font(2048, 0x0040, 1491, -431, 67, 1854, 434))
	if !arial.Valid() {
		t.Fatal("the synthetic Arial-metrics face did not parse")
	}
	if arial.UsesTypoMetrics {
		t.Error("fsSelection 0x0040 has USE_TYPO_METRICS clear — the win branch must be taken")
	}
	if arial.Leading != 0 {
		t.Errorf("external leading = %d, want 0 — the win metrics already swallowed the gap", arial.Leading)
	}
	if got := arial.Pitch(40); got != 44 {
		t.Errorf("Arial-metrics @40px: Pitch = %d, want 44 (Qt 6.5.3 measured 36 + 8 + 0)", got)
	}
}

// TestReadQtRowPitchRefusesRubbish holds the "total function" contract: a theme
// font is an untrusted file out of a downloaded archive, and every shape of
// broken input has to come back as the inert zero value rather than a panic.
func TestReadQtRowPitchRefusesRubbish(t *testing.T) {
	cases := map[string][]byte{
		"nil":                   nil,
		"empty":                 {},
		"shorter than a header": {0, 1, 0, 0, 0},
		"header only":           make([]byte, 12),
		"ascii":                 []byte("this is not a font, it is a sentence"),
		"ttcf stub":             []byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x01\xff\xff\xff\xff"),
	}
	for name, data := range cases {
		q := ReadQtRowPitch(data)
		if q.Valid() || q.Pitch(24) != 0 {
			t.Errorf("%s: got %+v (pitch %d), want the inert zero value", name, q, q.Pitch(24))
		}
	}
	// A record whose offset/length point outside the file must be skipped, not
	// sliced — this is the bounds check, and it is the one a fuzzer finds first.
	bad := synthOS2Font(1000, 0, 800, -200, 0, 900, 250)
	for cut := len(bad); cut > 0; cut -= 7 {
		_ = ReadQtRowPitch(bad[:cut]) // must not panic at any truncation
	}
}

// TestQtRowPitchZeroValueIsInert pins the contract every consumer relies on: the
// zero value means "no opinion", and render.UseRowPitch(0) is a no-op.
func TestQtRowPitchZeroValueIsInert(t *testing.T) {
	var q QtRowPitch
	if q.Valid() {
		t.Error("the zero value must not be Valid")
	}
	for _, px := range []int32{-1, 0, 1, 12, 1000} {
		if got := q.Pitch(px); got != 0 {
			t.Errorf("zero value Pitch(%d) = %d, want 0", px, got)
		}
	}
	// A valid face still refuses a non-positive em: the caller's size arithmetic
	// floors at 1, but a pitch for "no pixels" is not a number anyone can use.
	v := QtRowPitch{UnitsPerEm: 1000, Ascent: 1000, Descent: 250}
	if got := v.Pitch(0); got != 0 {
		t.Errorf("Pitch(0) = %d, want 0", got)
	}
}

// synthOS2Font builds the smallest sfnt that carries a head and an OS/2 table
// with the given metrics — enough for ReadQtRowPitch and nothing else.
//
// A synthetic font rather than a checked-in fixture because the only thing under
// test is the two tables' arithmetic, and a real face would drag several hundred
// kilobytes of outlines into the repository to say the same thing.
func synthOS2Font(upem int, fsSelection uint16, typoAsc, typoDesc, typoGap int16, winAsc, winDesc uint16) []byte {
	const (
		numTables = 2
		headLen   = 54
		os2Len    = 96
	)
	dirLen := sfntDirOff + numTables*sfntRecordLen
	headOff := dirLen
	os2Off := headOff + headLen
	out := make([]byte, os2Off+os2Len)

	be16 := func(b []byte, v uint16) { b[0], b[1] = byte(v>>8), byte(v) }
	be32 := func(b []byte, v uint32) {
		b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	be32(out[0:], 0x00010000)
	be16(out[sfntNumTablesOff:], numTables)
	rec := func(i int, tag string, off, length int) {
		p := sfntDirOff + i*sfntRecordLen
		copy(out[p:], tag)
		be32(out[p+sfntRecordOffOff:], uint32(off))
		be32(out[p+sfntRecordLenOff:], uint32(length))
	}
	rec(0, sfntTagHead, headOff, headLen)
	rec(1, sfntTagOS2, os2Off, os2Len)

	be16(out[headOff+headUnitsPerEmOff:], uint16(upem))
	be16(out[os2Off+os2FsSelectionOff:], fsSelection)
	be16(out[os2Off+os2TypoAscenderOff:], uint16(typoAsc))
	be16(out[os2Off+os2TypoDescenderOff:], uint16(typoDesc))
	be16(out[os2Off+os2TypoLineGapOff:], uint16(typoGap))
	be16(out[os2Off+os2WinAscentOff:], winAsc)
	be16(out[os2Off+os2WinDescentOff:], winDesc)
	return out
}
