package media

import (
	"net/url"
	"regexp"
	"strings"
)

// ListingEntry is one link in a host's HTML folder listing (Apache, nginx or Caddy autoindex).
type ListingEntry struct {
	// Href is the link as written, still escaped; a folder keeps its trailing slash.
	Href string
	// Name is the decoded file or folder name.
	Name string
	Dir  bool
}

// listingHref captures href targets; [^"?#] drops Apache's ?C=N;O=A sort links.
var listingHref = regexp.MustCompile(`(?i)href="([^"?#]+)"`)

// listingParent finds the link to the parent folder every autoindex page has: "../" on nginx and Caddy, "Parent
// Directory" on Apache, whose href is absolute.
var listingParent = regexp.MustCompile(`(?i)href="(\./)?\.\./?"|>\s*Parent Directory\s*<`)

// ParseListing returns up to limit unique entries one level below the folder, and whether data is a folder listing at
// all: an index page or a soft 404 has no parent link, and must never read as an empty folder.
func ParseListing(data []byte, limit int) (entries []ListingEntry, ok bool) {
	if !listingParent.Match(data) {
		return nil, false
	}
	seen := make(map[string]struct{})
	for _, m := range listingHref.FindAllSubmatch(data, -1) {
		e, ok := cleanListingEntry(string(m[1]))
		if !ok {
			continue
		}
		key := strings.ToLower(e.Name)
		if e.Dir {
			key += "/"
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, e)
		if len(entries) >= limit {
			break
		}
	}
	return entries, true
}

// cleanListingEntry normalizes one href and reports whether it is one level deep and stays in the folder. It is the
// listing's security boundary: ../, ./, /x, x://, a/b and any raw or percent-encoded ".." are refused.
func cleanListingEntry(raw string) (ListingEntry, bool) {
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "://") {
		return ListingEntry{}, false
	}
	dir := strings.HasSuffix(raw, "/")
	name := strings.TrimSuffix(raw, "/")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return ListingEntry{}, false
	}
	return ListingEntry{Href: raw, Name: name, Dir: dir}, true
}
