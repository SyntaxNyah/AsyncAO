package config

import "testing"

// TestFavoritesExportImportRoundTrip pins the phone-book share path: export to
// JSON, import into a fresh prefs, dedup by URL, and a count of only NEW adds.
func TestFavoritesExportImportRoundTrip(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.AddFavorite("Home", "wss://home:2096", "my server")
	p.AddFavorite("Cafe", "wss://cafe:2096", "")
	data, err := p.ExportFavoritesJSON()
	if err != nil {
		t.Fatalf("ExportFavoritesJSON: %v", err)
	}

	q, _ := newTestPrefs(t)
	q.AddFavorite("Home", "wss://home:2096", "dup") // already present (same URL)
	n, err := q.MergeFavoritesJSON(data)
	if err != nil {
		t.Fatalf("MergeFavoritesJSON: %v", err)
	}
	if n != 1 { // only Cafe is new; Home dedups by URL
		t.Errorf("merged new count = %d, want 1 (Cafe; Home dedups by URL)", n)
	}
	if got := len(q.FavoriteServers()); got != 2 {
		t.Errorf("favorites after merge = %d, want 2", got)
	}
	if !q.IsFavorite("wss://cafe:2096") {
		t.Error("imported Cafe should be a favorite")
	}

	// Garbage in → error, nothing added.
	if _, err := q.MergeFavoritesJSON([]byte("not json")); err == nil {
		t.Error("MergeFavoritesJSON should reject non-JSON")
	}
}
