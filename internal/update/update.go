// Package update implements AsyncAO's one-shot self-update check. It asks the
// GitHub Releases API for the latest published release, compares its semver tag
// to the running build, and — when newer — returns the release together with
// its patch notes (the release body) and the downloadable asset URL.
//
// The package is pure Go (no SDL/CGO) and does EXACTLY ONE network GET per
// Check — there is no polling or ticker here. The caller fires Check once, on
// its own goroutine, AFTER the window is up, so the update check adds zero cost
// to the boot critical path (the self-replace and the "What's New" UI live with
// the caller; this package only decides "is there a newer build, and what does
// it say"). See docs/FEATURES.md and the M13 plan.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// devVersion is the version an unstamped build reports. Release builds stamp a
// real semver via the linker (see build.ps1); a dev build never self-updates.
const devVersion = "dev"

// Version is the running build's version. Stamped at link time with
//
//	-ldflags "-X github.com/SyntaxNyah/AsyncAO/internal/update.Version=v1.2.3"
//
// Unstamped builds report "dev" and Check treats them as never having an
// update available (a developer running their own build isn't nagged).
var Version = devVersion

const (
	// DefaultReleasesURL is the GitHub Releases API for this repo's latest
	// published (non-draft, non-prerelease) release.
	DefaultReleasesURL = "https://api.github.com/repos/SyntaxNyah/AsyncAO/releases/latest"
	// checkTimeout bounds the one-shot probe so a hung endpoint can't stall the
	// caller's goroutine forever.
	checkTimeout = 10 * time.Second
	// maxBodyBytes caps the response read (rule §17.4: nothing unbounded). A
	// single release's JSON — even with long notes and many assets — fits well
	// under this; a hostile/huge body is truncated, not slurped.
	maxBodyBytes = 2 << 20
)

// Release is the user-facing result of a successful check: the newer version,
// its patch notes (shown on the "What's New" screen), the release page (for a
// "view online" fallback), and the direct asset download URL for the
// self-replace step ("" when the release shipped no matching asset).
type Release struct {
	Version  string // tag with any leading v stripped (e.g. "1.2.3")
	Tag      string // raw tag_name as published
	Notes    string // release body — the patch notes
	PageURL  string // html_url — the release page
	AssetURL string // browser_download_url of the matched asset
}

// ghRelease mirrors the subset of the GitHub Releases JSON we read.
type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check performs ONE GET against releasesURL (DefaultReleasesURL when "") and
// returns the latest release when its tag is a higher semver than current. It
// returns (nil, nil) when the build is already current/newer or when current is
// an unstamped dev build, and an error only on a transport/decode failure.
//
// assetMatch selects which release asset is the download: the first asset whose
// (lowercased) name contains it wins — pass e.g. "windows" or ".zip". An empty
// assetMatch takes the first asset; if nothing matches, AssetURL stays "".
func Check(ctx context.Context, releasesURL, current, assetMatch string) (*Release, error) {
	if current == "" || current == devVersion {
		return nil, nil // unstamped/dev build never self-updates
	}
	if releasesURL == "" {
		releasesURL = DefaultReleasesURL
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AsyncAO-updater") // GitHub rejects an empty UA
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: releases endpoint returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	var gh ghRelease
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, fmt.Errorf("update: decoding release: %w", err)
	}
	if gh.TagName == "" || compareSemver(gh.TagName, current) <= 0 {
		return nil, nil // no published tag, or not newer than us
	}
	rel := &Release{
		Version: trimVPrefix(gh.TagName),
		Tag:     gh.TagName,
		Notes:   strings.TrimSpace(gh.Body),
		PageURL: gh.HTMLURL,
	}
	want := strings.ToLower(assetMatch)
	for _, a := range gh.Assets {
		if want == "" || strings.Contains(strings.ToLower(a.Name), want) {
			rel.AssetURL = a.DownloadURL
			break
		}
	}
	return rel, nil
}

// trimVPrefix drops a single leading v/V from a tag.
func trimVPrefix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		return s[1:]
	}
	return s
}

// parseSemver splits a tag into its numeric major.minor.patch and an optional
// prerelease suffix. Missing parts are 0; build metadata (+meta) is ignored;
// non-numeric parts parse as 0, so a malformed tag can never read as "newer".
func parseSemver(s string) (nums [3]int, pre string) {
	s = trimVPrefix(s)
	if i := strings.IndexByte(s, '+'); i >= 0 { // build metadata: ignore
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 { // prerelease
		pre = s[i+1:]
		s = s[:i]
	}
	for i, part := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			n = 0
		}
		nums[i] = n
	}
	return nums, pre
}

// compareSemver returns -1, 0, +1 for a<b, a==b, a>b over major.minor.patch,
// with a prerelease ranking BELOW the same core release (semver §11) — both
// prerelease, compare the suffixes lexically (good enough for our tags).
func compareSemver(a, b string) int {
	an, ap := parseSemver(a)
	bn, bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "": // a is the full release, b is a prerelease of it → a > b
		return 1
	case bp == "":
		return -1
	default:
		return strings.Compare(ap, bp)
	}
}
