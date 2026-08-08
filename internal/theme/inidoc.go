package theme

import (
	"errors"
	"io"
	"strings"
	"unicode"
)

// INIDoc is a LOSSLESS INI document: the original lines, in order, with an index
// into them. ParseINI's map stays the fast READ path (theme.Load, char.ini);
// INIDoc is the WRITE path and is COLD ONLY — it is never touched from a draw,
// decode or resolver path (rule 2).
//
// Serialising re-emits every line VERBATIM except the ones whose value actually
// changed, so comments, blank lines, key order, key casing, unknown keys, entire
// unknown sections, and the author's own formatting all survive byte-identical. A
// writer that rebuilt from the map would silently delete every comment in a
// fifteen-year-old community theme the first time a user nudged one box — and the
// checked-in aceattorney2x fixture is comment-dominated, so that is not a
// hypothetical.
//
// Set replaces only the VALUE SPAN of a line, which is why a trailing comment
// survives an edit:
//
//	music_name = 1, 1, 134, 22  ; Relative to music_display.
//	                ↓ Set("", "music_name", "2, 2, 100, 20")
//	music_name = 2, 2, 100, 20  ; Relative to music_display.
//
// CLASSIFICATION IS PARITY-BOUND. Every line is read here EXACTLY as ParseINI
// reads it — same BOM strip, same whole-line ';' truncation, same '#' rule, same
// last-declaration-wins on a repeated key. If the two ever disagreed, INIDoc
// would rewrite a line the reader does not read, which is a silent no-op edit.
// FuzzINIDocRoundTrip pins the agreement on both halves.
type INIDoc struct {
	lines   []iniLine      // cap IniDocLineCap
	index   map[string]int // "section/key" (lowercased) -> line
	touched map[int]string // line -> new raw value
	// eol is the line ending DETECTED in the source and used for lines this
	// document ADDS. Every pre-existing line re-emits its OWN terminator (see
	// iniLine.term), so a file with mixed endings — or with no final newline,
	// which the aceattorney2x fonts fixture genuinely has — still round-trips
	// byte-identically. bufio.ScanLines eats a trailing '\r' silently, so an
	// LF-only writer would produce a whole-file diff on every Windows-authored
	// theme, which is most of the corpus. New files are written "\n".
	eol string
	// bom records that the source opened with a UTF-8 byte-order mark. It is held
	// at the DOCUMENT level, deliberately NOT as content of lines[0], because a
	// BOM is POSITIONAL: it is a BOM only while it is the first thing in the file.
	// As ordinary line content it would be displaceable — adding a root key to a
	// BOM'd file whose first line is a section header splices the new line above
	// it, the reader then strips nothing, "[Sec]" parses as neither header nor key
	// and is dropped, and every key beneath it silently reparents into the root
	// section. Stripping it here and re-emitting it FIRST in Bytes makes that
	// unreachable by construction. This is the write-side twin of the Uminek2x /
	// Uminek3x data loss ini.go:24 documents.
	bom bool
	// size is the source length, so Bytes can size its builder in one go.
	size int
}

const (
	// IniDocLineCap bounds the lines one document may hold: ~16x the largest
	// design INI in the ~80-pack reference corpus. Past it the file is refused,
	// never truncated — a truncated theme INI that then gets written back is
	// data loss (rule 4).
	IniDocLineCap = 8192
	// IniDocByteCap refuses a 1 MiB "INI" BEFORE parsing it. The largest real
	// theme INI in the corpus is a few tens of KiB; anything at this size is not
	// a theme file and must not be turned into 8192 line headers to find out.
	IniDocByteCap = 1 << 20

	// iniDocLF and iniDocCRLF are the two terminators a source line can carry.
	// Named because they are matched, emitted and compared in several places and
	// a bare "\r\n" literal in the middle of the splitter reads as noise.
	iniDocLF   = "\n"
	iniDocCRLF = "\r\n"
	// iniAssign is the key/value separator. Qt splits on the FIRST one, so a
	// value may contain further '=' characters.
	iniAssign = '='
	// iniAssignSpaced is how an ADDED line spells that separator: one space
	// either side, which is the shape every AO2 theme INI in the corpus uses and
	// therefore the shape a diff against one stays quiet in.
	iniAssignSpaced = " = "
	// iniSectionOpen / iniSectionClose delimit a section header line.
	iniSectionOpen  = "["
	iniSectionClose = "]"
	// iniHashComment starts a comment only at the beginning of a line (Qt
	// honours '#' there and nowhere else — see iniCommentStart).
	iniHashComment = "#"
)

var (
	// ErrINIDocTooLarge reports the IniDocByteCap refusal.
	ErrINIDocTooLarge = errors.New("theme: INI file is larger than the document cap")
	// ErrINIDocTooManyLines reports the IniDocLineCap refusal.
	ErrINIDocTooManyLines = errors.New("theme: INI file has more lines than the document cap")
	// ErrINIDocBadKey refuses a section or key that could not be written and
	// read back as itself.
	ErrINIDocBadKey = errors.New("theme: INI key or section is not writable")
	// ErrINIDocNoSection reports a rename of a section the document does not
	// declare. Distinct from a failure: an element authored this session has no
	// lines in the carrier yet, and renaming it is a no-op rather than a problem.
	ErrINIDocNoSection = errors.New("theme: INI document declares no such section")
	// ErrINIDocSectionTaken refuses a rename onto a section that already exists.
	// Merging two sections is not a rename — the winner of every shared key would
	// be decided by line order — so the caller has to mean it explicitly.
	ErrINIDocSectionTaken = errors.New("theme: INI section name is already taken")
	// ErrINIDocBadValue refuses a value that would not survive its own reader:
	// a newline splits the line in two, and a ';' truncates it (iniCommentStart).
	// Free text is percent-encoded by the caller before it gets here.
	ErrINIDocBadValue = errors.New("theme: INI value would not read back unchanged")
)

// iniLine is one source line, kept verbatim.
type iniLine struct {
	// raw is the line WITHOUT its terminator, aliasing the source string.
	raw string
	// term is the exact terminator this line carried: "\r\n", "\n", or "" for a
	// final line with no trailing newline. Re-emitted verbatim, which is what
	// makes a mixed-ending or newline-less file round-trip.
	term string
	// section is the section IN EFFECT at this line, lowercased. A header line
	// belongs to the section it opens, so "the last line of section S" includes
	// S's own header when S has no keys.
	section string
	// key is the lowercased key this line declares; isKey says whether it
	// declares one at all (comments, blanks, headers and separator-less lines
	// do not).
	key   string
	isKey bool
	// isHeader says the line OPENS its section, i.e. it is a "[name]" line. It
	// is not the negation of isKey — a comment is neither — and it is what
	// RenameSection edits.
	isHeader bool
	// valStart/valEnd bound the SPLICEABLE SPAN inside raw: the VALUE for a key
	// line, and the section NAME INSIDE THE BRACKETS for a header line. Both
	// exclude the surrounding whitespace and any ';' comment that follows, which
	// is what makes an edit preserve a trailing comment on either kind of line.
	//
	// ONE SPAN, ONE SPLICER. Bytes replaces raw[valStart:valEnd] with whatever
	// `touched` holds for the line and re-emits the rest verbatim, so a header
	// rename and a value edit are the same three writes rather than two
	// near-identical ones that could drift about whitespace.
	valStart, valEnd int
}

// ParseINIDoc builds a lossless document from raw INI bytes. It refuses, rather
// than truncates, anything past the caps.
func ParseINIDoc(src []byte) (*INIDoc, error) {
	if len(src) > IniDocByteCap {
		return nil, ErrINIDocTooLarge
	}
	text := string(src)
	d := &INIDoc{
		index:   map[string]int{},
		touched: map[int]string{},
		eol:     iniDocLF, // a file with no terminator at all is written LF
		size:    len(text),
	}
	// The BOM belongs to the FILE, not to line 0 (see INIDoc.bom). Take it off
	// before the split so no line ever carries it and no later insertion can
	// displace it; Bytes puts it back ahead of everything.
	if strings.HasPrefix(text, utf8BOM) {
		d.bom, text = true, text[len(utf8BOM):]
	}
	if text == "" {
		return d, nil // an empty file is a document with no lines, not one blank one
	}
	eolSeen, section, rest := false, "", text
	for rest != "" {
		var l iniLine
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			l.raw, l.term, rest = rest[:i], iniDocLF, rest[i+1:]
			if strings.HasSuffix(l.raw, "\r") {
				l.raw, l.term = l.raw[:len(l.raw)-1], iniDocCRLF
			}
			if !eolSeen {
				// The FIRST terminator in the file is the file's ending. Lines
				// that disagree keep their own; only ADDED lines use this.
				d.eol, eolSeen = l.term, true
			}
		} else {
			l.raw, l.term, rest = rest, "", "" // a final line with no newline
		}
		if len(d.lines) >= IniDocLineCap {
			return nil, ErrINIDocTooManyLines
		}
		section = classifyINILine(&l, section)
		if l.isKey {
			// Last declaration wins, exactly as ParseINI's map assignment does.
			// Indexing the FIRST would point Set at a line the reader ignores.
			d.index[l.section+iniSectionSep+l.key] = len(d.lines)
		}
		d.lines = append(d.lines, l)
	}
	return d, nil
}

// There is no LoadINIDoc. One existed — an exported open + LimitReader dance
// with zero callers anywhere, tests included — and it was a SECOND, untested
// copy of what LoadSidecar already does on the way to a model (ledger L32).
// Reading a document off the disk means reading a SIDECAR: LoadSidecar owns the
// cap, the migration and the degrade notes, and a path that skipped it would
// hand back a document nobody had validated.

// classifyINILine fills l's section/key/value fields and returns the section in
// effect AFTER it. A leading BOM was taken off the source ahead of the line split
// (see INIDoc.bom), so l.raw is pure line content here — which is also why a
// U+FEFF that reaches this function is ordinary key text, exactly as the reader
// treats a mid-file one (iniparity_test.go's mid-file case).
//
// This is ParseINI's body, expressed in OFFSETS instead of trimmed copies. Any
// change to one must be mirrored in the other; FuzzINIDocRoundTrip fails
// loudly when they drift.
func classifyINILine(l *iniLine, section string) string {
	l.section = section
	body := l.raw
	// Qt truncates at the first ';' before it looks at anything else, so this
	// must run ahead of the section/key split — see iniCommentStart.
	if i := strings.IndexByte(body, iniCommentStart); i >= 0 {
		body = body[:i]
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || strings.HasPrefix(trimmed, iniHashComment) {
		return section
	}
	if strings.HasPrefix(trimmed, iniSectionOpen) && strings.HasSuffix(trimmed, iniSectionClose) {
		inner := trimmed[1 : len(trimmed)-1]
		opened := strings.ToLower(strings.TrimSpace(inner))
		l.section = opened // a header belongs to the section it opens
		l.isHeader = true
		// The NAME's span inside raw, computed the same way the value span below
		// is: trimmed sits at offset `lead` in body, body is a prefix of raw, and
		// the name sits one byte past the '[' plus its own leading whitespace. A
		// header written "[  element.a  ]  ; the box" therefore renames to
		// "[  element.b  ]  ; the box" — the author's spacing and their comment
		// both survive, which is the whole reason this type exists.
		lead := len(body) - len(strings.TrimLeftFunc(body, unicode.IsSpace))
		nameLead := len(inner) - len(strings.TrimLeftFunc(inner, unicode.IsSpace))
		l.valStart = lead + 1 + nameLead
		l.valEnd = l.valStart + len(strings.TrimSpace(inner))
		return opened
	}
	eq := strings.IndexByte(trimmed, iniAssign)
	if eq < 0 {
		return section
	}
	// trimmed is body[lead:][:len(trimmed)], and body is a prefix of raw, so an
	// offset inside trimmed maps to raw by adding lead.
	lead := len(body) - len(strings.TrimLeftFunc(body, unicode.IsSpace))
	rest := trimmed[eq+1:]
	valLead := len(rest) - len(strings.TrimLeftFunc(rest, unicode.IsSpace))
	l.key = strings.ToLower(strings.TrimSpace(trimmed[:eq]))
	l.isKey = true
	l.valStart = lead + eq + 1 + valLead
	l.valEnd = l.valStart + len(strings.TrimSpace(rest))
	return section
}

// Get returns the value the READER would see for key under section, including
// any pending Set. Section and key are matched case-insensitively, like ParseINI.
func (d *INIDoc) Get(section, key string) (string, bool) {
	i, ok := d.index[strings.ToLower(section)+iniSectionSep+strings.ToLower(key)]
	if !ok {
		return "", false
	}
	if v, pending := d.touched[i]; pending {
		return v, true
	}
	l := d.lines[i]
	return l.raw[l.valStart:l.valEnd], true
}

// Set records a new value for key under section, adding the key — and the
// section header, if that is missing too — when it is not already declared.
//
// It refuses a value it could not read back as itself (rule: never emit a value
// that reads as something else). A ';' would truncate the line at the reader,
// and a newline would split it in two, so free text is percent-encoded by the
// caller before it arrives here.
func (d *INIDoc) Set(section, key, value string) error {
	sec, k := strings.ToLower(strings.TrimSpace(section)), strings.ToLower(strings.TrimSpace(key))
	if k == "" || badINIToken(sec) || badINIToken(k) {
		return ErrINIDocBadKey
	}
	if badINIValue(value) {
		return ErrINIDocBadValue
	}
	if i, ok := d.index[sec+iniSectionSep+k]; ok {
		d.touched[i] = value
		return nil
	}
	return d.appendKey(sec, k, value)
}

// RenameSection moves a whole section under a new name, losslessly.
//
// THE BYTES DO NOT MOVE — the NAME does. Only the header line's own name span is
// spliced; every key, comment, blank line and unknown line under it stays exactly
// where the author put it, in the author's own spelling, and the section keeps its
// position in the file. That is the difference between a rename and a
// delete-then-append, and it is the whole reason this primitive exists rather than
// its two-step imitation: this document has no DELETE, so "write the section again
// under the new name" leaves the old one behind and the file reads back as TWO of
// whatever the section described (sidecar_write.go's stated limit, and the exact
// duplication TestElementIdIsNotAnInspectorField was written to forbid until this
// function existed).
//
// EVERY header for the section is renamed, not just the first. A file may open the
// same section twice — the reader treats the two runs as one section (last
// declaration wins per key), so leaving the second header behind would split one
// section into two under different names and silently strand half its keys.
//
// It REFUSES rather than merges when `to` already exists: merging two sections is
// not a rename, it is a key collision whose winner depends on line order, and a
// caller that wants it can say so in two calls.
//
// COLD PATH (hard rule 2), like every other mutator here.
func (d *INIDoc) RenameSection(from, to string) error {
	f := strings.ToLower(strings.TrimSpace(from))
	t := strings.ToLower(strings.TrimSpace(to))
	if f == "" || t == "" || badINIToken(f) || badINIToken(t) {
		// The ROOT section is refused along with the malformed ones, and for a
		// reason rather than by accident: it has no header line to rename, so
		// "renaming" it could only mean inventing one and reparenting every key
		// above the first header into it.
		return ErrINIDocBadKey
	}
	if f == t {
		return nil
	}
	if d.hasSection(t) {
		return ErrINIDocSectionTaken
	}
	found := false
	for i := range d.lines {
		if d.lines[i].section != f {
			continue
		}
		if d.lines[i].isHeader {
			// The splice rides `touched`, which is the SAME mechanism a value edit
			// uses (see iniLine.valStart), so Bytes needs no header-shaped special
			// case and a rename cannot emit a line shape a Set could not.
			d.touched[i] = t
			found = true
		}
		d.lines[i].section = t
	}
	if !found {
		// A section that was never declared is not an error the CALLER can act on
		// — an element authored this session has no lines yet — so it is reported
		// as its own condition rather than as a failure.
		return ErrINIDocNoSection
	}
	d.reindex()
	return nil
}

// hasSection reports whether a header for name exists. Header-based rather than
// index-based on purpose: a section whose only content is a comment still exists,
// and renaming another section onto it would still be a collision.
func (d *INIDoc) hasSection(name string) bool {
	for i := range d.lines {
		if d.lines[i].isHeader && d.lines[i].section == name {
			return true
		}
	}
	return false
}

// Dirty reports whether anything has been Set. A clean document serialises to
// its source bytes, so a caller can skip the write entirely.
func (d *INIDoc) Dirty() bool { return len(d.touched) > 0 }

// Lines reports how many source lines the document holds.
func (d *INIDoc) Lines() int { return len(d.lines) }

// EOL is the line ending detected in the source and used for added lines.
func (d *INIDoc) EOL() string { return d.eol }

// Bytes serialises the document. Untouched lines are emitted verbatim, which is
// the whole point of the type: the default behaviour on any line this document
// did not deliberately change is "emit the original bytes".
func (d *INIDoc) Bytes() []byte {
	var b strings.Builder
	b.Grow(d.size)
	if d.bom {
		// Ahead of every line, unconditionally: a BOM is a BOM only while it is
		// the first thing in the file, so it cannot be one of the lines an insert
		// is allowed to push down (see INIDoc.bom).
		b.WriteString(utf8BOM)
	}
	for i, l := range d.lines {
		if v, pending := d.touched[i]; pending {
			b.WriteString(l.raw[:l.valStart])
			b.WriteString(v)
			b.WriteString(l.raw[l.valEnd:])
		} else {
			b.WriteString(l.raw)
		}
		b.WriteString(l.term)
	}
	return []byte(b.String())
}

// Write serialises the document to w. The atomic temp+fsync+rename dance belongs
// to the caller that owns the destination directory.
func (d *INIDoc) Write(w io.Writer) error {
	_, err := w.Write(d.Bytes())
	return err
}

// appendKey adds "key = value" at the END of its section — never at the end of
// the FILE, which for a root-section key would drop it inside whatever section
// happens to be last and silently change which key it is.
func (d *INIDoc) appendKey(section, key, value string) error {
	at, needHeader := d.sectionEnd(section)
	grow := 1
	if needHeader {
		grow = 2
	}
	if len(d.lines)+grow > IniDocLineCap {
		return ErrINIDocTooManyLines
	}
	if needHeader {
		at = d.insertLine(at, iniSectionOpen+section+iniSectionClose, section, "", false) + 1
		// The header a WRITE created is a header like any other: it carries its own
		// name span so RenameSection can move it later. Without this, a section this
		// document authored would be the one section in the file that could not be
		// renamed — and worse, a splice against a zero span would prepend the new
		// name in front of the '[' instead of replacing what is inside it.
		h := &d.lines[at-1]
		h.isHeader = true
		h.valStart = len(iniSectionOpen)
		h.valEnd = h.valStart + len(section)
	}
	raw := key + iniAssignSpaced + value
	// The value span is everything after "key = ".
	valStart := len(raw) - len(value)
	i := d.insertLine(at, raw, section, key, true)
	d.lines[i].valStart, d.lines[i].valEnd = valStart, len(raw)
	d.reindex()
	return nil
}

// insertLine splices one line in at index at, returning where it landed. Both
// line-number-keyed maps (index, touched) shift with it.
func (d *INIDoc) insertLine(at int, raw, section, key string, isKey bool) int {
	l := iniLine{raw: raw, term: d.eol, section: section, key: key, isKey: isKey}
	moved := 0
	if at > 0 && d.lines[at-1].term == "" {
		// The source did not end with a newline. Keep that property by moving
		// the missing terminator onto the new last line instead of inventing one.
		// The line ABOVE gains one it never had, so those bytes are counted here:
		// the new line's own term is now "", and charging only len(raw) would leave
		// size short of what Bytes emits and under-Grow its builder.
		d.lines[at-1].term = d.eol
		l.term = ""
		moved = len(d.eol)
	}
	d.lines = append(d.lines, iniLine{})
	copy(d.lines[at+1:], d.lines[at:])
	d.lines[at] = l
	d.size += len(raw) + len(l.term) + moved
	if len(d.touched) > 0 {
		shifted := make(map[int]string, len(d.touched))
		for i, v := range d.touched {
			if i >= at {
				i++
			}
			shifted[i] = v
		}
		d.touched = shifted
	}
	return at
}

// sectionEnd returns the index one past the last line of section, and whether a
// header for it has to be created.
//
// THE CONVENTION, stated as it actually behaves: every line up to the next
// header belongs to the section ABOVE it (classifyINILine assigns the section in
// effect), trailing comments and blank lines included. A new key therefore lands
// at the very end of that run — immediately before the next header — and when
// the author wrote a comment block INTRODUCING the next section, the new key
// lands underneath that comment.
//
// That placement is accepted, not overlooked. Deciding that a trailing comment
// "really" belongs to the section below means guessing at prose, and a wrong
// guess moves the author's own words, which is the one thing this type exists to
// never do. Where a key SITS never changes which key it is: the reader binds it
// to the section header above it, and that is the property the placement
// preserves.
func (d *INIDoc) sectionEnd(section string) (at int, needHeader bool) {
	end, found := -1, false
	for i, l := range d.lines {
		if l.section == section {
			end, found = i, true
		}
	}
	if !found {
		if section == "" {
			// The root section always EXISTS — it just has no lines yet because
			// the file opens with a header. Its key goes at the TOP: appending it
			// at EOF would drop it inside whatever section happens to be last,
			// silently making it a different key.
			return 0, false
		}
		// A section nobody has opened yet: its header goes at the end of the file.
		return len(d.lines), true
	}
	return end + 1, false
}

// reindex rebuilds the key index after an insertion shifted line numbers. Cold
// path: an insert happens once per newly-authored key, never per frame. Last
// declaration still wins, so a key inserted into a section that already shadows
// it cannot steal the reader's attention from the later line.
func (d *INIDoc) reindex() {
	clear(d.index)
	for i, l := range d.lines {
		if l.isKey {
			d.index[l.section+iniSectionSep+l.key] = i
		}
	}
}

// badINIToken reports a section or key that could not survive a write/read
// round trip: the reader would truncate it, split it, or fail to find the '='.
//
// U+FEFF is refused ANYWHERE in the token, not just as a prefix, even though only
// a leading one on the file's first line actually drifts (there the reader strips
// it — ini.go:34 — and reads back "k" for a key this document indexed as
// utf8BOM+"k"; a literal BOM is never spelled out in this source because it is
// invisible in every editor and diff, which is what makes the bug class so
// quiet). Whether a BOM-carrying token reads back as itself depends on WHERE
// its line lands, and the writer chooses that; a position-dependent
// classification is precisely what this type's PARITY-BOUND promise forbids. The
// character is invisible in every editor, so admitting it conditionally buys
// nothing and costs the invariant.
func badINIToken(s string) bool {
	return strings.ContainsAny(s, "=[]\r\n") || strings.ContainsRune(s, iniCommentStart) ||
		strings.Contains(s, utf8BOM) || strings.HasPrefix(s, iniHashComment) ||
		s != strings.TrimSpace(s)
}

// badINIValue reports a value that would not read back unchanged.
func badINIValue(v string) bool {
	return strings.ContainsAny(v, "\r\n") || strings.ContainsRune(v, iniCommentStart) ||
		v != strings.TrimSpace(v)
}
