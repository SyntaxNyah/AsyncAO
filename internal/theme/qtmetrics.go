package theme

// The row pitch Qt gives a face, from its OS/2 table; SDL_ttf reads a different table of the same file.
// AO2's message is a QTextEdit (src/courtroom.h:626, constructed at src/courtroom.cpp:97).
// Parsing belongs on the theme-apply goroutine (hard rule 2); internal/render never opens a file.

import "encoding/binary"

// QtRowPitch is a face's Qt metrics in font units; the zero value is inert, keeping the SDL_ttf metric.
type QtRowPitch struct {
	// UnitsPerEm is head.unitsPerEm; zero means no usable metrics.
	UnitsPerEm int
	// Ascent and Descent are the family fsSelection selected, normalised to positive so the sum is a pitch.
	Ascent  int
	Descent int
	Leading int
	// UsesTypoMetrics records the branch taken, as the two families agree for most faces.
	UsesTypoMetrics bool
}

func (q QtRowPitch) Valid() bool { return q.UnitsPerEm > 0 && q.Ascent+q.Descent > 0 }

// Pitch is Qt's baseline step at emPx, or 0 without metrics.
// Ascent and descent round separately (QFontEngine::initializeHeightMetrics); a rounded sum lands a pixel off.
func (q QtRowPitch) Pitch(emPx int32) int32 {
	if !q.Valid() || emPx <= 0 {
		return 0
	}
	return q.scale(q.Ascent, emPx) + q.scale(q.Descent, emPx) + q.scale(q.Leading, emPx)
}

// scale converts font units to pixels at emPx, rounding half up on non-negative values.
func (q QtRowPitch) scale(units int, emPx int32) int32 {
	if units <= 0 {
		return 0
	}
	return int32((units*int(emPx)*2 + q.UnitsPerEm) / (2 * q.UnitsPerEm))
}

// Table tags and field offsets (the OS/2 ones are easy to be one field out on).
const (
	sfntTagHead = "head"
	sfntTagOS2  = "OS/2"

	// head: unitsPerEm is a uint16 at byte 18; the table is 54 bytes.
	headUnitsPerEmOff = 18
	headMinLen        = headUnitsPerEmOff + 2

	// OS/2: fsSelection uint16 at 62, sTypo* int16 at 68/70/72, usWin* uint16 at 74/76.
	// A table under 78 bytes predates win metrics and is refused.
	os2FsSelectionOff   = 62
	os2TypoAscenderOff  = 68
	os2TypoDescenderOff = 70
	os2TypoLineGapOff   = 72
	os2WinAscentOff     = 74
	os2WinDescentOff    = 76
	os2MinLen           = os2WinDescentOff + 2

	// os2UseTypoMetrics is fsSelection bit 7, USE_TYPO_METRICS; Qt honours it and FreeType does not.
	os2UseTypoMetrics = 0x0080

	// The sfnt directory: numTables is uint16 at byte 4, and 16-byte records start at byte 12.
	sfntNumTablesOff = 4
	sfntDirOff       = 12
	sfntRecordLen    = 16
	sfntRecordOffOff = 8
	sfntRecordLenOff = 12
	sfntHeaderMinLen = sfntDirOff

	// A TrueType Collection has uint32 member offsets at byte 12; only the first member is read.
	ttcTag       = "ttcf"
	ttcFirstOff  = 12
	ttcMinLength = ttcFirstOff + 4

	// sfntMaxTables bounds the directory walk (hard rule 4), since a hostile font can claim 65535 tables.
	sfntMaxTables = 64
)

// ReadQtRowPitch parses head and OS/2 tables; runs on the theme-apply goroutine once per face (hard rule 2).
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
	// A font may author a positive typo descender; clamp, as a negative term would shorten the pitch.
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

// sfntTables returns the head and OS/2 slices of an sfnt or a collection's first member; either may be nil.
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
