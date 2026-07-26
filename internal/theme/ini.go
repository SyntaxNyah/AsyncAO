// Package theme loads AO2-Client theme folders (courtroom_design.ini,
// courtroom_fonts.ini, theme images and sounds) so existing AO2 themes
// migrate to AsyncAO unchanged. Format reference: AO2-Client
// text_file_functions.cpp (QSettings INI + "x, y, w, h" tuples).
package theme

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// INI is a minimal QSettings-compatible INI reader: optional [sections],
// key=value pairs, a leading UTF-8 BOM, ';' comments (whole-line OR inline)
// and '#' comment lines, whitespace-insensitive keys. Section-less keys live
// under the root section "".
type INI struct {
	values map[string]string // "section/key" (lowercased) → raw value
}

const iniSectionSep = "/"

// utf8BOM is the byte-order mark Windows text editors prepend to a UTF-8 file.
// Qt's QSettings::IniFormat strips it, so a BOM'd theme's FIRST key parses
// normally under AO2 (verified against Qt 6.5.3: a file starting
// EF BB BF "showname = 16" reads back under the key "showname"). Without this
// the U+FEFF becomes part of the key — "\ufeffshowname" — and the entire first
// entry of the file silently disappears, which is exactly how the Uminek2x and
// Uminek3x themes lost their showname font size.
//
// Written as an escape on purpose: a literal BOM in the source is invisible in
// every editor and diff, which is what makes this class of bug so quiet.
const utf8BOM = "\ufeff"

// iniCommentStart is the character QSettings::IniFormat treats as the start of
// a comment ANYWHERE on a line, not only in column 0. Verified against Qt
// 6.5.3 with the real theme data that breaks without it:
//
//	music_name = 1, 1, 134, 22  ; Relative to music_display.  → 1,1,134,22
//	ooc_chat_message = 585, 324, 227, 23;492, 281, 222, 19    → 585,324,227,23
//	clock_0_align = center;                                   → center
//
// Truncating the WHOLE LINE rather than just the value is what Qt does, and the
// two follow-on behaviours confirm it: "foo;bar = 1" loses its "=" along with
// the comment and is dropped entirely, and "[Sec] ; note" still opens section
// Sec. Truncating at line level also subsumes the leading-';' comment line.
//
// '#' is deliberately NOT a comment start here. Qt honours it only at the
// beginning of a line (handled below); inline it is ordinary value text —
// "x = a # b" reads back as "a # b" — and AO2 payloads use '#' as a field
// separator, so truncating on it would corrupt real data.
//
// BLAST RADIUS: this reader is shared, so the rule also governs every
// server-supplied char.ini (courtroom/charini.go parses them through ParseINI).
// That is parity, not overreach — AO2 reads char.ini through QSettings::IniFormat
// too (AO2-Client text_file_functions.cpp:411) — and it was measured against the
// 74-theme reference corpus (517 INI files, 7335 lines containing a ';') before
// being turned on.
//
// TWO Qt SUB-RULES ARE KNOWINGLY NOT IMPLEMENTED. Both were measured at ZERO
// occurrences over that same corpus, so neither can change any parse result we
// have ever seen; they are recorded here so the next person meets them as a known
// divergence instead of rediscovering them from a field report.
//
//  1. Qt suspends the comment rule inside DOUBLE QUOTES. Probed on Qt 6.5.3:
//     `quoted = "a;b"` reads back as `a;b`, where this reader yields `"a`.
//     Corpus: 0 lines carry a '"' before their first ';'.
//  2. Qt consumes a BACKSLASH-ESCAPED semicolon. Probed on Qt 6.5.3:
//     `escaped = a\;b` does not end at the ';' at all, where this reader yields
//     `a\`. Corpus: 0 lines contain a backslash immediately before a ';'.
//
// Implementing either means carrying quote state (and Qt's whole escape table)
// through the scan for data that does not exist, so the honest trade is to leave
// them out and say so.
const iniCommentStart = ';'

// LoadINI reads path; a missing file yields an empty (usable) INI.
func LoadINI(path string) (*INI, error) {
	f, err := os.Open(path)
	if err != nil {
		return &INI{values: map[string]string{}}, err
	}
	defer f.Close()
	return ParseINI(f)
}

// ParseINI reads INI content from any reader (char.ini payloads fetched over
// the asset pipeline use this).
func ParseINI(r io.Reader) (*INI, error) {
	ini := &INI{values: map[string]string{}}
	section := ""
	scanner := bufio.NewScanner(r)
	firstLine := true
	for scanner.Scan() {
		raw := scanner.Text()
		if firstLine {
			// A BOM can only ever precede the very first line of the file.
			raw = strings.TrimPrefix(raw, utf8BOM)
			firstLine = false
		}
		// Qt truncates at the first ';' before it looks at anything else, so
		// this must run ahead of the section/key split — see iniCommentStart.
		// It also covers a whole-line ';' comment (the line becomes empty).
		if i := strings.IndexByte(raw, iniCommentStart); i >= 0 {
			raw = raw[:i]
		}
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		ini.values[section+iniSectionSep+strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return ini, scanner.Err()
}

// Get returns the value for key in the root section.
func (i *INI) Get(key string) (string, bool) {
	return i.GetSection("", key)
}

// GetSection returns the value for key under [section].
func (i *INI) GetSection(section, key string) (string, bool) {
	v, ok := i.values[strings.ToLower(section)+iniSectionSep+strings.ToLower(key)]
	return v, ok
}

// Len reports how many keys were loaded.
func (i *INI) Len() int { return len(i.values) }

// SectionKeys returns every key=value under [section] (keys lowercased,
// section prefix stripped). Iteration order is unspecified — callers that
// care must sort.
func (i *INI) SectionKeys(section string) map[string]string {
	prefix := strings.ToLower(section) + iniSectionSep
	out := map[string]string{}
	for k, v := range i.values {
		if strings.HasPrefix(k, prefix) {
			out[k[len(prefix):]] = v
		}
	}
	return out
}
