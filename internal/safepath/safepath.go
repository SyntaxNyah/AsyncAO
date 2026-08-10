// Package safepath holds the ONE implementation of the guards that keep a
// third-party folder or archive from reading — or being extracted — outside the
// root it was mounted at: ".." / absolute-path refusal (zip-slip), the refusal of
// everything that is neither a plain directory nor a regular file (symlinks first,
// but FIFOs, sockets and device nodes too), and a recursion depth bound.
//
// WHY IT IS ITS OWN PACKAGE. These guards had exactly one consumer
// (assets.MountIndex, which indexes user mount folders and .zip packs) and are
// about to have a second (internal/themepack, which extracts .aotheme bundles
// authored by strangers). Two copies of a security boundary is a divergence
// liability the moment a fix lands in one of them and not the other, so the copy
// that would have been made is this package instead — one implementation, one
// adversarial corpus, one place to fix.
//
// SDL-free and stdlib-only by construction (CLAUDE.md rule 1): nothing here may
// grow a dependency that would keep it out of a worker goroutine. WalkFiles
// blocks on the disk and is goroutine-only (rule 2).
package safepath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaxDepth bounds directory recursion. AO layouts are three deep
// (characters/<char>/(a)<emote>.png) and theme folders no deeper; twelve leaves
// room for oddly nested packs while stopping a pathological tree — or a symlink
// loop we did not already refuse — from walking forever (rule 4).
const MaxDepth = 12

// ErrUnsafe reports a path that could escape its root. Callers surface it as a
// refusal, never as a miss: a hostile name must not look like an absent file.
var ErrUnsafe = errors.New("safepath: path escapes its root")

// UnsafeRel refuses any relative path that could escape the root it will be
// joined to: an empty path, a ".." segment, a rooted or absolute path, or a
// Windows drive-relative / alternate-data-stream name (anything with a ':').
//
// Applied to archive entry names (zip-slip) and to every folder read. It is
// deliberately a predicate on the NAME alone — no disk access — so it is safe on
// any thread and cannot be defeated by a race against the filesystem.
//
// Note it does NOT refuse a name that merely CONTAINS "..": "a..b.png" is a
// legal filename and only a whole ".." SEGMENT can traverse.
func UnsafeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return true
	}
	if filepath.IsAbs(rel) || strings.Contains(rel, ":") {
		return true
	}
	for _, seg := range strings.FieldsFunc(rel, isSeparator) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// TooDeep reports whether rel sits at or past maxDepth separators.
//
// BOTH separators are counted. A zip entry name is '/'-separated by the format,
// but the name is attacker-controlled metadata and Windows treats '\' as a
// separator too, so counting only '/' would let a hostile archive walk as deep as
// it liked with backslashes.
func TooDeep(rel string, maxDepth int) bool {
	n := 0
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' || rel[i] == '\\' {
			n++
		}
	}
	return n >= maxDepth
}

// Join resolves a root-relative path against root, refusing anything UnsafeRel
// rejects. This is the only way a caller should turn an untrusted rel into a
// filesystem path: filepath.Join alone would silently CLEAN "../secret" into a
// path inside root for some inputs and out of it for others, which is a guard
// that reads as if it works.
func Join(root, rel string) (string, error) {
	if UnsafeRel(rel) {
		return "", ErrUnsafe
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// WalkFiles visits every regular file under root, passing the root-relative path
// with forward slashes. Returning false from fn stops the walk (that is how a
// caller enforces its own entry/byte cap). BLOCKING — goroutine only (rule 2).
//
// SYMLINKS ARE SKIPPED. WalkDir reports them via lstat, so a link is visible as a
// non-regular entry and refused here — which closes an exfiltration route a
// third-party pack could otherwise use (a link at characters/x/(a)a.png pointing
// at ~/.ssh/id_rsa would be indexed, read, and uploaded as a texture). The ".."
// guard alone does not cover it, because the link's own path never contains "..".
//
// Every rel handed to fn has already passed UnsafeRel and TooDeep, so a caller
// may Join it without re-checking. An unreadable SUBTREE is skipped rather than
// abandoning the rest of the pack; an unreadable ROOT is an error, because that
// is the mount itself failing.
//
// UnsafePath states the same refusal for a caller that holds a PATH rather than a
// DirEntry — the mount fetcher's case fold, which enumerates directories of its own
// (assets.foldJoin). Two mechanisms, one policy, one package.
func WalkFiles(root string, maxDepth int, fn func(rel string) bool) error {
	stopped := false
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // an unreadable subtree must not abandon the rest of the pack
		}
		if e.IsDir() {
			return nil
		}
		if !e.Type().IsRegular() {
			return nil // symlink, device, socket: never visited (see the doc comment)
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		if UnsafeRel(slashed) || TooDeep(slashed, maxDepth) {
			return nil
		}
		if !fn(slashed) {
			stopped = true
			return fs.SkipAll
		}
		return nil
	})
	if stopped {
		return nil
	}
	return err
}

// UnsafePath reports whether a candidate NAME must be refused before anything
// descends into it or reads through it: it is refused unless it is a plain directory
// or a regular file. A symlink, a FIFO, a socket and a device node are all refused.
//
// It is the by-path HALF OF WalkFiles' REFUSAL AND HAS THE SAME SHAPE, which is the
// whole reason it lives here: that walk visits regular files, recurses into
// directories, and skips every other entry (`!e.Type().IsRegular()` above); this
// answers the identical question for a caller holding a PATH rather than a DirEntry —
// the mount fetcher's case fold, which enumerates directories of its own
// (assets.foldJoin). This package holds ONE copy of each guard (see the package doc),
// so a change of policy has one place to land.
//
// IT REFUSES MORE THAN SYMLINKS BECAUSE THE FIRST DRAFT REFUSED ONLY SYMLINKS AND THE
// SHAPE CLAIM WAS THEREFORE FALSE. A review measured what the gap cost: a mount holding
// characters/Phoenix/(a)Normal.png as a FIFO is INVISIBLE to WalkFiles (the index over
// that tree is empty) while the fold enumerated it, folded the authored case onto it,
// and handed it back — and the os.ReadFile that followed blocked forever on a pool
// worker, because opening a FIFO for reading waits for a writer and assets.Fetch
// documents local reads as uncancellable. Device nodes are the same story with an
// unbounded read instead of a stall. The two byte sources have to agree on the whole
// shape or the agreement is a sentence rather than a property.
//
// LSTAT, NOT STAT: the question is what the name IS, never what it points at — an
// os.Stat-based answer reports the link's TARGET (a link to a directory looks like a
// directory, and a dangling link looks like nothing at all), which is precisely the
// blindness the guard exists to remove. BLOCKING (one lstat) — goroutine only, rule 2.
//
// A path that cannot be lstat'd is not refused. It cannot be traversed or read either
// — the caller's own open fails with the same error — so an unreadable candidate needs
// no refusal of its own, and refusing it would turn a permissions problem into a
// security-looking one.
func UnsafePath(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	m := fi.Mode()
	return !m.IsRegular() && !m.IsDir()
}

// isSeparator reports whether r separates path segments on ANY host. A pack
// authored on Windows can carry backslashes into a zip entry name, and that name
// must be segmented the same way here as the OS would segment it.
func isSeparator(r rune) bool { return r == '/' || r == '\\' }
