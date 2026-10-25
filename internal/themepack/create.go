package themepack

// CREATE FROM A MODEL — "a theme from nothing" (v1.90.0 W9, the preset gallery).
// BLOCKING — goroutine only (hard rule 2).

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ErrCreateNoModel reports a create with nothing to write. Named because the
// caller's fix differs from every other refusal here: it is a bug in the caller,
// not a problem with the disk.
var ErrCreateNoModel = errors.New("themepack: no sidecar to create a theme from")

// CreateFromSidecar writes sc into a NEW folder under themesRoot.
// The name is sanitized and collision-suffixed exactly as an import is, so
// building the same pair twice gives "Neon Netrunner" and "Neon Netrunner (2)"
// rather than one overwriting the other.
func CreateFromSidecar(themesRoot, name string, sc *theme.Sidecar) (*Manifest, error) {
	if sc == nil {
		return nil, ErrCreateNoModel
	}
	if themesRoot == "" {
		return nil, errors.New("themepack: no themes folder to create in")
	}
	// FIRST, before anything touches the disk: a model the reader would refuse is
	// not a theme, and finding that out after MkdirAll leaves a folder behind.
	body, err := sc.Bytes()
	if err != nil {
		return nil, err
	}
	want := SanitizeName(name)
	if want == "" {
		want = SanitizeName(sc.Meta.Name)
	}
	if want == "" {
		return nil, ErrCreateNoModel
	}
	if err := os.MkdirAll(themesRoot, packDirPerm); err != nil {
		return nil, err
	}
	final, err := freeName(themesRoot, want)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(themesRoot, final)
	part := dest + packPartSuffix
	if err := os.RemoveAll(part); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(part, packDirPerm); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(part, sidecarName), body, packFilePerm); err != nil {
		os.RemoveAll(part)
		return nil, err
	}
	if err := os.Rename(part, dest); err != nil {
		os.RemoveAll(part)
		return nil, err
	}
	return &Manifest{
		Root:    final,
		Dir:     dest,
		Files:   1,
		Bytes:   int64(len(body)),
		Name:    sc.Meta.Name,
		Author:  sc.Meta.Author,
		License: sc.Meta.License,
		Format:  sc.Format,
	}, nil
}
