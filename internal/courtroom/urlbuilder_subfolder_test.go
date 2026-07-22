package courtroom

import (
	"strings"
	"testing"
)

// TestBackgroundSubfolderKeepsSeparators pins issue #40: a background whose name
// nests in a subfolder ("cases/case1") must build a URL with a REAL "/" path
// separator, never the percent-escaped "%2F" a single-segment escaper produces.
// A "%2F" URL is a dead path on strict origins (it doesn't normalize the escape
// back) AND, once an archive is exported, it lands on disk as a folder literally
// named "cases%2Fcase1" instead of the "cases/case1" subfolder the reporter
// expected. Backgrounds + desks are referenced by EVERY message, so this was the
// "many assets" the report describes.
func TestBackgroundSubfolderKeepsSeparators(t *testing.T) {
	u := NewURLBuilder("https://cdn/base/")
	got := u.Background("cases/case1", "defenseempty")
	if strings.Contains(got, "%2F") || strings.Contains(got, "%2f") {
		t.Fatalf("nested background escaped its separator: %q", got)
	}
	const want = "https://cdn/base/background/cases/case1/defenseempty"
	if got != want {
		t.Fatalf("Background(nested) = %q, want %q", got, want)
	}
	// A flat name is unchanged — the fix is a no-op for the common case.
	if flat := u.Background("gs4", "stand"); flat != "https://cdn/base/background/gs4/stand" {
		t.Fatalf("flat background changed: %q", flat)
	}
}

// TestBackgroundOverlayTraversal pins the DRIO "overlays/../background/x" trick
// (#40 follow-up): a unique position authored with "../" must keep real
// separators AND collapse the traversal exactly like AO2-Client's QFile does
// (get_background_path builds "background/<bg>/<part>" and the OS normalizes it),
// never the "overlays%2F..%2F…" escape that both 404s live and lands as a broken
// folder on disk.
func TestBackgroundOverlayTraversal(t *testing.T) {
	u := NewURLBuilder("https://cdn/base/")
	got := u.Background("drio/ch1/frontdoorroom", "overlays/../background/drio/monitor/wit")
	if strings.Contains(got, "%2F") || strings.Contains(got, "%2f") || strings.Contains(got, "..") {
		t.Fatalf("overlay traversal left an escape or a raw '..': %q", got)
	}
	// "frontdoorroom/overlays/.." collapses to "frontdoorroom", then re-descends.
	const want = "https://cdn/base/background/drio/ch1/frontdoorroom/background/drio/monitor/wit"
	if got != want {
		t.Fatalf("Background(overlay) = %q, want %q", got, want)
	}
	// A traversal that climbs above background/ re-roots at the origin (matching
	// AO2's OS resolution) and can never escape it.
	esc := u.Background("room", "../../../../etc/passwd")
	if strings.Contains(esc, "..") {
		t.Fatalf("traversal escaped the clamp: %q", esc)
	}
	if esc != "https://cdn/base/etc/passwd" {
		t.Fatalf("clamped traversal = %q, want origin-rooted etc/passwd", esc)
	}
}

// TestBackgroundFolderSubfolder pins the same fix for the folder-listing URL the
// background picker walks.
func TestBackgroundFolderSubfolder(t *testing.T) {
	u := NewURLBuilder("https://cdn/base/")
	got := u.BackgroundFolder("cases/case1")
	if strings.Contains(got, "%2F") || strings.Contains(got, "%2f") {
		t.Fatalf("nested background folder escaped its separator: %q", got)
	}
	if got != "https://cdn/base/background/cases/case1/" {
		t.Fatalf("BackgroundFolder(nested) = %q", got)
	}
}
