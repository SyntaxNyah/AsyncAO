package render

import "testing"

// TestTextureStoreNegativeCache pins the decode-failure backoff: MarkFailed
// reports "fresh" once per window (so the pump logs once, not every retry),
// FailedRecently gates the manager's re-fetch, and a successful upload
// (clearFailed) drops the entry so a transient failure recovers.
func TestTextureStoreNegativeCache(t *testing.T) {
	s := &TextureStore{}
	const base = "https://h/characters/akita/char_icon"
	if s.FailedRecently(base) {
		t.Fatal("nothing should be failed yet")
	}
	if !s.MarkFailed(base) {
		t.Error("first MarkFailed should report a NEW failure (log-once)")
	}
	if s.MarkFailed(base) {
		t.Error("a repeat within the TTL should NOT report fresh")
	}
	if !s.FailedRecently(base) {
		t.Error("base should read as recently-failed (prefetch gate backs off)")
	}
	s.clearFailed(base)
	if s.FailedRecently(base) {
		t.Error("clearFailed must drop the entry")
	}
}
