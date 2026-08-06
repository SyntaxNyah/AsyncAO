package theme

// THE MIGRATION CHAIN'S CALL SITE (ledger L17).
//
// TestMigrationChainIsIdentityAtV1 drives migrateSidecar directly, so
// sidecar_read.go's `migrateSidecar(s.Format, doc)` was deletable with the whole
// suite green — the same shape [overrides] shipped in for four waves, and armed to
// fire on exactly the day it matters: every migration test would pass, and no file
// would ever be migrated.
//
// It could not be tested before because the chain was a fixed-length ARRAY
// (`[...]sidecarMigration{}` is a [0]T) bounded by ThemeFormatVersion, so nothing
// could install a probe step and the loop could never enter at v1. It is a slice
// bounded by its own length now; both halves of that change are pinned below.

import (
	"strings"
	"testing"
)

// migrateProbeMarker is the section a probe step writes. Chosen inside the reserved
// third-party namespace so the reader PRESERVES it verbatim rather than refusing or
// stripping it — the probe must be visible in the document the parse produces, not
// swallowed by the very reader under test.
const migrateProbeMarker = VendorSectionPrefix + "migrateprobe"

// installMigrationProbe appends one counting step to the chain and restores the
// shipped (empty) chain when the test ends. Returns the counter.
//
// t.Cleanup rather than a returned restore func: the chain is a package-level var,
// so a t.Fatal between install and restore would leave every later test in this
// package running a mutated grammar.
func installMigrationProbe(t *testing.T) *int {
	t.Helper()
	saved := sidecarMigrations
	n := 0
	sidecarMigrations = append(append([]sidecarMigration(nil), saved...),
		func(d *INIDoc) error {
			n++
			d.Set(migrateProbeMarker, "ran", "1")
			return nil
		})
	t.Cleanup(func() { sidecarMigrations = saved })
	return &n
}

// TestParseSidecarRunsTheMigrationChain is the call-site gate.
//
// THE MUTATION IT ANSWERS TO: replace `migrateSidecar(s.Format, doc)` in
// sidecar_read.go with `_ = s.Format`. The probe then never runs, the marker never
// appears, and this fails — where every other migration test still passes.
func TestParseSidecarRunsTheMigrationChain(t *testing.T) {
	ran := installMigrationProbe(t)

	src := []byte("; kept\n[theme]\nformat = 1\nname = Probe\n")
	sc, err := ParseSidecar(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *ran != 1 {
		t.Fatalf("the migration chain ran %d times during ParseSidecar, want exactly 1 — the reader "+
			"is not invoking it, so on the day the first real migration lands every migration test "+
			"passes and no file is ever migrated", *ran)
	}
	// It ran against the document the rest of the parse then walks, which is the
	// whole reason the version is read first.
	out := string(sc.doc.Bytes())
	if !strings.Contains(out, migrateProbeMarker) {
		t.Errorf("the probe's write is not in the parsed document:\n%s", out)
	}
	// And the untouched half of the file is still byte-for-byte what it was — a
	// migration is an edit through INIDoc, not a rebuild.
	if !strings.HasPrefix(out, "; kept\n[theme]\nformat = 1\nname = Probe\n") {
		t.Errorf("the migration rewrote the original lines:\n%s", out)
	}
}

// TestMigrationChainNeverRunsForwardOfThisBuild pins the rule that makes "your
// friend upgraded, you did not" a non-event: a file NEWER than this build is left
// exactly as it is. With a probe installed, a format-99 document must still see
// zero steps.
func TestMigrationChainNeverRunsForwardOfThisBuild(t *testing.T) {
	ran := installMigrationProbe(t)
	if _, err := ParseSidecar([]byte("[theme]\nformat = 99\nname = Future\n")); err != nil {
		t.Fatalf("a future-format file must still parse: %v", err)
	}
	if *ran != 0 {
		t.Fatalf("the chain ran %d step(s) on a format-99 file, want 0 — migrating forward past this "+
			"build's grammar would rewrite a file it does not understand", *ran)
	}
}

// TestMigrationChainCoversEveryFormatVersion is the invariant migrateSidecar's
// length-based bound now rests on: one step per version gap. It is stated here as
// well as inside TestMigrationChainIsIdentityAtV1 because it stopped being a
// bookkeeping nicety the moment the loop started reading len(sidecarMigrations) —
// a chain shorter than the version count would silently stop migrating partway.
func TestMigrationChainCoversEveryFormatVersion(t *testing.T) {
	if len(sidecarMigrations) != ThemeFormatVersion-1 {
		t.Fatalf("the chain holds %d steps for format version %d — migrateSidecar walks it by LENGTH, "+
			"so a short chain means a file stops migrating partway with no error anywhere",
			len(sidecarMigrations), ThemeFormatVersion)
	}
	for i, step := range sidecarMigrations {
		if step == nil {
			t.Errorf("chain step %d (format %d → %d) is nil", i, i+1, i+2)
		}
	}
}
