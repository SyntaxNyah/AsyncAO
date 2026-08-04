package theme

// SCRATCH — prover probe, deleted before commit. Not part of the package.

import (
	"os"
	"strings"
	"testing"
)

func TestW1ProbeHostileFutureSidecar(t *testing.T) {
	path := os.Getenv("ASYNCAO_W1_PROBE")
	if path == "" {
		t.Skip("ASYNCAO_W1_PROBE unset")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("probe file unreadable: %v", err)
	}
	s, err := LoadSidecar(path)
	if err != nil {
		t.Fatalf("hostile future sidecar REFUSED: %v", err)
	}
	if s == nil {
		t.Fatal("LoadSidecar returned nil for an existing file")
	}
	if !s.IsFutureFormat() {
		t.Errorf("Format = %d, want a future format", s.Format)
	}
	if len(s.Elements) != 0 {
		t.Errorf("%d elements rendered from unknown kinds, want 0", len(s.Elements))
	}
	if got := len(s.Notes()); got != NoteCap {
		t.Errorf("Notes() = %d, want exactly NoteCap (%d)", got, NoteCap)
	}
	out, err := s.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(out) != string(src) {
		t.Errorf("ROUND TRIP CHANGED THE FILE: in %d bytes, out %d bytes", len(src), len(out))
		a, b := strings.Split(string(src), "\n"), strings.Split(string(out), "\n")
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				t.Errorf("first differing line %d:\n  in : %q\n  out: %q", i+1, a[i], b[i])
				break
			}
		}
	}
	if got := strings.Count(string(out), "[Element."); got != 96 {
		t.Errorf("%d capitalised [Element.*] headers survived, want 96", got)
	}
	if got := strings.Count(string(out), "[Widget."); got != 20 {
		t.Errorf("%d unknown [Widget.*] sections survived, want 20", got)
	}
	t.Logf("PROBE OK: format=%d notes=%d unknown=%d bytes=%d roundtrip=identical",
		s.Format, len(s.Notes()), len(s.Unknown()), len(out))
}
