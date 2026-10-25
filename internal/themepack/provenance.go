package themepack

// PROVENANCE — the four values a copy carries about where it came from.
//
// ONE concern: turning a folder into the three [import] strings, and nothing
// else. It is split from copy.go because copying bytes and answering "who wrote
// this originally?" are two jobs that change for different reasons — the first
// changes when the caps or the guards change, the second when the format does.

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

// designHashBytesCap bounds what is read to fingerprint an upstream design INI.
// It is the format's own file bound, reused rather than restated: a
// courtroom_design.ini larger than the reader would accept is not a file whose
// hash means anything.
const designHashBytesCap = theme.IniDocByteCap

// provenanceTimeFormat is RFC 3339 in UTC — sortable, unambiguous across time
// zones, and readable by a human squinting at an INI. It is written into a
// metadata field, so it degrades rather than refusing if it ever grows (see
// internal/theme/metatext.go).
const provenanceTimeFormat = time.RFC3339

// loadSidecarFile reads a folder's native tier. (nil, nil) means "there is none",
// which is the ordinary state of a plain AO2 theme and never an error.
func loadSidecarFile(path string) (*theme.Sidecar, error) {
	return theme.LoadSidecar(path)
}

// nowStamp is the derived_at value.
func nowStamp() string { return time.Now().UTC().Format(provenanceTimeFormat) }

// designHash fingerprints the theme's courtroom_design.ini — the file the
// UPSTREAM author maintains, and therefore the one whose change means "upstream
// moved" rather than "I edited my copy".
//
// xxhash because that is the hasher this client already uses for the disk cache's
// keys; it is not a trust boundary and is never compared against anything but
// another run of itself. A theme with no design INI hashes to "", which reads as
// "nothing to compare" rather than as a hash of nothing.
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
