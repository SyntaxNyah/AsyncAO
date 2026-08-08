package themepack

// THE TRANSPORT'S GATES.
//
// Every one of them drives the SHIPPED entry point — Inspect, Extract, Write,
// CopyForEditing — against a real archive on a real temp directory. Nothing here
// re-implements a guard in order to check it: a mirror of the zip-slip rule would
// stay green with safepath ripped out, which is the exact shape rule 11 forbids.
//
// The hostile corpus is built in-process rather than checked in, because a zip
// carrying `../../evil.dll` is a file that some scanner somewhere will one day
// delete out of the repository, and a security gate that can silently stop
// existing is not one.

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// packBuilder writes a zip entry by entry, so a test can express exactly the
// hostile shape it is about.
type packBuilder struct {
	t   *testing.T
	buf *os.File
	zw  *zip.Writer
}

func newPack(t *testing.T, name string) *packBuilder {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	return &packBuilder{t: t, buf: f, zw: zip.NewWriter(f)}
}

// add writes one ordinary file.
func (b *packBuilder) add(name, body string) *packBuilder {
	b.t.Helper()
	w, err := b.zw.Create(name)
	if err != nil {
		b.t.Fatalf("adding %q: %v", name, err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		b.t.Fatalf("writing %q: %v", name, err)
	}
	return b
}

// addMode writes an entry with an explicit file mode — how a symlink gets into
// an archive.
func (b *packBuilder) addMode(name, body string, mode os.FileMode) *packBuilder {
	b.t.Helper()
	fh := &zip.FileHeader{Name: name, Method: zip.Store}
	fh.SetMode(mode)
	w, err := b.zw.CreateHeader(fh)
	if err != nil {
		b.t.Fatalf("adding %q: %v", name, err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		b.t.Fatalf("writing %q: %v", name, err)
	}
	return b
}

// addDeclared writes an entry whose CENTRAL DIRECTORY LIES about its size. It is
// how a zip bomb is built and how "Inspect never decompresses" is proved: the
// payload here is not a valid deflate stream, so anything that opened it would
// fail loudly.
func (b *packBuilder) addDeclared(name string, declared uint64, payload string) *packBuilder {
	b.t.Helper()
	fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
	fh.SetMode(0o644)
	fh.UncompressedSize64 = declared
	fh.CompressedSize64 = uint64(len(payload))
	fh.CRC32 = crc32.ChecksumIEEE([]byte(payload))
	w, err := b.zw.CreateRaw(fh)
	if err != nil {
		b.t.Fatalf("adding %q: %v", name, err)
	}
	if _, err := w.Write([]byte(payload)); err != nil {
		b.t.Fatalf("writing %q: %v", name, err)
	}
	return b
}

// done closes the archive and returns its path.
func (b *packBuilder) done() string {
	b.t.Helper()
	if err := b.zw.Close(); err != nil {
		b.t.Fatalf("closing the fixture: %v", err)
	}
	name := b.buf.Name()
	if err := b.buf.Close(); err != nil {
		b.t.Fatalf("closing the fixture file: %v", err)
	}
	return name
}

// themeFolder builds a small but complete theme on disk.
func themeFolder(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mustWrite(t, filepath.Join(dir, "courtroom_design.ini"), "courtroom = 0, 0, 256, 192\nviewport = 0, 0, 256, 192\n")
	mustWrite(t, filepath.Join(dir, "asyncao_theme.ini"), "[theme]\nformat = 1\nname = Paper\nauthor = Nyah\nlicense = CC-BY-4.0\n")
	mustWrite(t, filepath.Join(dir, "fonts", "Court.ttf"), "not really a font")
	mustWrite(t, filepath.Join(dir, "asyncao", "media", "plate.png"), "not really a png")
	return dir
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// emptyRoot is a fresh themes/ directory to import into.
func emptyRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "themes")
}

// The two halves of the consent-sheet spoof: the sidecar a sheet would happily
// describe, and a forged one claiming a different author under a licence that
// forbids what the sheet said was allowed.
//
// THEY DIFFER ONLY IN THE CASE OF THEIR FILE NAME, which is the whole trick —
// two entries in an archive, one file on Windows and macOS, last one wins.
const (
	honestSidecarName = theme.SidecarFileName
	forgedSidecarName = "ASYNCAO_THEME.INI"
	honestSidecar     = "[theme]\nformat = 1\nname = Friendly Paper\nauthor = A Friend\nlicense = CC-BY 4.0\n"
	forgedSidecar     = "[theme]\nformat = 1\nname = SOMETHING ELSE\nauthor = nobody\nlicense = All rights reserved\n"
)

// ---------------------------------------------------------------------------
// The hostile corpus — every refusal, and every one of them leaves no trace
// ---------------------------------------------------------------------------

// TestPackRefusesEveryHostileShape drives Extract against the archives an
// attacker actually sends, and asserts BOTH halves each time: the refusal
// happened, and the destination is still empty.
//
// The second half is the one that matters. A guard that refuses AFTER writing
// four of six entries has already put a stranger's files on the disk.
func TestPackRefusesEveryHostileShape(t *testing.T) {
	cases := []struct {
		name string
		pack func(*testing.T) string
		want error
		why  string
		// quotes are strings the refusal must NAME. Design §3.3 asks every refusal to
		// name its offender, and for the case-collision row that means the PAIR: an
		// error that quotes one spelling twice tells the user to rename one of two
		// identically-named files, which is not an instruction anybody can follow.
		quotes []string
	}{
		{
			name: "zip slip",
			pack: func(t *testing.T) string {
				return newPack(t, "slip.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add("paper/../../evil.dll", "pwned").done()
			},
			want: ErrPackUnsafe,
			why:  "a '..' segment is how an archive writes outside the folder it was extracted into",
		},
		{
			name: "absolute entry name",
			pack: func(t *testing.T) string {
				return newPack(t, "abs.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add("/etc/passwd", "pwned").done()
			},
			want: ErrPackUnsafe,
			why:  "an absolute name ignores the destination entirely",
		},
		{
			name: "windows drive-relative name",
			pack: func(t *testing.T) string {
				return newPack(t, "drive.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add(`paper/C:evil.dll`, "pwned").done()
			},
			want: ErrPackUnsafe,
			why:  "a ':' is a drive-relative path or an alternate data stream on Windows",
		},
		{
			name: "backslash traversal",
			pack: func(t *testing.T) string {
				return newPack(t, "back.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add(`paper\..\..\evil.dll`, "pwned").done()
			},
			want: ErrPackUnsafe,
			why: "zip names are '/'-separated by the format, but the name is attacker metadata and " +
				"Windows honours '\\' — a '/'-only check walks straight past this",
		},
		{
			name: "one entry over the per-file cap",
			pack: func(t *testing.T) string {
				return newPack(t, "fat.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					addDeclared("paper/huge.png", PackEntryBytesCap+1, "x").done()
			},
			want: ErrPackCap,
			why:  "one entry bigger than the per-file cap",
		},
		{
			name: "zip bomb",
			pack: func(t *testing.T) string {
				b := newPack(t, "bomb.aotheme").add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n")
				// Four entries each just under the per-file cap: every one is legal
				// alone, and together they are past the TOTAL. That is the shape a
				// per-file-only check misses.
				for i := 0; i < 4; i++ {
					b.addDeclared("paper/pad"+string(rune('a'+i))+".png", PackEntryBytesCap, "x")
				}
				return b.done()
			},
			want: ErrPackCap,
			why:  "the uncompressed total is what fills a disk, and it is knowable from the directory alone",
		},
		{
			name: "too many entries",
			pack: func(t *testing.T) string {
				b := newPack(t, "many.aotheme")
				for i := 0; i <= PackEntryCap; i++ {
					b.add("paper/f"+strings.Repeat("0", i%3)+itoa(i)+".png", "x")
				}
				return b.done()
			},
			want: ErrPackCap,
			why:  "an archive with thousands of members is a filesystem, not a theme",
		},
		{
			name: "an over-long path",
			pack: func(t *testing.T) string {
				return newPack(t, "long.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add("paper/"+strings.Repeat("n", PackPathLenCap), "x").done()
			},
			want: ErrPackCap,
			why:  "a path this build cannot extract anywhere useful is refused by name, not per-file half way through",
		},
		{
			name: "too deep",
			pack: func(t *testing.T) string {
				return newPack(t, "deep.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add("paper/"+strings.Repeat("d/", packMaxDepth)+"x.png", "x").done()
			},
			want: ErrPackCap,
			why:  "a pathological tree, or a link loop the symlink skip somehow missed, must not walk forever",
		},
		{
			name: "two top-level folders",
			pack: func(t *testing.T) string {
				return newPack(t, "two.aotheme").
					add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
					add("ink/asyncao_theme.ini", "[theme]\nformat = 1\n").done()
			},
			want: ErrPackNotABundle,
			why:  "a bundle is ONE theme; picking one of them for the user would be guessing",
		},
		{
			name: "two entries that differ only in case",
			pack: func(t *testing.T) string {
				return newPack(t, "spoof.aotheme").
					add("paper/"+honestSidecarName, honestSidecar).
					add("paper/"+forgedSidecarName, forgedSidecar).done()
			},
			want: ErrPackNotABundle,
			why: "on Windows and macOS the two are ONE file, so the second silently replaces the first: " +
				"the consent sheet would read the honest sidecar's name, author and licence and the theme " +
				"that lands would declare the forged one's — and Files would count a file that is not there",
			// BOTH SPELLINGS, as the archive spells them. This pair is folded onto the
			// format's own name before it is claimed (canonicalSidecarRel), so a refusal
			// built from the post-fold path alone would quote "asyncao_theme.ini" twice
			// and lose the very evidence — the shouted spelling — that explains why an
			// archive that looked fine where it was made is refused here.
			quotes: []string{honestSidecarName, forgedSidecarName},
		},
		{
			name: "not a zip at all",
			pack: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "screenshot.aotheme")
				mustWrite(t, p, "\x89PNG\r\n\x1a\n this is a screenshot")
				return p
			},
			want: ErrPackNotABundle,
			why:  "the extension is never the test — the bytes are (the assets.Sniff doctrine)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pack := tc.pack(t)
			root := emptyRoot(t)
			_, err := Extract(pack, root)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Extract = %v, want %v\n%s", err, tc.want, tc.why)
			}
			if err.Error() == tc.want.Error() {
				t.Errorf("the refusal is the bare sentinel %q and names no offender — "+
					"design §3.3: name the offender, name the limit, offer the fix", err)
			}
			for _, want := range tc.quotes {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v — a refusal the user cannot act on "+
						"is a refusal they will blame on us", want, err)
				}
			}
			assertNothingWritten(t, root)
			// And the same shape is refused by INSPECT, which is what makes the
			// consent sheet safe to render on anything a user drops.
			if _, err := Inspect(pack); !errors.Is(err, tc.want) {
				t.Errorf("Inspect = %v, want %v — a refusal Extract makes and Inspect does not "+
					"is a sheet that offers to import something we will not import", err, tc.want)
			}
		})
	}
}

// assertNothingWritten fails if the destination holds anything at all.
func assertNothingWritten(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("reading the destination: %v", err)
	}
	if len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a refused bundle left %v behind — a refusal after a partial write has already put "+
			"a stranger's files on the disk", names)
	}
}

// TestPackSkipsSymlinks is separate from the refusals because a link is SKIPPED,
// not refused: an archive that happens to carry one is not hostile, it was made
// on a unix box. The theme still imports; the link does not become a file.
func TestPackSkipsSymlinks(t *testing.T) {
	pack := newPack(t, "link.aotheme").
		add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
		addMode("paper/secret.png", "../../../../../../etc/passwd", os.ModeSymlink|0o777).
		done()
	root := emptyRoot(t)
	m, err := Extract(pack, root)
	if err != nil {
		t.Fatalf("Extract refused an archive for carrying a link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Dir, "secret.png")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the link was materialised — a link at fonts/x.ttf pointing at ~/.ssh/id_rsa would " +
			"then be read and uploaded as a texture")
	}
	if len(m.Warnings) == 0 {
		t.Error("the skip was silent; nothing about an import may be silent")
	}
}

// TestInspectReadsOnlyTheCentralDirectory is the property the whole consent flow
// rests on: describing a stranger's archive must not run its decompressor.
//
// The fixture's payload is NOT a valid deflate stream while its directory entry
// is well-formed, so anything that opened it would fail. Inspect must succeed.
func TestInspectReadsOnlyTheCentralDirectory(t *testing.T) {
	pack := newPack(t, "opaque.aotheme").
		add("paper/asyncao_theme.ini", "[theme]\nformat = 1\nname = Paper\n").
		addDeclared("paper/art.png", 4096, "this is not a deflate stream").
		done()
	m, err := Inspect(pack)
	if err != nil {
		t.Fatalf("Inspect opened an entry it should never have read: %v", err)
	}
	if m.Files != 2 {
		t.Errorf("Files = %d, want 2 — the census comes from the directory", m.Files)
	}
	if m.Bytes < 4096 {
		t.Errorf("Bytes = %d, want at least the 4096 the directory declares", m.Bytes)
	}
	if m.Name != "Paper" {
		t.Errorf("Name = %q — the ONE entry Inspect is allowed to decompress is the sidecar", m.Name)
	}
	// And extraction of the same archive fails, which is the proof the payload
	// really was unreadable and the test above was not passing by accident.
	if _, err := Extract(pack, emptyRoot(t)); err == nil {
		t.Error("Extract succeeded on an undecompressable payload — the fixture is not what this gate thinks")
	}
}

// TestTheConsentSheetDescribesTheFileThatLands is the anti-spoof gate.
//
// The two-drop line IS the whole consent surface of an import: it names the
// theme, its author and its licence, and the user's second drop is consent to
// exactly that. So the sheet and the disk must be the same sidecar — not the same
// FOLDER, the same FILE — and this drives both halves against the shipped API to
// prove it, rather than reasoning about entry names.
//
// It also pins Files against reality: a census that counts entries which collapse
// into each other on the recipient's filesystem promises a file that is not there.
func TestTheConsentSheetDescribesTheFileThatLands(t *testing.T) {
	cases := []struct {
		name string
		pack func(*testing.T) string
		// wantName is the [theme] name BOTH sides must end up agreeing on; "" is the
		// plain-AO2 case, where neither has anything to say.
		wantName string
		// namesSpelling asks the manifest to have warned about a folded sidecar name.
		// Nothing about an import may be silent.
		namesSpelling bool
	}{
		{
			name: "the format's own spelling",
			pack: func(t *testing.T) string {
				return newPack(t, "plain.aotheme").
					add("paper/"+honestSidecarName, honestSidecar).
					add("paper/courtroom_design.ini", "courtroom = 0, 0, 256, 192\n").done()
			},
			wantName: "Friendly Paper",
		},
		{
			name: "a sidecar spelled in the wrong case",
			pack: func(t *testing.T) string {
				return newPack(t, "shout.aotheme").
					add("paper/"+forgedSidecarName, honestSidecar).
					add("paper/courtroom_design.ini", "courtroom = 0, 0, 256, 192\n").done()
			},
			// SHOUTED, and it still has to agree: on Windows and macOS this file loads
			// as the theme's native tier, so a sheet that read nothing out of it would
			// promise "a plain AO2 theme with no name or licence" and install one that
			// declares both.
			wantName:      "Friendly Paper",
			namesSpelling: true,
		},
		{
			name: "no sidecar at all",
			pack: func(t *testing.T) string {
				return newPack(t, "ao2.aotheme").
					add("paper/courtroom_design.ini", "courtroom = 0, 0, 256, 192\n").done()
			},
			wantName: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pack := tc.pack(t)
			sheet, err := Inspect(pack) // what the user is shown, before anything is written
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			im, err := Extract(pack, emptyRoot(t))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			// WHAT THE CLIENT ITSELF WILL READ once the theme is picked — the same
			// loader, off the same folder, not a re-parse of the archive.
			sc, err := theme.LoadSidecar(filepath.Join(im.Dir, theme.SidecarFileName))
			if err != nil {
				t.Fatalf("the sidecar that landed will not load: %v", err)
			}
			var name, author, license string
			var format int
			if sc != nil {
				name, author, license, format = sc.Meta.Name, sc.Meta.Author, sc.Meta.License, sc.Format
			}
			if name != sheet.Name || author != sheet.Author || license != sheet.License || format != sheet.Format {
				t.Errorf("the sheet says name=%q author=%q license=%q format=%d and the theme that LANDED says "+
					"name=%q author=%q license=%q format=%d — a bundle that makes the consent line describe a "+
					"different sidecar than the one it installs is a provenance and licence spoof, and that line "+
					"is the only consent this import asks for",
					sheet.Name, sheet.Author, sheet.License, sheet.Format, name, author, license, format)
			}
			if name != tc.wantName {
				t.Errorf("the theme that landed is named %q, want %q — the fixture is not what this gate thinks", name, tc.wantName)
			}
			if got := len(treeOf(t, im.Dir)); got != sheet.Files {
				t.Errorf("the sheet promised %d files and %d landed — entries that collapse into one another on "+
					"disk make Files a count of something that is not there", sheet.Files, got)
			}
			if tc.namesSpelling && !warningNames(sheet, forgedSidecarName) {
				t.Errorf("the sidecar's name was folded onto %q and no warning said so: %v — "+
					"nothing about an import may be silent", theme.SidecarFileName, sheet.Warnings)
			}
		})
	}
}

// warningNames reports whether some manifest warning names the given string.
func warningNames(m *Manifest, want string) bool {
	for _, w := range m.Warnings {
		if strings.Contains(w, want) {
			return true
		}
	}
	return false
}

// TestTheExporterRefusesWhatTheImporterRefuses is write.go's symmetry claim for
// the one-file-per-name rule: an exporter looser than the importer ships bundles
// only the author's FILESYSTEM can open.
//
// It can only run where the pair can exist. On Windows and macOS the second
// os.WriteFile overwrites the first, so there is nothing to refuse — the test
// says so out loud rather than pretending to have checked.
func TestTheExporterRefusesWhatTheImporterRefuses(t *testing.T) {
	dir := themeFolder(t, "paper")
	mustWrite(t, filepath.Join(dir, forgedSidecarName), forgedSidecar)
	if len(treeOf(t, dir)) != 5 {
		t.Skip("this filesystem folds case, so a folder cannot hold the colliding pair this gate is about")
	}
	if _, err := Write(dir, filepath.Join(t.TempDir(), "paper"+PackExt), nil); !errors.Is(err, ErrPackNotABundle) {
		t.Fatalf("Write = %v, want %v — a bundle built here would be refused on import, which is the "+
			"asymmetry write.go's header promises does not exist", err, ErrPackNotABundle)
	}
}

// ---------------------------------------------------------------------------
// The round trip — the whole point of the wave
// ---------------------------------------------------------------------------

// TestExportThenImportIsIdentical is requirement 4 stated as a test: a theme
// bundled and then imported is byte-for-byte the theme that was bundled.
func TestExportThenImportIsIdentical(t *testing.T) {
	src := themeFolder(t, "paper")
	dest := filepath.Join(t.TempDir(), "paper"+PackExt)
	wm, err := Write(src, dest, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if wm.Files != 4 {
		t.Errorf("bundled %d files, want the folder's 4 — a bundler that drops what it does not "+
			"recognise breaks the re-share it exists for", wm.Files)
	}
	if wm.ZipBytes <= 0 {
		t.Error("ZipBytes is 0 — it is the number that decides whether a theme is shareable at all")
	}
	root := emptyRoot(t)
	im, err := Extract(dest, root)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if im.Root != "paper" {
		t.Errorf("imported as %q, want the folder name it was exported from", im.Root)
	}
	assertTreesEqual(t, src, im.Dir)
}

// assertTreesEqual compares two folders file for file, byte for byte.
func assertTreesEqual(t *testing.T, a, b string) {
	t.Helper()
	got := treeOf(t, a)
	want := treeOf(t, b)
	if len(got) != len(want) {
		t.Fatalf("%d files on one side, %d on the other:\n%v\n%v", len(got), len(want), keysOf(got), keysOf(want))
	}
	for rel, body := range got {
		other, ok := want[rel]
		if !ok {
			t.Errorf("%s is missing from the round trip", rel)
			continue
		}
		if body != other {
			t.Errorf("%s differs across the round trip", rel)
		}
	}
}

func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestImportCollisionSuffixesRatherThanOverwrites: re-shared themes are renamed
// constantly, so "paper" arriving twice is two themes far more often than it is
// one theme twice — and the cost of guessing wrong the other way is somebody
// else's work, silently gone.
func TestImportCollisionSuffixesRatherThanOverwrites(t *testing.T) {
	src := themeFolder(t, "paper")
	dest := filepath.Join(t.TempDir(), "paper"+PackExt)
	if _, err := Write(src, dest, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	root := emptyRoot(t)
	first, err := Extract(dest, root)
	if err != nil {
		t.Fatalf("first Extract: %v", err)
	}
	// Mark the first import so an overwrite is detectable.
	marker := filepath.Join(first.Dir, "MINE.txt")
	mustWrite(t, marker, "do not overwrite me")

	second, err := Extract(dest, root)
	if err != nil {
		t.Fatalf("second Extract: %v", err)
	}
	if second.Root == first.Root {
		t.Fatalf("both imports landed on %q — the second overwrote the first", first.Root)
	}
	if !strings.Contains(second.Root, "(2)") {
		t.Errorf("the second import is %q, want a \" (2)\" suffix", second.Root)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the first import's own file is gone: %v", err)
	}
}

// TestFontWithoutLicenceFileIsWarnedOnExport — OFL requires the licence to travel
// with the face, so the person re-sharing the bundle is the one who would be
// breaking the terms. Never a silent bundle.
func TestFontWithoutLicenceFileIsWarnedOnExport(t *testing.T) {
	src := themeFolder(t, "paper") // fonts/Court.ttf, no licence beside it
	m, err := Write(src, filepath.Join(t.TempDir(), "paper"+PackExt), nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(m.Fonts) != 1 {
		t.Fatalf("found %d fonts, want the folder's 1", len(m.Fonts))
	}
	if m.Fonts[0].LicenseFile != "" {
		t.Errorf("claimed a licence file at %q that is not there", m.Fonts[0].LicenseFile)
	}
	if !warnsAbout(m, "Court.ttf") {
		t.Errorf("the export warns nothing about a font with no licence; Warnings = %v", m.Warnings)
	}
	// And with one beside it, the warning goes away — otherwise the warning is
	// noise and gets ignored, which is the same as not having one.
	// "Court-OFL.txt" and not "Court-LICENSE.txt": the SIL Open Font License is
	// overwhelmingly shipped under the former, which contains neither "license" nor
	// "licence" — a probe that missed it would warn about every correctly-licensed
	// OFL font, and a warning that fires when nothing is wrong is one everybody
	// learns to skip.
	mustWrite(t, filepath.Join(src, "fonts", "Court-OFL.txt"), "SIL Open Font License")
	m2, err := Write(src, filepath.Join(t.TempDir(), "paper2"+PackExt), nil)
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if m2.Fonts[0].LicenseFile == "" {
		t.Error("a licence file beside the face was not found")
	}
	if warnsAbout(m2, "Court.ttf") {
		t.Errorf("still warning about a font whose licence travels with it: %v", m2.Warnings)
	}
}

func warnsAbout(m *Manifest, needle string) bool {
	for _, w := range m.Warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}

// TestFlatArchiveImportsUnderTheFileName — a friend who selected the files inside
// a theme and pressed "compress" produces a flat zip. Refusing it would fail the
// one user story the wave exists for.
func TestFlatArchiveImportsUnderTheFileName(t *testing.T) {
	pack := newPack(t, "Umineko Meta World.zip").
		add("asyncao_theme.ini", "[theme]\nformat = 1\nname = Umineko\n").
		add("courtroom_design.ini", "courtroom = 0, 0, 256, 192\n").
		add("fonts/Serif.ttf", "face").
		done()
	root := emptyRoot(t)
	m, err := Extract(pack, root)
	if err != nil {
		t.Fatalf("a flat archive was refused: %v", err)
	}
	if m.Root != "umineko meta world" {
		t.Errorf("Root = %q, want the sanitized archive name", m.Root)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "asyncao_theme.ini")); err != nil {
		t.Errorf("the sidecar did not land at the theme's root: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Copy for editing
// ---------------------------------------------------------------------------

// TestCopyForEditingLeavesTheOriginalUntouched is the promise "editing makes your
// own copy" rests on. It hashes the source tree before and after.
func TestCopyForEditingLeavesTheOriginalUntouched(t *testing.T) {
	src := themeFolder(t, "paper")
	before := treeOf(t, src)
	root := emptyRoot(t)
	m, err := CopyForEditing(src, root, "paper (edited)")
	if err != nil {
		t.Fatalf("CopyForEditing: %v", err)
	}
	if diff := treeOf(t, src); len(diff) != len(before) {
		t.Fatalf("the ORIGINAL grew or shrank: %d files, was %d", len(diff), len(before))
	} else {
		for rel, body := range before {
			if diff[rel] != body {
				t.Errorf("the ORIGINAL's %s was modified by a copy — the highest-blast-radius risk in the feature", rel)
			}
		}
	}
	if m.Root != "paper (edited)" {
		t.Errorf("Root = %q, want the name that was asked for", m.Root)
	}
	// NOTHING IS SKIPPED: a theme's unknown files are its author's business.
	if len(treeOf(t, m.Dir)) != len(before) {
		t.Errorf("the copy holds %d files, the original %d", len(treeOf(t, m.Dir)), len(before))
	}
	// PROVENANCE: credit that survives re-share.
	body, err := os.ReadFile(filepath.Join(m.Dir, "asyncao_theme.ini"))
	if err != nil {
		t.Fatalf("reading the copy's sidecar: %v", err)
	}
	for _, key := range []string{"derived_from", "derived_at", "derived_hash"} {
		if !strings.Contains(string(body), key) {
			t.Errorf("the copy does not record %s — a copy that forgot where it came from is how an "+
				"ecosystem loses its authors\n%s", key, body)
		}
	}
	if !strings.Contains(string(body), "derived_from = paper") {
		t.Errorf("derived_from does not name the source folder:\n%s", body)
	}
}

// ---------------------------------------------------------------------------
// Identity
// ---------------------------------------------------------------------------

// TestSanitizeNameIsTotal drives the ONE place a folder name is invented. Every
// input must yield either a name safepath accepts or "" — never something in
// between, because "" is the caller's refusal and anything else becomes a
// directory.
func TestSanitizeNameIsTotal(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"Paper", "paper", "lowercased: two of three platforms have case-insensitive filesystems"},
		{"Umineko Meta World", "umineko meta world", "spaces are legal in a folder name"},
		{"..", "", "a traversal segment can never become a folder"},
		{"../../etc", "etc", "the traversal characters are dropped, not honoured"},
		{".hidden", "hidden", "a leading dot hides the folder on unix"},
		{"C:evil", "cevil", "a colon is a drive or an alternate data stream on Windows"},
		{"   ", "", "whitespace alone is not a name"},
		{"", "", "the empty string stays empty"},
		{"-  -", "", "punctuation alone is not a name"},
		{strings.Repeat("a", NameRuneCap+40), strings.Repeat("a", NameRuneCap), "bounded (hard rule 4)"},
	}
	for _, tc := range cases {
		if got := SanitizeName(tc.in); got != tc.want {
			t.Errorf("SanitizeName(%q) = %q, want %q — %s", tc.in, got, tc.want, tc.why)
		}
	}
}

// ---------------------------------------------------------------------------
// The lying central directory — the one number an attacker gets to choose
// ---------------------------------------------------------------------------

// addUnderDeclared writes an entry whose central directory UNDER-states its size:
// a real deflate stream of body, advertised as `declared` bytes.
//
// This is the other half of addDeclared. That one over-states, which is how a zip
// bomb passes a naive extractor; this one under-states, which is how a bomb passes
// an extractor that checks the DECLARATION and then trusts it. Go's own reader
// only compares the two at EOF, so a consumer that stops early — which any bounded
// one must — never finds out.
func (b *packBuilder) addUnderDeclared(name string, declared uint64, body []byte) *packBuilder {
	b.t.Helper()
	var packed bytes.Buffer
	zwr, err := flate.NewWriter(&packed, flate.BestCompression)
	if err != nil {
		b.t.Fatalf("compressing %q: %v", name, err)
	}
	if _, err := zwr.Write(body); err != nil {
		b.t.Fatalf("compressing %q: %v", name, err)
	}
	if err := zwr.Close(); err != nil {
		b.t.Fatalf("compressing %q: %v", name, err)
	}
	fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
	fh.SetMode(0o644)
	fh.UncompressedSize64 = declared // the lie
	fh.CompressedSize64 = uint64(packed.Len())
	fh.CRC32 = crc32.ChecksumIEEE(body)
	w, err := b.zw.CreateRaw(fh)
	if err != nil {
		b.t.Fatalf("adding %q: %v", name, err)
	}
	if _, err := w.Write(packed.Bytes()); err != nil {
		b.t.Fatalf("writing %q: %v", name, err)
	}
	return b
}

// TestTheDeclaredSizeIsAHardCeilingOnWhatAnEntryCanDeliver pins the STDLIB
// invariant the whole extraction budget rests on.
//
// WHY THIS GATE EXISTS. Every cap Inspect applies is decided from the CENTRAL
// DIRECTORY, which is attacker-controlled metadata — that is the point of Inspect
// and it is right, because refusing a bomb before decompressing a byte is only
// possible from metadata. It is only a bound on what reaches the DISK, though,
// because archive/zip refuses on the read that would take an entry past its own
// declared UncompressedSize64 (reader.go, `if r.nread > r.f.UncompressedSize64`).
// Take that away and an archive declaring a few kilobytes could deliver 64 MiB per
// entry — every entry individually legal against PackEntryBytesCap, the aggregate
// 512 × 64 MiB = 32 GiB, and Extract's only defence a total it checked against the
// attacker's own arithmetic.
//
// So the invariant is DRIVEN, not assumed: a forged under-declaring archive goes
// through the real reader, and the read must be refused. A future Go that relaxed
// the check would fail here — in the package that depends on it, with this comment
// attached — rather than silently un-bounding the extractor. That is exactly why
// it asserts on the reader rather than on Extract: Extract merely inherits the
// property, and a gate on the inheritor cannot say which ancestor stopped holding.
func TestTheDeclaredSizeIsAHardCeilingOnWhatAnEntryCanDeliver(t *testing.T) {
	const realBytes = 64 << 10
	body := bytes.Repeat([]byte("A"), realBytes)
	pack := newPack(t, "liar.aotheme").
		add("paper/asyncao_theme.ini", "[theme]\nformat = 1\n").
		addUnderDeclared("paper/pad.bin", 1, body). // says one byte, carries 64 KiB
		done()

	// The census PASSES, which is the premise: from the directory alone this is a
	// two-entry, tiny theme. If this ever starts failing, the fixture has stopped
	// being the attack it is named after.
	m, err := Inspect(pack)
	if err != nil {
		t.Fatalf("the fixture is no longer the attack: Inspect refused it up front (%v)", err)
	}
	if m.Bytes >= realBytes {
		t.Fatalf("the fixture declares %d bytes and carries %d — it has to under-state to prove "+
			"anything", m.Bytes, realBytes)
	}

	zr, err := openPack(pack)
	if err != nil {
		t.Fatalf("opening the fixture: %v", err)
	}
	defer zr.Close()
	var f *zip.File
	for _, cand := range zr.r.File {
		if strings.HasSuffix(cand.Name, "pad.bin") {
			f = cand
		}
	}
	if f == nil {
		t.Fatal("the fixture lost its lying entry")
	}
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("opening the entry: %v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err == nil {
		t.Fatalf("archive/zip handed out %d bytes for an entry that declared %d and returned no error — "+
			"the central-directory total Inspect verifies is no longer a bound on what Extract writes, "+
			"and themepack needs its own running total in writeEntries", n, f.UncompressedSize64)
	}
	if uint64(n) > f.UncompressedSize64 {
		t.Errorf("archive/zip handed out %d bytes before refusing an entry that declared %d — the "+
			"overshoot is what lands on the disk, so the bound has to hold per read, not per entry",
			n, f.UncompressedSize64)
	}
	// AND THE WHOLE IMPORT REFUSES, because a truncated texture is not a theme.
	root := emptyRoot(t)
	if _, err := Extract(pack, root); err == nil {
		t.Error("Extract accepted an archive whose entry could not be read to its declared length — " +
			"half a file written under the author's name is worse than no import")
	}
	assertNothingWritten(t, root)
}

// itoa without importing strconv for one call in a table.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
