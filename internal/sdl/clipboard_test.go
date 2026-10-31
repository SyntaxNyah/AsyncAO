package sdl

import (
	"bytes"
	"testing"
)

func TestSetClipboardDataServesEachType(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := Init(INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer Quit()
	const (
		uriType   = "text/uri-list"
		otherType = "x-test/other"
	)
	uri, other := []byte("file:///a.gif\r\n"), []byte("copy")
	if err := SetClipboardData(map[string][]byte{uriType: uri, otherType: other}); err != nil {
		t.Fatal(err)
	}
	if got := GetClipboardData(uriType); !bytes.Equal(got, uri) {
		t.Errorf("%s: got %q, want %q", uriType, got, uri)
	}
	if got := GetClipboardData(otherType); !bytes.Equal(got, other) {
		t.Errorf("%s: got %q, want %q", otherType, got, other)
	}
	// A second offer releases the first one, which must not crash on the old data.
	if err := SetClipboardData(map[string][]byte{uriType: other}); err != nil {
		t.Fatal(err)
	}
	if got := GetClipboardData(uriType); !bytes.Equal(got, other) {
		t.Errorf("after a new offer: got %q, want %q", got, other)
	}
}
