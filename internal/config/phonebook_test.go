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

// TestUpdateFavorite pins the in-place phone-book edit: rename, re-address,
// collision-reject, not-found, no-op-accept, and list-order preservation.
func TestUpdateFavorite(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.AddFavorite("Home", "wss://home:2096", "my server")
	p.AddFavorite("Cafe", "wss://cafe:2096", "coffee")
	p.AddFavorite("Work", "wss://work:2096", "office")

	// Rename only (URL unchanged): description preserved, identity intact.
	if !p.UpdateFavorite("wss://cafe:2096", "Café", "wss://cafe:2096", "coffee") {
		t.Fatal("rename should succeed")
	}
	favs := p.FavoriteServers()
	if favs[1].Name != "Café" || favs[1].URL != "wss://cafe:2096" || favs[1].Description != "coffee" {
		t.Errorf("after rename: %+v, want Name=Café URL=wss://cafe:2096 Description=coffee", favs[1])
	}
	// Order preserved: the middle entry stays at index 1 (not appended to the end).
	if len(favs) != 3 || favs[0].URL != "wss://home:2096" || favs[2].URL != "wss://work:2096" {
		t.Errorf("update must preserve list order, got %+v", favs)
	}

	// Address change: old URL gone, new URL present, still index 1.
	if !p.UpdateFavorite("wss://cafe:2096", "Café", "wss://bistro:2096", "coffee") {
		t.Fatal("address change should succeed")
	}
	if p.IsFavorite("wss://cafe:2096") {
		t.Error("old address should no longer be a favorite after re-address")
	}
	if !p.IsFavorite("wss://bistro:2096") {
		t.Error("new address should be a favorite after re-address")
	}
	if favs = p.FavoriteServers(); favs[1].URL != "wss://bistro:2096" {
		t.Errorf("re-addressed entry should stay at index 1, got %+v", favs)
	}

	// Collision: changing Work's address onto Home's URL is rejected, both untouched.
	if p.UpdateFavorite("wss://work:2096", "Work", "wss://home:2096", "office") {
		t.Error("collision with another favorite's URL should be rejected")
	}
	favs = p.FavoriteServers()
	if favs[0].URL != "wss://home:2096" || favs[2].URL != "wss://work:2096" {
		t.Errorf("rejected collision must not mutate any entry, got %+v", favs)
	}

	// Not found: unknown oldURL returns false.
	if p.UpdateFavorite("wss://ghost:2096", "Ghost", "wss://ghost:2096", "") {
		t.Error("updating a non-existent favorite should return false")
	}

	// No-op (identical fields): accepted (true) so an unchanged Save doesn't error.
	if !p.UpdateFavorite("wss://home:2096", "Home", "wss://home:2096", "my server") {
		t.Error("a no-op update should return true (accepted, nothing changed)")
	}
}
