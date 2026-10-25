package theme

// The row pitch Qt gives a face, read out of the face's own OS/2 table.
// text engine (ui_vp_message is a QTextEdit — AO2-Client src/courtroom.h:626,
// constructed at src/courtroom.cpp:97). AsyncAO stacks them with the metric
// SDL_ttf reports (internal/render.FontLineSpacing). Those two read DIFFERENT
// tables out of the same font file, and for a face that disagrees with itself the
// gap is enormous:
// It is a THEME concern rather than a render one because the input is a font FILE
// and the read is parse work that belongs on the theme-apply goroutine (hard rule
// 2), next to every other per-theme font decision. internal/render consumes the
// resulting number and never opens a file.

import "encoding/binary"

// QtRowPitch is one face's Qt vertical metrics, in FONT UNITS, plus the em they
// are expressed against. Font units rather than pixels because a single face is
// opened at several sizes (the chatbox zoom, the canvas fold, the device scale)
type QtRowPitch struct {
	// UnitsPerEm is head.unitsPerEm. Zero means the face carried no usable
	// metrics and the whole value is inert.
	UnitsPerEm int
	// Ascent / Descent are the metric family fsSelection selected, both POSITIVE
	// (a descent is stored negative in the typo fields and positive in the win
	// fields; both are normalised to positive-down here so the sum is a pitch).
	Ascent  int
	Descent int
	// Leading is the extra gap between rows.
	Leading int
	// UsesTypoMetrics records which branch was taken. Not consulted by Pitch —
	// the branch is already baked into the three numbers — but a test that has to
	// prove the branch was TAKEN cannot do it from the pitch alone (the two
	// families agree for most faces), and a diagnostic that cannot say which
	// family a face used is the kind of thing this file exists to stop guessing at.
	UsesTypoMetrics bool
}

// Valid reports whether the face carried the tables this needs.
func (q QtRowPitch) Valid() bool { return q.UnitsPerEm > 0 && q.Ascent+q.Descent > 0 }

// Pitch is the baseline-to-baseline step Qt would use for this face at emPx
// pixels, or 0 when the face carried no usable metrics.
// Each of the three parts is scaled and rounded SEPARATELY, because that is what
// Qt does to the ascent and the descent (QFixed::round() per metric in
// QFontEngine::initializeHeightMetrics, measurable in the table above: Calibri's
// 38.09 and 10.74 are reported as 38 and 11) and because rounding the sum instead
// lands a pixel off — 38.09 + 10.74 rounds to 49 term-by-term and to 48 as a sum,
// and Qt reports 49. Qt leaves the LEADING fractional; rounding it here can put
// the answer one pixel over Qt's, which is harmless in the only direction this
// number is ever used (render.UseRowPitch raises, never tightens).
func (q QtRowPitch) Pitch(emPx int32) int32 {
	if !q.Valid() || emPx <= 0 {
		return 0
	}
	return q.scale(q.Ascent, emPx) + q.scale(q.Descent, emPx) + q.scale(q.Leading, emPx)
}

// scale converts font units to pixels at emPx, rounding half up on the
// non-negative values this is only ever called with.
func (q QtRowPitch) scale(units int, emPx int32) int32 {
	if units <= 0 {
		return 0
	}
	return int32((units*int(emPx)*2 + q.UnitsPerEm) / (2 * q.UnitsPerEm))
}

// Table tags and field offsets. Named because a bare 18 in a slice expression is
// exactly the kind of number hard rule 9 exists to stop, and because the OS/2
// offsets in particular are easy to be one field out on.
const (
	sfntTagHead = "head"
	sfntTagOS2  = "OS/2"

	// head: unitsPerEm is a uint16 at byte 18; the table is 54 bytes.
	headUnitsPerEmOff = 18
	headMinLen        = headUnitsPerEmOff + 2

	// OS/2 (version >= 0): fsSelection is a uint16 at 62,
	// sTypoAscender/Descender/LineGap are int16 at 68/70/72, and
	// usWinAscent/usWinDescent are uint16 at 74/76. A table shorter than 78 bytes
	// predates the win metrics and is refused outright.
	os2FsSelectionOff   = 62
	os2TypoAscenderOff  = 68
	os2TypoDescenderOff = 70
	os2TypoLineGapOff   = 72
	os2WinAscentOff     = 74
	os2WinDescentOff    = 76
	os2MinLen           = os2WinDescentOff + 2

	// os2UseTypoMetrics is fsSelection bit 7 (0x0080), "USE_TYPO_METRICS" in the
	// OpenType spec: "If set, it is strongly recommended that applications use
	// sTypoAscender - sTypoDescender + sTypoLineGap as the default line spacing
	// for this font." Qt honours it; FreeType does not.
	os2UseTypoMetrics = 0x0080

	// The sfnt table directory: numTables is a uint16 at byte 4 of the header and
	// the 16-byte records start at byte 12.
	sfntNumTablesOff = 4
	sfntDirOff       = 12
	sfntRecordLen    = 16
	sfntRecordOffOff = 8
	sfntRecordLenOff = 12
	sfntHeaderMinLen = sfntDirOff

	// A TrueType Collection puts its member offsets at byte 12, one uint32 each.
	ttcTag       = "ttcf"
	ttcFirstOff  = 12
	ttcMinLength = ttcFirstOff + 4

	// sfntMaxTables bounds the directory walk (hard rule 4). Theme fonts are user
	// data: a hostile file can claim 65535 tables in two bytes and make the loop
	// as long as it likes. 64 is more than double what any real face ships.
	sfntMaxTables = 64
)

// ReadQtRowPitch parses a font file's head + OS/2 tables out of `data`.
// Runs on the theme-apply goroutine, once per distinct face (hard rule 2).
func ReadQtRowPitch(data []byte) QtRowPitch {
	head, os2 := sfntTables(data)
	if len(head) < headMinLen || len(os2) < os2MinLen {
		return QtRowPitch{}
	}
	upem := int(binary.BigEndian.Uint16(head[headUnitsPerEmOff:]))
	fsSelection := binary.BigEndian.Uint16(os2[os2FsSelectionOff:])
	typoAsc := int(int16(binary.BigEndian.Uint16(os2[os2TypoAscenderOff:])))
	typoDesc := int(int16(binary.BigEndian.Uint16(os2[os2TypoDescenderOff:])))
	typoGap := int(int16(binary.BigEndian.Uint16(os2[os2TypoLineGapOff:])))
	winAsc := int(binary.BigEndian.Uint16(os2[os2WinAscentOff:]))
	winDesc := int(binary.BigEndian.Uint16(os2[os2WinDescentOff:]))

	q := QtRowPitch{UnitsPerEm: upem}
	if fsSelection&os2UseTypoMetrics != 0 {
		q.UsesTypoMetrics = true
		q.Ascent, q.Descent, q.Leading = typoAsc, -typoDesc, typoGap
	} else {
		q.Ascent, q.Descent = winAsc, winDesc
		if ext := typoGap - ((winAsc + winDesc) - (typoAsc - typoDesc)); ext > 0 {
			q.Leading = ext
		}
	}
	// A designer can author a negative typo descender OR a positive one; the
	// negation above assumes the spec's sign (descender below the baseline is
	// negative) and a font that breaks it would produce a negative descent here.
	// Clamp rather than trust: a negative term would SHORTEN the pitch, which is
	// the one direction this whole mechanism must never move.
	if q.Ascent < 0 {
		q.Ascent = 0
	}
	if q.Descent < 0 {
		q.Descent = 0
	}
	if q.Leading < 0 {
		q.Leading = 0
	}
	if !q.Valid() {
		return QtRowPitch{}
	}
	return q
}

// sfntTables returns the head and OS/2 table slices of an sfnt (or of the first
// member of a TrueType Collection). Either may come back nil.
func sfntTables(data []byte) (head, os2 []byte) {
	base := 0
	if len(data) >= ttcMinLength && string(data[:4]) == ttcTag {
		off := int(binary.BigEndian.Uint32(data[ttcFirstOff:]))
		if off < 0 || off > len(data) {
			return nil, nil
		}
		base = off
	}
	if len(data) < base+sfntHeaderMinLen {
		return nil, nil
	}
	n := int(binary.BigEndian.Uint16(data[base+sfntNumTablesOff:]))
	if n > sfntMaxTables {
		n = sfntMaxTables
	}
	for i := 0; i < n; i++ {
		rec := base + sfntDirOff + i*sfntRecordLen
		if rec+sfntRecordLen > len(data) {
			break
		}
		tag := string(data[rec : rec+4])
		if tag != sfntTagHead && tag != sfntTagOS2 {
			continue
		}
		off := int(binary.BigEndian.Uint32(data[rec+sfntRecordOffOff:]))
		length := int(binary.BigEndian.Uint32(data[rec+sfntRecordLenOff:]))
		if off < 0 || length < 0 || off+length > len(data) {
			continue // a record that points outside the file is not a table
		}
		if tag == sfntTagHead {
			head = data[off : off+length]
		} else {
			os2 = data[off : off+length]
		}
	}
	return head, os2
}
