package themepack

// Create a theme from a model; blocking, goroutine only (hard rule 2).

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ErrCreateNoModel reports a create with nothing to write, a caller bug rather than a disk problem.
var ErrCreateNoModel = errors.New("themepack: no sidecar to create a theme from")

// CreateFromSidecar writes sc into a new collision-suffixed folder, built under .part and renamed,
// so a crash leaves no half-theme; Sidecar.Bytes refuses bad models first.
func CreateFromSidecar(themesRoot, name string, sc *theme.Sidecar) (*Manifest, error) {
	if sc == nil {
		return nil, ErrCreateNoModel
	}
	if themesRoot == "" {
		return nil, errors.New("themepack: no themes folder to create in")
	}
	// Before touching the disk: finding a bad model after MkdirAll leaves a folder behind.
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
