package media

import "testing"

// cleanListingEntry is the listing's security boundary: parent, self, absolute, external, nested and ".." links, raw or
// percent-encoded, are all refused.
func TestCleanListingEntry(t *testing.T) {
	reject := []string{
		"../", "./", "..", "%2e%2e/", "%2e%2e%2f", "a/..%2f",
		"/abs/", "/etc/passwd",
		"https://evil.example/x/", "http://x/y",
		"a/b", "a/b/", `a\b`,
		"", "%2e%2e",
	}
	for _, raw := range reject {
		if e, ok := cleanListingEntry(raw); ok {
			t.Errorf("cleanListingEntry(%q) accepted as %+v", raw, e)
		}
	}
	accept := map[string]ListingEntry{
		"maya/":         {Href: "maya/", Name: "maya", Dir: true},
		"char_icon.png": {Href: "char_icon.png", Name: "char_icon.png"},
		"my%20court/":   {Href: "my%20court/", Name: "my court", Dir: true},
	}
	for raw, want := range accept {
		if got, ok := cleanListingEntry(raw); !ok || got != want {
			t.Errorf("cleanListingEntry(%q) = %+v, %v; want %+v", raw, got, ok, want)
		}
	}
}

// Only a page that links to its parent folder is a listing, so an index page or soft 404 never reads as empty.
func TestParseListingNeedsAParentLink(t *testing.T) {
	const limit = 10
	for name, page := range map[string]string{
		"nginx":  `<pre><a href="../">../</a><a href="normal.png">normal.png</a></pre>`,
		"apache": `<a href="/base/characters/">Parent Directory</a> <a href="normal.png">normal.png</a>`,
		"caddy":  `<a href="./..">Up</a><a href="./normal.png">normal.png</a><a href="normal.png">x</a>`,
	} {
		if _, ok := ParseListing([]byte(page), limit); !ok {
			t.Errorf("%s listing not recognised", name)
		}
	}
	for name, page := range map[string]string{
		"index page": `<html><a href="normal.png">normal.png</a></html>`,
		"error page": `<html><h1>403 Forbidden</h1></html>`,
		"empty":      ``,
	} {
		if entries, ok := ParseListing([]byte(page), limit); ok {
			t.Errorf("%s read as a listing: %+v", name, entries)
		}
	}
}
