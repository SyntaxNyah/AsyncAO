// Package update does the one-shot self-update check: exactly one GET per Check, with no polling.
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

// devVersion is what an unstamped build reports; such a build never self-updates.
const devVersion = "dev"

// Version is the running build's version, stamped by the linker with -X (see scripts/build.ps1).
var Version = devVersion

// IsDev reports whether the running build is unstamped.
func IsDev() bool { return Version == "" || Version == devVersion }

const (
	// DefaultReleasesURL is the latest non-draft, non-prerelease release: the stable channel.
	DefaultReleasesURL = "https://api.github.com/repos/SyntaxNyah/AsyncAO/releases/latest"
	// DefaultReleaseListURL lists newest first with prereleases: the experimental channel, as test branch builds
	// publish as prereleases. per_page bounds the page (hard rule 4).
	DefaultReleaseListURL = "https://api.github.com/repos/SyntaxNyah/AsyncAO/releases?per_page=10"
	// checkTimeout stops a hung endpoint stalling the caller's goroutine forever.
	checkTimeout = 10 * time.Second
	// maxBodyBytes caps the response read (hard rule 4); a hostile body is truncated.
	maxBodyBytes = 2 << 20
)

// Release is a newer version with its patch notes, release page, and direct asset URL ("" when no asset matched).
type Release struct {
	Version    string // tag with any leading v stripped (e.g. "1.2.3")
	Tag        string // raw tag_name as published
	Notes      string // release body — the patch notes
	PageURL    string // html_url — the release page
	AssetURL   string // browser_download_url of the matched asset
	AssetName  string // file name of the matched asset — the key into the SHA256SUMS list
	SumsURL    string // browser_download_url of the SHA256SUMS asset ("" on releases cut before checksums shipped)
	Prerelease bool   // published as a prerelease (a test-branch build) — the UI says "test build"
}

const (
	// sumsAssetName has one "<sha256>  <filename>" line per binary (release.yml); if absent, downloads go unverified.
	sumsAssetName = "SHA256SUMS.txt"
	// maxSumsBytes caps the SHA256SUMS download (hard rule 4).
	maxSumsBytes = 64 << 10
)

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Doer sends a request; the caller's comes from the socket factory, so updates follow the proxy setting.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Check does one GET and returns the latest release if its tag is a higher semver than current; (nil, nil)
// when already current or a dev build, and an error only on transport or decode failure.
func Check(ctx context.Context, client Doer, releasesURL, current, assetMatch string) (*Release, error) {
	if current == "" || current == devVersion {
		return nil, nil // unstamped/dev build never self-updates
	}
	if releasesURL == "" {
		releasesURL = DefaultReleasesURL
	}
	body, err := fetchJSON(ctx, client, releasesURL)
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
	return newRelease(&gh, assetMatch), nil
}

// CheckExperimental does one GET, and the newest published build wins if its tag differs from the running one.
// It deliberately allows sideways and downgrade offers: test branch tags rank below the stable they preview.
func CheckExperimental(ctx context.Context, client Doer, listURL, current, assetMatch string) (*Release, error) {
	if current == "" || current == devVersion {
		return nil, nil // unstamped/dev build never self-updates
	}
	if listURL == "" {
		listURL = DefaultReleaseListURL
	}
	body, err := fetchJSON(ctx, client, listURL)
	if err != nil {
		return nil, err
	}
	var list []ghRelease
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("update: decoding release list: %w", err)
	}
	// GitHub returns newest first: the first published, tagged entry is the channel's current build.
	for i := range list {
		gh := &list[i]
		if gh.Draft || gh.TagName == "" {
			continue
		}
		if trimVPrefix(gh.TagName) == trimVPrefix(current) {
			return nil, nil // the newest published build IS the one running
		}
		return newRelease(gh, assetMatch), nil
	}
	return nil, nil
}

func fetchJSON(ctx context.Context, client Doer, url string) ([]byte, error) {
	return fetchBounded(ctx, client, url, maxBodyBytes)
}

// fetchBounded is the package's bounded, timeout-capped GET, with a per-call read cap.
func fetchBounded(ctx context.Context, client Doer, url string, maxBytes int64) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AsyncAO-updater") // GitHub rejects an empty UA
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: endpoint %s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

// newRelease must not stop at the platform match (first name containing assetMatch; "" takes the first): the
// sums asset can come after the binary, and stopping would silently disable verification.
func newRelease(gh *ghRelease, assetMatch string) *Release {
	rel := &Release{
		Version:    trimVPrefix(gh.TagName),
		Tag:        gh.TagName,
		Notes:      strings.TrimSpace(gh.Body),
		PageURL:    gh.HTMLURL,
		Prerelease: gh.Prerelease,
	}
	want := strings.ToLower(assetMatch)
	for _, a := range gh.Assets {
		if a.Name == sumsAssetName {
			rel.SumsURL = a.DownloadURL
			continue
		}
		if rel.AssetURL == "" && (want == "" || strings.Contains(strings.ToLower(a.Name), want)) {
			rel.AssetURL = a.DownloadURL
			rel.AssetName = a.Name
		}
	}
	return rel
}

// FetchSums errors on a missing entry, as every shipped binary is listed and the update must not go unverified.
// Don't call it with an empty sumsURL.
func FetchSums(ctx context.Context, client Doer, sumsURL, assetName string) (string, error) {
	body, err := fetchBounded(ctx, client, sumsURL, maxSumsBytes)
	if err != nil {
		return "", err
	}
	hex, ok := parseSums(string(body), assetName)
	if !ok {
		return "", fmt.Errorf("update: %q missing from %s", assetName, sumsAssetName)
	}
	return hex, nil
}

// parseSums reads sha256sum lines (a leading "*" marks binary mode), comparing base names.
func parseSums(manifest, assetName string) (string, bool) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if pathBase(name) == assetName {
			return fields[0], true
		}
	}
	return "", false
}

// pathBase handles both separators, as filepath is OS-specific and a manifest may carry a directory prefix.
func pathBase(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		return name[i+1:]
	}
	return name
}

// SelfUpdateAssetMatch returns the case-insensitive asset name part for this platform; unknown GOOS gets its name.
func SelfUpdateAssetMatch(goos string) string {
	switch goos {
	case "windows":
		return "windows-x86_64-bundle.zip"
	case "linux":
		return "linux-x86_64.appimage"
	case "darwin":
		return "macos-bundle-arm64.tar.gz"
	default:
		return goos
	}
}

func trimVPrefix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		return s[1:]
	}
	return s
}

// parseSemver: missing or non-numeric parts are 0, so a malformed tag can't read as newer; +meta is ignored.
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

// compareSemver returns -1, 0 or +1, a prerelease ranking below its core release (semver §11).
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
