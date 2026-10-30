package update

// Self-replace. A running Windows .exe can't be overwritten but can be renamed, so the swap renames it aside;
// a failed install renames it back, because an AV can quarantine mid-swap.
// Verification here is integrity, not authenticity, which would need release signing at VerifyChecksum's call.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

const (
	// downloadMaxBytes bounds a release download (hard rule 4), so a hostile or mislinked asset can't fill the disk.
	downloadMaxBytes = 256 << 20
	// downloadTimeout caps the asset download, larger than a JSON fetch.
	downloadTimeout = 5 * time.Minute
)

// BackupPath is a sibling of the binary, so the rename is atomic; it also marks a swap for next-boot cleanup.
func BackupPath(targetPath string) string { return targetPath + ".old" }

// StagedPath is where a download lands before the swap; a sibling, so the rename is atomic.
func StagedPath(targetPath string) string { return targetPath + ".new" }

// CleanupOldVersion leaves a locked backup (AV scan) for a later boot; it touches disk, so run it off boot.
func CleanupOldVersion(targetPath string) {
	_ = os.Remove(BackupPath(targetPath))
}

// CleanupOldBundle removes .old backups of the exe and its runtime libs; locked files wait for a later boot.
func CleanupOldBundle(exePath string) {
	dir := filepath.Dir(exePath)
	clean := func(d string) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".old") {
				_ = os.Remove(filepath.Join(d, e.Name()))
			}
		}
	}
	clean(dir)
	clean(filepath.Join(dir, bundledLibDirName)) // macOS dylib backups live in lib/
}

// Download streams url to destPath, which must be on the install's volume (use StagedPath); a truncated or
// over-cap download is removed, so a partial file never reaches the swap.
func Download(ctx context.Context, url, destPath string) (int64, error) {
	cctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AsyncAO-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("update: downloading asset: %s", resp.Status)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, downloadMaxBytes))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		_ = os.Remove(destPath)
		return n, copyErr
	case closeErr != nil:
		_ = os.Remove(destPath)
		return n, closeErr
	case n >= downloadMaxBytes:
		_ = os.Remove(destPath)
		return n, fmt.Errorf("update: asset exceeds the %d-byte cap", int64(downloadMaxBytes))
	}
	// os.Create made the file 0644 and renames preserve mode, so macOS and Linux would not launch the new binary.
	if runtime.GOOS != "windows" {
		_ = os.Chmod(destPath, stagedBinaryMode)
	}
	return n, nil
}

// stagedBinaryMode is the conventional mode for an installed executable.
const stagedBinaryMode = 0o755

// VerifyChecksum is integrity, not authenticity: a compromised release could publish a matching sum.
// An empty wantHex (no sum published) skips the check.
func VerifyChecksum(path, wantHex string) error {
	if strings.TrimSpace(wantHex) == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(wantHex)) {
		return fmt.Errorf("update: checksum mismatch (got %s)", got)
	}
	return nil
}

// renameFn is overridable in tests to simulate a mid-swap failure.
var renameFn = os.Rename

// StageReplace swaps targetPath for newPath after the macOS SONAME preflight; a failed install rolls back,
// and a failed rollback is a hard error naming the good binary. All paths must share a volume.
func StageReplace(newPath, targetPath, backupPath string) error {
	// Before any mutation: a bare-binary update never replaces ./lib (docs/CODE-SIGNING.md), so refuse while the
	// install is pristine. StageBundle skips this because it replaces lib/ too.
	if err := preflightDarwinSwap(newPath, targetPath); err != nil {
		return err
	}
	return stageReplaceFile(newPath, targetPath, backupPath)
}

// stageReplaceFile is the rename-with-rollback swap shared by StageReplace and StageBundle.
func stageReplaceFile(newPath, targetPath, backupPath string) error {
	if _, err := os.Stat(newPath); err != nil {
		return fmt.Errorf("update: staged file missing: %w", err)
	}
	_ = os.Remove(backupPath) // clear a stale backup so the rename can't collide
	if err := renameFn(targetPath, backupPath); err != nil {
		return fmt.Errorf("update: backing up current file: %w", err)
	}
	if err := renameFn(newPath, targetPath); err != nil {
		if rbErr := renameFn(backupPath, targetPath); rbErr != nil {
			return fmt.Errorf("update: install failed (%v) and rollback failed (%v); working file is at %s", err, rbErr, backupPath)
		}
		return fmt.Errorf("update: installing new file (rolled back cleanly): %w", err)
	}
	return nil
}

// ExtractZip unpacks a flat Windows bundle, refusing entries with a separator so nothing lands outside destDir.
func ExtractZip(zipPath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			continue
		}
		if name == "" || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("update: unsafe path in bundle: %q", name)
		}
		dst := filepath.Join(destDir, name)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		_ = rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// ExtractTarGz allows only flat names or a lib/ subdir, refuses '..' and absolute paths, and keeps the exec bit.
func ExtractTarGz(tarPath, destDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // skip dirs, symlinks, links, devices
		}
		name := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		name = strings.TrimPrefix(name, "/")
		if safepath.UnsafeRel(name) {
			return fmt.Errorf("update: unsafe path in bundle: %q", hdr.Name)
		}
		dst := filepath.Join(destDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if mode := os.FileMode(hdr.Mode).Perm(); mode&0o111 != 0 {
			_ = os.Chmod(dst, mode) // preserve the exec bit so the binary launches
		}
	}
}

// StageBundle replaces the binary and libraries from an extracted bundle; libSubdir is "" on Windows, "lib" on
// macOS, and the binary is the flat .exe or the flat file that is neither library nor documentation.
func StageBundle(stagingDir, targetExe, libSubdir string) error {
	dir := filepath.Dir(targetExe)
	var newBin string
	type staged struct{ src, rel string }
	var files []staged

	isLib := func(ext string) bool {
		switch ext {
		case ".dll", ".dylib", ".so":
			return true
		}
		return false
	}
	isDoc := func(ext string) bool { return ext == ".txt" || ext == ".md" }

	addFlat := func(absDir string) error {
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := filepath.Join(absDir, e.Name())
			ext := strings.ToLower(filepath.Ext(e.Name()))
			switch {
			case newBin == "" && ext == ".exe":
				newBin = p
			case isDoc(ext):
			case isLib(ext):
				files = append(files, staged{p, e.Name()})
			case newBin == "":
				// Neither exe, library nor docs: the macOS bare binary, which has no extension.
				newBin = p
			default:
				files = append(files, staged{p, e.Name()})
			}
		}
		return nil
	}
	if err := addFlat(stagingDir); err != nil {
		return err
	}
	if libSubdir != "" {
		libDir := filepath.Join(stagingDir, libSubdir)
		entries, err := os.ReadDir(libDir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			files = append(files, staged{filepath.Join(libDir, e.Name()), filepath.Join(libSubdir, e.Name())})
		}
	}
	if newBin == "" {
		return fmt.Errorf("update: bundle contains no binary")
	}
	for _, f := range files {
		target := filepath.Join(dir, f.rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if _, statErr := os.Stat(target); statErr != nil {
			if err := renameFn(f.src, target); err != nil {
				return fmt.Errorf("update: installing %s: %w", f.rel, err)
			}
			continue
		}
		if err := stageReplaceFile(f.src, target, target+".old"); err != nil {
			return err
		}
	}
	return stageReplaceFile(newBin, targetExe, targetExe+".old")
}

// TargetWritable creates a temp file, the only reliable cross-filesystem check; read-only installs need the page.
func TargetWritable(targetPath string) bool {
	dir := targetPath[:strings.LastIndexAny(targetPath, `/\`)+1]
	if dir == "" {
		dir = "."
	}
	f, err := os.CreateTemp(dir, ".asyncao-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// bundledLibDirName is the macOS tarball's dylib folder; its presence tells a tarball install, which can
// skew, from a bare brew install, which falls through to /opt/homebrew/lib.
const bundledLibDirName = "lib"

// preflightDarwinSwap refuses an update whose binary can't load an @rpath dylib from the stale lib/
// (docs/CODE-SIGNING.md). A false positive costs a manual tarball download, never a broken install.
func preflightDarwinSwap(newPath, targetPath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	libDir := filepath.Join(filepath.Dir(targetPath), bundledLibDirName)
	if info, err := os.Stat(libDir); err != nil || !info.IsDir() {
		return nil // no bundled lib/ → brew/rpath-fallback install, can't skew
	}
	f, err := macho.Open(newPath)
	if err != nil {
		return nil // not inspectable as Mach-O → fail open, let the swap proceed
	}
	defer f.Close()
	imported, err := f.ImportedLibraries()
	if err != nil {
		return nil // fail open
	}
	// A correct release is all @rpath (bundle-macos.sh check_clean), so a Homebrew import means a mis-bundled
	// binary, and dyld would fail at launch for tarball users without Homebrew.
	if hb := homebrewImports(imported); len(hb) > 0 {
		// "or any newer release": this also fires on an experimental downgrade onto a release with no tarball.
		return fmt.Errorf("update: the new macOS build links %s directly from Homebrew instead of its bundled lib/ "+
			"(a mis-bundled release asset) — install from the release page's bundle tarball, or any newer release", hb[0])
	}
	missing := missingBundledDylibs(imported, libDir)
	if len(missing) == 0 {
		return nil
	}
	// Name the missing dylib so the message is actionable; the matching lib/ comes with the tarball.
	return fmt.Errorf("update: the new macOS build needs %s, which the installed lib/ doesn't have "+
		"(a dependency changed its version) — re-download the bundle tarball from the release page to update", missing[0])
}

// homebrewImports returns paths inside a Homebrew prefix (/opt/homebrew on Apple Silicon, /usr/local on Intel).
func homebrewImports(imported []string) []string {
	var bad []string
	for _, name := range imported {
		if strings.HasPrefix(name, "/opt/homebrew/") || strings.HasPrefix(name, "/usr/local/") {
			bad = append(bad, name)
		}
	}
	return bad
}

// missingBundledDylibs returns base names of @rpath imports absent from libDir; others are never bundled.
func missingBundledDylibs(imported []string, libDir string) []string {
	const rpathPrefix = "@rpath/"
	var missing []string
	for _, name := range imported {
		if !strings.HasPrefix(name, rpathPrefix) {
			continue // absolute or @executable_path/@loader_path: not bundled here
		}
		base := path.Base(name[len(rpathPrefix):]) // dylib SONAMEs use / — use path, not filepath
		if _, err := os.Stat(filepath.Join(libDir, base)); err != nil {
			missing = append(missing, base)
		}
	}
	return missing
}
