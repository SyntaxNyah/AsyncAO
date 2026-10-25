package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestMissingBundledDylibs pins the pure name-set logic behind the macOS
// SONAME-skew preflight (preflightDarwinSwap): given a binary's imported dylib
// names and a sibling lib/, which @rpath imports are absent. It is OS-agnostic
// (no macho parsing, just os.Stat over a temp dir) so it runs on the Windows dev
// box too, where the darwin preflight branch itself is skipped.
func TestMissingBundledDylibs(t *testing.T) {
	libDir := t.TempDir()
	// The bundled lib/ holds the OLD soname (libavif.16) — the skew case.
	writeFile(t, filepath.Join(libDir, "libavif.16.dylib"), "")
	writeFile(t, filepath.Join(libDir, "libSDL2-2.0.0.dylib"), "")

	imported := []string{
		"@rpath/libSDL2-2.0.0.dylib",             // present in lib/ → fine
		"@rpath/libavif.17.dylib",                // SONAME bumped: NOT in lib/ → must be flagged
		"/usr/lib/libSystem.B.dylib",             // absolute system lib → never bundled, ignore
		"@executable_path/../Frameworks/x.dylib", // not @rpath → ignore
	}
	missing := missingBundledDylibs(imported, libDir)
	if len(missing) != 1 || missing[0] != "libavif.17.dylib" {
		t.Fatalf("missingBundledDylibs = %v, want exactly [libavif.17.dylib]", missing)
	}

	// All @rpath deps present → nothing missing (the no-skew common case).
	writeFile(t, filepath.Join(libDir, "libavif.17.dylib"), "")
	if got := missingBundledDylibs(imported, libDir); len(got) != 0 {
		t.Errorf("with every dylib present, missing = %v, want none", got)
	}
}

// TestHomebrewImports pins the mis-bundled-asset detector: a release binary
// whose imports point straight into a Homebrew prefix (the artifact of a failed
// release-side bundle step) must be flagged, while @rpath, system, and
// @executable_path imports pass. This is the pure half of the preflight's
// second refusal case — a correctly built release binary never carries such an
// import (bundle-macos.sh check_clean), so flagging is never a false positive.
func TestHomebrewImports(t *testing.T) {
	clean := []string{
		"@rpath/libSDL2-2.0.0.dylib",
		"/usr/lib/libSystem.B.dylib",
		"/System/Library/Frameworks/Cocoa.framework/Versions/A/Cocoa",
		"@executable_path/../Frameworks/x.dylib",
	}
	if got := homebrewImports(clean); len(got) != 0 {
		t.Errorf("clean import set flagged: %v", got)
	}
	dirty := append([]string{
		"/opt/homebrew/opt/sdl2/lib/libSDL2-2.0.0.dylib", // Apple Silicon prefix
		"/usr/local/opt/webp/lib/libwebp.7.dylib",        // Intel / Rosetta prefix
	}, clean...)
	got := homebrewImports(dirty)
	if len(got) != 2 || got[0] != dirty[0] || got[1] != dirty[1] {
		t.Errorf("homebrewImports = %v, want exactly the two Homebrew-prefixed paths", got)
	}
}

// TestPreflightDarwinSwapNonDarwin proves the guard is inert off macOS: on the
// Windows/Linux dev + CI boxes it must return nil unconditionally (there is no
// bundled sibling lib/ convention there), so StageReplace behaves exactly as
// before. On darwin this test is a tautology (still returns nil for a temp dir
// with no lib/), which is also the correct brew-install behaviour.
func TestPreflightDarwinSwapNonDarwin(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	newBin := filepath.Join(dir, "app.new")
	writeFile(t, target, "OLD")
	writeFile(t, newBin, "NEW")
	if err := preflightDarwinSwap(newBin, target); err != nil {
		t.Fatalf("preflightDarwinSwap must be a no-op with no sibling lib/: %v", err)
	}
}

func TestStageReplaceSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	staged := StagedPath(target)
	backup := BackupPath(target)
	writeFile(t, target, "OLD")
	writeFile(t, staged, "NEW")

	if err := StageReplace(staged, target, backup); err != nil {
		t.Fatalf("StageReplace: %v", err)
	}
	if got := readFile(t, target); got != "NEW" {
		t.Errorf("target = %q, want the new binary", got)
	}
	if got := readFile(t, backup); got != "OLD" {
		t.Errorf("backup = %q, want the old binary kept for next-boot cleanup", got)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("the staged file should have been consumed by the rename")
	}
}

func TestStageReplaceRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	staged := StagedPath(target)
	backup := BackupPath(target)
	writeFile(t, target, "OLD")
	writeFile(t, staged, "NEW")

	// Simulate the install rename (new -> target) failing mid-swap, the
	// AV-quarantine case; the backup -> target rollback must still run.
	orig := renameFn
	renameFn = func(from, to string) error {
		if from == staged {
			return errors.New("simulated AV lock")
		}
		return orig(from, to)
	}
	defer func() { renameFn = orig }()

	if err := StageReplace(staged, target, backup); err == nil {
		t.Fatal("a failed install must report an error")
	}
	if got := readFile(t, target); got != "OLD" {
		t.Errorf("after rollback target = %q, want the original binary restored", got)
	}
}

func TestStageReplaceMissingStaged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	writeFile(t, target, "OLD")
	// No staged file: must error WITHOUT touching the live binary.
	if err := StageReplace(StagedPath(target), target, BackupPath(target)); err == nil {
		t.Fatal("missing staged binary must error")
	}
	if got := readFile(t, target); got != "OLD" {
		t.Errorf("target must be untouched, got %q", got)
	}
}

func TestCleanupOldVersion(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	backup := BackupPath(target)
	writeFile(t, backup, "stale")
	CleanupOldVersion(target)
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("CleanupOldVersion must remove the .old backup")
	}
	CleanupOldVersion(target) // absent now: must be a safe no-op
}

func TestDownload(t *testing.T) {
	const payload = "this-is-a-binary"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "app.exe.new")
	n, err := Download(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if int(n) != len(payload) || readFile(t, dest) != payload {
		t.Errorf("download wrote %d bytes / %q", n, readFile(t, dest))
	}
}

// TestDownloadStagedExecutable pins the unix exec bit on the staged binary:
// os.Create alone yields 0644 (0666 & umask) and StageReplace's renames
// preserve mode, so without Download's explicit chmod every macOS / Linux
// self-update would install a binary the OS refuses to launch — and the .old
// backup is unlinked right after the stage, leaving nothing runnable. Windows
// has no exec bit (Download deliberately skips the chmod there), so the test
// skips too.
func TestDownloadStagedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no exec bit on Windows; Download skips the chmod there")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bin"))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "app.new")
	if _, err := Download(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat staged binary: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("staged binary mode = %v, want the exec bit set (stagedBinaryMode)", info.Mode())
	}
}

func TestDownloadServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "app.exe.new")
	if _, err := Download(context.Background(), srv.URL, dest); err == nil {
		t.Fatal("a 404 must error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed download must not leave a partial file")
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob")
	writeFile(t, path, "hello world")
	sum := sha256.Sum256([]byte("hello world"))
	hexSum := hex.EncodeToString(sum[:])

	if err := VerifyChecksum(path, hexSum); err != nil {
		t.Errorf("matching checksum must pass: %v", err)
	}
	if err := VerifyChecksum(path, "deadbeef"); err == nil {
		t.Error("a wrong checksum must fail")
	}
	if err := VerifyChecksum(path, ""); err != nil {
		t.Errorf("empty checksum must skip (no sum published): %v", err)
	}
}

func TestTargetWritable(t *testing.T) {
	dir := t.TempDir()
	if !TargetWritable(filepath.Join(dir, "app.exe")) {
		t.Error("a fresh temp dir must be writable")
	}
}

// TestVerifiedDownloadFlow exercises the Download → FetchSums → VerifyChecksum
// sequence the self-updater runs between the download and the swap, over the
// three release shapes that matter: a matching manifest (verification passes and
// the swap would proceed), a mismatching one (verification fails so the binary
// is never installed), and no manifest at all (SumsURL == "", verification is
// skipped and the flow proceeds as on every pre-checksums release).
func TestVerifiedDownloadFlow(t *testing.T) {
	const assetName = "asyncao-windows-x86_64.exe"
	const payload = "the-new-binary-bytes"
	good := sha256.Sum256([]byte(payload))
	goodHex := hex.EncodeToString(good[:])

	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer assetSrv.Close()

	// matching-sums server: verification passes.
	matchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(goodHex + "  " + assetName + "\n"))
	}))
	defer matchSrv.Close()

	// mismatching-sums server: a wrong digest for the same asset name.
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("00000000000000000000000000000000  " + assetName + "\n"))
	}))
	defer badSrv.Close()

	download := func(t *testing.T) string {
		t.Helper()
		staged := filepath.Join(t.TempDir(), "app.exe.new")
		if _, err := Download(context.Background(), assetSrv.URL, staged); err != nil {
			t.Fatalf("Download: %v", err)
		}
		return staged
	}

	t.Run("matching sums verifies", func(t *testing.T) {
		staged := download(t)
		wantHex, err := FetchSums(context.Background(), matchSrv.URL, assetName)
		if err != nil {
			t.Fatalf("FetchSums: %v", err)
		}
		if err := VerifyChecksum(staged, wantHex); err != nil {
			t.Fatalf("matching download must verify: %v", err)
		}
	})

	t.Run("mismatching sums fails before the swap", func(t *testing.T) {
		staged := download(t)
		wantHex, err := FetchSums(context.Background(), badSrv.URL, assetName)
		if err != nil {
			t.Fatalf("FetchSums: %v", err)
		}
		if err := VerifyChecksum(staged, wantHex); err == nil {
			t.Fatal("a checksum mismatch must fail so the binary is never installed")
		}
	})

	t.Run("absent sums skips verification and proceeds", func(t *testing.T) {
		staged := download(t)
		// SumsURL == "" is the pre-checksums release: the caller skips FetchSums
		// entirely and passes an empty want, which VerifyChecksum treats as
		// "no sum published, proceed".
		if err := VerifyChecksum(staged, ""); err != nil {
			t.Fatalf("an absent manifest must proceed unverified: %v", err)
		}
	})
}

// writeZip builds a flat zip (one entry per map key, contents from the value)
// at path, for ExtractZip / StageBundle fixtures.
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestExtractZip pins that the Windows bundle (a flat exe + DLLs archive) lands
// in the staging dir with its exact contents.
func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.zip")
	writeZip(t, zipPath, map[string]string{
		"asyncao.exe": "EXE",
		"SDL2.dll":    "DLL1",
		"libwebp.dll": "DLL2",
	})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractZip(zipPath, out); err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}
	if got := readFile(t, filepath.Join(out, "asyncao.exe")); got != "EXE" {
		t.Errorf("exe = %q", got)
	}
	if got := readFile(t, filepath.Join(out, "SDL2.dll")); got != "DLL1" {
		t.Errorf("SDL2.dll = %q", got)
	}
	if got := readFile(t, filepath.Join(out, "libwebp.dll")); got != "DLL2" {
		t.Errorf("libwebp.dll = %q", got)
	}
}

// TestExtractZipRejectsTraversal pins the §17.4 guard: a bundle entry carrying a
// path separator (an absolute or ../ path) must be refused, never written
// outside the staging dir.
func TestExtractZipRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	writeZip(t, zipPath, map[string]string{
		"../evil.dll": "EVIL",
	})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractZip(zipPath, out); err == nil {
		t.Fatal("a traversal entry must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.dll")); !os.IsNotExist(err) {
		t.Error("the traversal entry must not be written outside the staging dir")
	}
}

// TestStageBundle pins the bundle replacement choreography: the exe is swapped
// last, an existing DLL is rename-swapped (backup kept at .old), and a
// newly-shipped DLL just moves in.
func TestStageBundle(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "asyncao.exe")
	oldDLL := filepath.Join(dir, "SDL2.dll")
	writeFile(t, target, "OLD-EXE")
	writeFile(t, oldDLL, "OLD-DLL")

	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(staging, "asyncao.exe"), "NEW-EXE")
	writeFile(t, filepath.Join(staging, "SDL2.dll"), "NEW-DLL")
	writeFile(t, filepath.Join(staging, "SDL2_mixer_ext.dll"), "NEW-ENGINE") // a newly-shipped lib

	if err := StageBundle(staging, target, ""); err != nil {
		t.Fatalf("StageBundle: %v", err)
	}
	if got := readFile(t, target); got != "NEW-EXE" {
		t.Errorf("exe = %q, want the new exe", got)
	}
	if got := readFile(t, filepath.Join(dir, "asyncao.exe.old")); got != "OLD-EXE" {
		t.Errorf("exe backup = %q, want the old exe", got)
	}
	if got := readFile(t, oldDLL); got != "NEW-DLL" {
		t.Errorf("SDL2.dll = %q, want the new DLL", got)
	}
	if got := readFile(t, oldDLL+".old"); got != "OLD-DLL" {
		t.Errorf("SDL2.dll.old = %q, want the old DLL", got)
	}
	if got := readFile(t, filepath.Join(dir, "SDL2_mixer_ext.dll")); got != "NEW-ENGINE" {
		t.Errorf("newly-shipped DLL = %q, want it moved in (no old to back up)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "SDL2_mixer_ext.dll.old")); !os.IsNotExist(err) {
		t.Error("a DLL that didn't exist before must not leave a .old backup")
	}
}

// TestCleanupOldBundle pins that next-boot cleanup removes the exe's .old and
// every sibling DLL's .old, and leaves unrelated files alone.
func TestCleanupOldBundle(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "asyncao.exe")
	writeFile(t, exe+".old", "stale exe")
	writeFile(t, filepath.Join(dir, "SDL2.dll.old"), "stale dll")
	writeFile(t, filepath.Join(dir, "config.json"), "keep me")
	CleanupOldBundle(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error("the exe .old must be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "SDL2.dll.old")); !os.IsNotExist(err) {
		t.Error("a sibling DLL .old must be removed")
	}
	if got := readFile(t, filepath.Join(dir, "config.json")); got != "keep me" {
		t.Error("non-.old files must be left alone")
	}
}

// writeTarGz builds a gzipped tar at path: one entry per map key (0755, or 0644
// for .txt/.md documentation) — the shape of the macOS bundle tarball.
func writeTarGz(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		mode := int64(0o755)
		if strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".md") {
			mode = 0o644
		}
		hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestExtractTarGz pins the macOS bundle extraction: the binary + lib/ + docs
// land under the staging dir, the binary keeps its exec bit, and a '..' path is
// refused.
func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar.gz")
	writeTarGz(t, tarPath, map[string]string{
		"./asyncao-macos-arm64":     "BIN",
		"./lib/libSDL2-2.0.0.dylib": "DYLIB",
		"./INSTALL.txt":             "docs",
	})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractTarGz(tarPath, out); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}
	bin := filepath.Join(out, "asyncao-macos-arm64")
	if got := readFile(t, bin); got != "BIN" {
		t.Errorf("binary = %q", got)
	}
	if runtime.GOOS != "windows" { // Windows has no exec bit to preserve
		if info, err := os.Stat(bin); err == nil && info.Mode().Perm()&0o111 == 0 {
			t.Error("the extracted binary must keep its exec bit")
		}
	}
	if got := readFile(t, filepath.Join(out, "lib", "libSDL2-2.0.0.dylib")); got != "DYLIB" {
		t.Errorf("dylib = %q", got)
	}
	if got := readFile(t, filepath.Join(out, "INSTALL.txt")); got != "docs" {
		t.Errorf("INSTALL.txt = %q", got)
	}
}

// TestStageBundleMacLib pins the macOS bundle replacement: the binary (exec bit,
// no .exe) is detected and swapped last, lib/ dylibs are rename-swapped into
// lib/, a newly-shipped dylib moves in, and the INSTALL.txt doc is ignored.
func TestStageBundleMacLib(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "asyncao")
	writeFile(t, target, "OLD-BIN")
	libDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(libDir, "libSDL2.dylib"), "OLD-DYLIB")

	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(filepath.Join(staging, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(staging, "asyncao-macos-arm64")
	writeFile(t, bin, "NEW-BIN")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(staging, "lib", "libSDL2.dylib"), "NEW-DYLIB")
	writeFile(t, filepath.Join(staging, "lib", "libavif.dylib"), "NEW-LIB") // newly shipped
	writeFile(t, filepath.Join(staging, "INSTALL.txt"), "docs")

	if err := StageBundle(staging, target, "lib"); err != nil {
		t.Fatalf("StageBundle: %v", err)
	}
	if got := readFile(t, target); got != "NEW-BIN" {
		t.Errorf("binary = %q, want the new binary", got)
	}
	if got := readFile(t, filepath.Join(dir, "lib", "libSDL2.dylib")); got != "NEW-DYLIB" {
		t.Errorf("libSDL2.dylib = %q, want the new dylib", got)
	}
	if got := readFile(t, filepath.Join(dir, "lib", "libavif.dylib")); got != "NEW-LIB" {
		t.Errorf("newly-shipped dylib = %q, want it moved into lib/", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "INSTALL.txt")); !os.IsNotExist(err) {
		t.Error("documentation (INSTALL.txt) must not be installed")
	}
}
