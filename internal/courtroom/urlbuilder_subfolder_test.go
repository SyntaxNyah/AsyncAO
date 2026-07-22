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
