//go:build themecensus

// OPT-IN: run with `go test -tags themecensus ./internal/theme/`.
//
// The checked-in aceattorney2x fixture is one theme by one author. The property
// INIDoc has to hold is a property of the ECOSYSTEM — ~80 third-party packs, 517
// INI files, fifteen years of hand-editing in whatever text editor was open —
// and that corpus is hundreds of megabytes of art nobody's machine has at the
// same path, so it is env-gated rather than checked in. Set it before a release
// audit:
//
//	$env:ASYNCAO_THEME_CORPUS = "C:\...\Skrapegropen AO THEMES"
//	go test -tags themecensus -run TestINIDocRoundTripsTheThemeCorpus ./internal/theme/

package theme

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// themeCorpusEnv points at a directory of real AO2 themes (the folder CONTAINING
// `themes/`, which is what theme.Load takes). It is the same variable
// internal/ui's census uses, deliberately: one corpus, one path to set.
const themeCorpusEnv = "ASYNCAO_THEME_CORPUS"

// corpusFileByteCap bounds what this walk will even read. It is IniDocByteCap,
// aliased so the two can never drift: a corpus file the parser would refuse is
// reported as a refusal, not skipped by a different number here.
const corpusFileByteCap = IniDocByteCap

// TestINIDocRoundTripsTheThemeCorpusByteIdentical is the blast-radius version of
// TestINIDocRoundTripsByteIdentical, taken over REAL themes. Every .ini under
// the corpus is parsed and re-emitted untouched; a single differing byte is a
// theme whose comments, spacing or line endings our writer would have rewritten.
func TestINIDocRoundTripsTheThemeCorpusByteIdentical(t *testing.T) {
	root := os.Getenv(themeCorpusEnv)
	if root == "" {
		t.Skipf("set %s to a folder containing themes/ to run the corpus audit", themeCorpusEnv)
	}
	files, refused, crlf, unread := 0, 0, 0, 0
	err := filepath.WalkDir(filepath.Join(root, ThemesDirName), func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.EqualFold(filepath.Ext(p), ".ini") {
			return nil // an unreadable subtree must not end the audit
		}
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Errorf("%s: %v", p, readErr)
			return nil
		}
		if len(src) > corpusFileByteCap {
			refused++
			t.Errorf("%s is %d bytes, past IniDocByteCap (%d) — the cap is too small for the real corpus",
				p, len(src), corpusFileByteCap)
			return nil
		}
		d, parseErr := ParseINIDoc(src)
		if parseErr != nil {
			refused++
			t.Errorf("%s: %v — a real theme file the writer would refuse to open", p, parseErr)
			return nil
		}
		files++
		if bytes.Contains(src, []byte("\r\n")) {
			crlf++
		}
		if got := d.Bytes(); !bytes.Equal(got, src) {
			t.Errorf("%s: round trip changed the bytes (%d in, %d out); first diff at byte %d",
				p, len(src), len(got), firstDiff(src, got))
		}
		if d.Lines() > IniDocLineCap {
			t.Errorf("%s: %d lines, past IniDocLineCap (%d)", p, d.Lines(), IniDocLineCap)
		}
		// A file the READER could not parse (bufio's 64 KiB token limit sits well
		// under IniDocByteCap) is counted and named, never silently skipped: the
		// round-trip half above still ran, but no key was ever compared, and a
		// corpus summary that hid that would report parity it did not measure.
		if !assertReaderParity(t, p, string(src), d) {
			unread++
			t.Logf("%s: the READER refused this file, so only the byte round trip was checked", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the corpus: %v", err)
	}
	if files == 0 {
		t.Fatalf("%s is set but no .ini files were found under it", themeCorpusEnv)
	}
	t.Logf("corpus round trip: %d INI files byte-identical, %d CRLF-authored, %d refused by a cap, "+
		"%d unparsed by the reader (parity unchecked)", files, crlf, refused, unread)
}
