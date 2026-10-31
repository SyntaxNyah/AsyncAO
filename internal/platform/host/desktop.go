package host

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Desktop hands URLs and files to the programs the user chose for them.
type Desktop struct{}

// OpenURL opens an http or https address in the browser; server text reaches this, and the OS would run anything else.
func (Desktop) OpenURL(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("not a web address: %q", address)
	}
	return sdl.OpenURL(address)
}

// OpenPath opens a folder in the file manager, or a file in its default program.
func (Desktop) OpenPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return sdl.OpenURL(fileURL(abs))
}

// fileURL percent-encodes an absolute path; a Windows drive path gains the leading slash file URLs need.
func fileURL(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
