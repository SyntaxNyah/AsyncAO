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
type INI struct {
	values map[string]string // "section/key" (lowercased) → raw value
}

const iniSectionSep = "/"

// utf8BOM is the byte-order mark Windows text editors prepend to a UTF-8 file.
const utf8BOM = "\ufeff"

// iniCommentStart is the character QSettings::IniFormat treats as the start of
// a comment ANYWHERE on a line, not only in column 0. Verified against Qt
// 6.5.3 with the real theme data that breaks without it:
// too (AO2-Client text_file_functions.cpp:411) — and it was measured against the
// 74-theme reference corpus (517 INI files, 7335 lines containing a ';') before
// being turned on.
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
