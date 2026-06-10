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
// key=value pairs, ';'/'#' comment lines, whitespace-insensitive keys.
// Section-less keys live under the root section "".
type INI struct {
	values map[string]string // "section/key" (lowercased) → raw value
}

const iniSectionSep = "/"

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
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
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
