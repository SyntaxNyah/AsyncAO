// Package safepath is the one implementation of the guards that keep a third-party folder or archive in its root.
// It stays SDL-free and stdlib-only so it can run on a worker goroutine (hard rule 1).
package safepath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaxDepth bounds recursion (hard rule 4): AO layouts are three deep, and twelve allows odd packs.
const MaxDepth = 12

// ErrUnsafe must surface as a refusal, because a hostile name must not look like an absent file.
var ErrUnsafe = errors.New("safepath: path escapes its root")

// UnsafeRel refuses an empty path, a ".." segment, a rooted path, or any ':' (drive-relative, alternate stream).
// "a..b.png" is legal: only a whole ".." segment traverses.
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

// TooDeep counts both separators, because Windows treats '\' as one and a hostile zip name could nest endlessly.
func TooDeep(rel string, maxDepth int) bool {
	n := 0
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' || rel[i] == '\\' {
			n++
		}
	}
	return n >= maxDepth
}

// Join resolves an untrusted rel against root; plain filepath.Join silently cleans "../secret".
func Join(root, rel string) (string, error) {
	if UnsafeRel(rel) {
		return "", ErrUnsafe
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// WalkFiles visits every regular file with forward-slash paths; fn returning false stops it. Goroutine only
// (hard rule 2).
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

// UnsafePath refuses anything but a plain directory or regular file: opening a FIFO blocks a pool worker
// forever and a device node reads without end, and assets.Fetch documents local reads as uncancellable.
// It uses lstat because the question is what the name is, not what it points at. An lstat failure isn't
// refused, as the caller's own open fails the same way. Goroutine only (hard rule 2).
func UnsafePath(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	m := fi.Mode()
	return !m.IsRegular() && !m.IsDir()
}

// isSeparator treats both on any host, since a Windows-authored pack can carry backslashes in a zip entry name.
func isSeparator(r rune) bool { return r == '/' || r == '\\' }
