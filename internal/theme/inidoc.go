package theme

import (
	"errors"
	"io"
	"strings"
	"unicode"
)

// INIDoc is a lossless INI document for writing, cold path only (hard rule 2).
// It must classify lines exactly as ParseINI does, or an edit rewrites a line the reader ignores.
type INIDoc struct {
	lines   []iniLine      // cap IniDocLineCap
	index   map[string]int // "section/key" (lowercased) -> line
	touched map[int]string // line -> new raw value
	// eol is used for added lines; existing ones keep iniLine.term, or a Windows theme rewrites whole.
	eol string
	// bom is held on the document because a BOM only counts first in the file.
	// As line content, an insert above it would break the header and reparent its keys into root.
	bom bool
	// size is the source length, so Bytes can size its builder in one go.
	size int
}

const (
	// IniDocLineCap bounds a document's lines; refused, as a truncated INI written back is data loss.
	IniDocLineCap = 8192
	// IniDocByteCap refuses a 1 MiB "INI" before parsing, since real theme INIs are tens of KiB.
	IniDocByteCap = 1 << 20

	iniDocLF   = "\n"
	iniDocCRLF = "\r\n"
	// iniAssign is the key/value separator; Qt splits on the first, so a value may contain more.
	iniAssign = '='
	// iniAssignSpaced is how an added line spells it, the shape AO2 theme INIs use, keeping diffs quiet.
	iniAssignSpaced = " = "
	iniSectionOpen  = "["
	iniSectionClose = "]"
	// iniHashComment starts a comment only at the beginning of a line, as in Qt (see iniCommentStart).
	iniHashComment = "#"
)

var (
	ErrINIDocTooLarge = errors.New("theme: INI file is larger than the document cap")
	// ErrINIDocTooManyLines reports the IniDocLineCap refusal.
	ErrINIDocTooManyLines = errors.New("theme: INI file has more lines than the document cap")
	// ErrINIDocBadKey refuses a section or key that could not be written and read back as itself.
	ErrINIDocBadKey = errors.New("theme: INI key or section is not writable")
	// ErrINIDocNoSection reports a rename of an undeclared section, a no-op for a new element.
	ErrINIDocNoSection = errors.New("theme: INI document declares no such section")
	// ErrINIDocSectionTaken refuses a rename onto a section, since line order would decide shared keys.
	ErrINIDocSectionTaken = errors.New("theme: INI section name is already taken")
	// ErrINIDocBadValue refuses a newline, which splits the line, or a ';', which truncates it.
	ErrINIDocBadValue = errors.New("theme: INI value would not read back unchanged")
)

type iniLine struct {
	// raw is the line without its terminator, aliasing the source string.
	raw string
	// term is this line's exact terminator ("" if none), re-emitted so mixed endings round-trip.
	term string
	// section is the lowercased section in effect; a header belongs to the section it opens.
	section string
	// isKey says whether the line declares a key (comments, blanks, headers and separator-less lines do not).
	key   string
	isKey bool
	// isHeader says the line is "[name]"; not the negation of isKey, since a comment is neither.
	isHeader bool
	// valStart and valEnd bound the editable span: a key's value or the name inside a header's brackets.
	// They exclude whitespace and any ';' comment, which an edit preserves.
	valStart, valEnd int
}

// ParseINIDoc builds a lossless document, refusing rather than truncating anything past the caps.
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
	// The BOM belongs to the file (see INIDoc.bom); Bytes puts it back first.
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
				// The first terminator is the file's ending, used only for added lines.
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
			// Last declaration wins as in ParseINI; indexing the first would point Set at a line the reader ignores.
			d.index[l.section+iniSectionSep+l.key] = len(d.lines)
		}
		d.lines = append(d.lines, l)
	}
	return d, nil
}

// There is no LoadINIDoc: LoadSidecar owns the cap, migration and degrade notes.

// classifyINILine fills l's fields and returns the section in effect after it.
// It is ParseINI's body in offsets; a change to one must be mirrored in the other.
func classifyINILine(l *iniLine, section string) string {
	l.section = section
	body := l.raw
	// Qt truncates at the first ';' before anything else, so this runs ahead of the section/key split.
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
		// The name's span in raw, so a rename keeps the author's spacing and trailing comment.
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
	// body is a prefix of raw, so an offset inside trimmed maps to raw by adding lead.
	lead := len(body) - len(strings.TrimLeftFunc(body, unicode.IsSpace))
	rest := trimmed[eq+1:]
	valLead := len(rest) - len(strings.TrimLeftFunc(rest, unicode.IsSpace))
	l.key = strings.ToLower(strings.TrimSpace(trimmed[:eq]))
	l.isKey = true
	l.valStart = lead + eq + 1 + valLead
	l.valEnd = l.valStart + len(strings.TrimSpace(rest))
	return section
}

// Get returns the value the reader would see, including any pending Set; matching is case-insensitive like ParseINI.
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

// Set records a value, adding the key and header if missing; refuses one that would not read back.
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

// RenameSection splices only the header's name span, so every line stays put; cold path (hard rule 2).
func (d *INIDoc) RenameSection(from, to string) error {
	f := strings.ToLower(strings.TrimSpace(from))
	t := strings.ToLower(strings.TrimSpace(to))
	if f == "" || t == "" || badINIToken(f) || badINIToken(t) {
		// Root has no header line, so renaming it would reparent every key above the first header.
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
			// The same mechanism as a value edit (see iniLine.valStart), so Bytes needs no header special case.
			d.touched[i] = t
			found = true
		}
		d.lines[i].section = t
	}
	if !found {
		// An element authored this session has no lines yet, so this is its own condition rather than a failure.
		return ErrINIDocNoSection
	}
	d.reindex()
	return nil
}

// RemoveSection deletes a section's header, keys and inner comments; cold path (hard rule 2).
func (d *INIDoc) RemoveSection(name string) error {
	sec := strings.ToLower(strings.TrimSpace(name))
	if sec == "" || badINIToken(sec) {
		// Root has no header, so removing it could only mean deleting every key above the first header.
		return ErrINIDocBadKey
	}
	if !d.hasSection(sec) {
		return ErrINIDocNoSection
	}
	drop := make([]bool, len(d.lines))
	for i := range d.lines {
		drop[i] = d.lines[i].section == sec
	}
	d.keepTrailingProse(drop)
	kept := make([]iniLine, 0, len(d.lines))
	touched := make(map[int]string, len(d.touched))
	for i := range d.lines {
		if drop[i] {
			continue
		}
		if v, pending := d.touched[i]; pending {
			touched[len(kept)] = v
		}
		kept = append(kept, d.lines[i])
	}
	// Bytes round-trips a missing final newline, so deleting the last line moves it up rather than inventing one.
	if n := len(d.lines); n > 0 && drop[n-1] && d.lines[n-1].term == "" && len(kept) > 0 {
		kept[len(kept)-1].term = ""
	}
	d.lines, d.touched = kept, touched
	d.reparentOrphans()
	d.reindex()
	d.resize()
	return nil
}

// keepTrailingProse un-drops blanks and comments ending each removed run, never the header.
func (d *INIDoc) keepTrailingProse(drop []bool) {
	for i := 0; i < len(drop); i++ {
		if !drop[i] {
			continue
		}
		end := i
		for end < len(drop) && drop[end] {
			end++
		}
		for k := end - 1; k > i; k-- {
			if d.lines[k].isKey || d.lines[k].isHeader {
				break
			}
			drop[k] = false
		}
		i = end
	}
}

// reparentOrphans re-derives each line's section as a re-parse would, keeping classifyINILine the only answer.
func (d *INIDoc) reparentOrphans() {
	section := ""
	for i := range d.lines {
		if d.lines[i].isHeader {
			section = d.lines[i].section
			continue
		}
		d.lines[i].section = section
	}
}

// resize re-derives the builder hint after a removal; only used to Grow, so a touched line's length is not tracked.
func (d *INIDoc) resize() {
	n := 0
	for i := range d.lines {
		n += len(d.lines[i].raw) + len(d.lines[i].term)
	}
	d.size = n
}

// hasSection checks headers, as a section holding only a comment still exists and would collide.
func (d *INIDoc) hasSection(name string) bool {
	for i := range d.lines {
		if d.lines[i].isHeader && d.lines[i].section == name {
			return true
		}
	}
	return false
}

// Dirty reports whether anything has been Set; a clean document serialises to its source bytes.
func (d *INIDoc) Dirty() bool { return len(d.touched) > 0 }

func (d *INIDoc) Lines() int { return len(d.lines) }

func (d *INIDoc) EOL() string { return d.eol }

// Bytes serialises the document, emitting untouched lines verbatim.
func (d *INIDoc) Bytes() []byte {
	var b strings.Builder
	b.Grow(d.size)
	if d.bom {
		// Ahead of every line: a BOM only counts first in the file (see INIDoc.bom).
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

// Write serialises the document to w; the atomic rename belongs to the caller that owns the destination directory.
func (d *INIDoc) Write(w io.Writer) error {
	_, err := w.Write(d.Bytes())
	return err
}

// appendKey adds "key = value" at its section's end; at file end, a root key would change section.
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
		// A created header carries its name span, or a splice would put the name before the '['.
		h := &d.lines[at-1]
		h.isHeader = true
		h.valStart = len(iniSectionOpen)
		h.valEnd = h.valStart + len(section)
	}
	raw := key + iniAssignSpaced + value
	valStart := len(raw) - len(value)
	i := d.insertLine(at, raw, section, key, true)
	d.lines[i].valStart, d.lines[i].valEnd = valStart, len(raw)
	d.reindex()
	return nil
}

// insertLine splices a line in at index at and returns where it landed; index and touched shift with it.
func (d *INIDoc) insertLine(at int, raw, section, key string, isKey bool) int {
	l := iniLine{raw: raw, term: d.eol, section: section, key: key, isKey: isKey}
	moved := 0
	if at > 0 && d.lines[at-1].term == "" {
		// No final newline: move it to the new last line, counting the bytes so Bytes does not under-Grow.
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

// sectionEnd returns the index past section's last line and whether a header is needed.
// Trailing comments stay in the section, as guessing could move the author's words.
func (d *INIDoc) sectionEnd(section string) (at int, needHeader bool) {
	end, found := -1, false
	for i, l := range d.lines {
		if l.section == section {
			end, found = i, true
		}
	}
	if !found {
		if section == "" {
			// Root always exists; its key goes at the top, as EOF would put it in the last section.
			return 0, false
		}
		return len(d.lines), true
	}
	return end + 1, false
}

// reindex rebuilds the key index after an insert; last declaration still wins.
func (d *INIDoc) reindex() {
	clear(d.index)
	for i, l := range d.lines {
		if l.isKey {
			d.index[l.section+iniSectionSep+l.key] = i
		}
	}
}

// badINIToken reports a section or key that could not round-trip.
// A BOM-carrying token is refused because whether it survives depends on where its line lands.
func badINIToken(s string) bool {
	return strings.ContainsAny(s, "=[]\r\n") || strings.ContainsRune(s, iniCommentStart) ||
		strings.Contains(s, utf8BOM) || strings.HasPrefix(s, iniHashComment) ||
		s != strings.TrimSpace(s)
}

func badINIValue(v string) bool {
	return strings.ContainsAny(v, "\r\n") || strings.ContainsRune(v, iniCommentStart) ||
		v != strings.TrimSpace(v)
}
