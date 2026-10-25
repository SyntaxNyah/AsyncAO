// Package safepath holds the ONE implementation of the guards that keep a
// third-party folder or archive from reading — or being extracted — outside the
// root it was mounted at: ".." / absolute-path refusal (zip-slip), the refusal of
// everything that is neither a plain directory nor a regular file (symlinks first,
// but FIFOs, sockets and device nodes too), and a recursion depth bound.
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
// IT REFUSES MORE THAN SYMLINKS BECAUSE THE FIRST DRAFT REFUSED ONLY SYMLINKS AND THE
// SHAPE CLAIM WAS THEREFORE FALSE. A review measured what the gap cost: a mount holding
// characters/Phoenix/(a)Normal.png as a FIFO is INVISIBLE to WalkFiles (the index over
// that tree is empty) while the fold enumerated it, folded the authored case onto it,
// and handed it back — and the os.ReadFile that followed blocked forever on a pool
// worker, because opening a FIFO for reading waits for a writer and assets.Fetch
// documents local reads as uncancellable. Device nodes are the same story with an
// unbounded read instead of a stall. The two byte sources have to agree on the whole
// shape or the agreement is a sentence rather than a property.
// LSTAT, NOT STAT: the question is what the name IS, never what it points at — an
// os.Stat-based answer reports the link's TARGET (a link to a directory looks like a
// directory, and a dangling link looks like nothing at all), which is precisely the
// blindness the guard exists to remove. BLOCKING (one lstat) — goroutine only, rule 2.
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
