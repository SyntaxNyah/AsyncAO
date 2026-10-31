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

// PickFiles shows the OS's open-file dialog on the main thread; done may run on any thread and must not block.
func (Desktop) PickFiles(opts sdl.DialogOptions, done sdl.DialogDone) {
	sdl.ShowOpenFileDialog(opts, done)
}

// PickFolders shows the OS's folder dialog on the main thread; done may run on any thread and must not block.
func (Desktop) PickFolders(opts sdl.DialogOptions, done sdl.DialogDone) {
	sdl.ShowOpenFolderDialog(opts, done)
}

// fileClipboardData offers an absolute path the way Linux file managers paste it: most read text/uri-list, while GNOME's
// Files reads its own type.
func fileClipboardData(abs string) map[string][]byte {
	address := fileURL(abs)
	return map[string][]byte{
		"text/uri-list":                []byte(address + "\r\n"),
		"x-special/gnome-copied-files": []byte("copy\n" + address),
	}
}

// fileURL percent-encodes an absolute path; a Windows drive path gains the leading slash file URLs need.
func fileURL(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
