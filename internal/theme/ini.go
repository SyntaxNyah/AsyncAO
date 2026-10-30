// Package theme loads AO2-Client theme folders unchanged (format: AO2-Client text_file_functions.cpp).
package theme

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// INI is a minimal QSettings-compatible reader; section-less keys live under root "".
type INI struct {
	values map[string]string // "section/key" (lowercased) → raw value
}

const iniSectionSep = "/"

// utf8BOM is stripped as Qt's IniFormat does, or the first key silently vanishes.
// Written as an escape because a literal BOM is invisible in editors and diffs.
const utf8BOM = "\ufeff"

// iniCommentStart begins a comment anywhere on a line, as in QSettings (text_file_functions.cpp:411).
const iniCommentStart = ';'

// LoadINI reads path; a missing file yields an empty usable INI.
func LoadINI(path string) (*INI, error) {
	f, err := os.Open(path)
	if err != nil {
		return &INI{values: map[string]string{}}, err
	}
	defer f.Close()
	return ParseINI(f)
}

// ParseINI reads INI content from any reader, including char.ini fetched over the asset pipeline.
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
		// Qt truncates at the first ';' before anything else, so this runs ahead of the section/key split.
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

func (i *INI) Get(key string) (string, bool) {
	return i.GetSection("", key)
}

func (i *INI) GetSection(section, key string) (string, bool) {
	v, ok := i.values[strings.ToLower(section)+iniSectionSep+strings.ToLower(key)]
	return v, ok
}

func (i *INI) Len() int { return len(i.values) }

// SectionKeys returns every key under [section], lowercased without the prefix; order is unspecified.
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
