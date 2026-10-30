package themepack

// Provenance: the [import] strings a copy carries about where it came from.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/cespare/xxhash/v2"
)

// designHashBytesCap reuses the format's file bound: a larger design INI is not one whose hash means anything.
const designHashBytesCap = theme.IniDocByteCap

// provenanceTimeFormat is RFC 3339 in UTC; a metadata field, so it degrades rather than refuses.
const provenanceTimeFormat = time.RFC3339

// loadSidecarFile reads a folder's sidecar; (nil, nil) is the ordinary state of a plain AO2 theme.
func loadSidecarFile(path string) (*theme.Sidecar, error) {
	return theme.LoadSidecar(path)
}

func nowStamp() string { return time.Now().UTC().Format(provenanceTimeFormat) }

// designHash fingerprints courtroom_design.ini, so a change means upstream moved; "" when absent.
func designHash(dir string) string {
	f, err := os.Open(filepath.Join(dir, theme.DesignFileName))
	if err != nil {
		return ""
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, io.LimitReader(f, designHashBytesCap)); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return strconv.FormatUint(h.Sum64(), 16)
}
