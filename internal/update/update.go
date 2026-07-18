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

// IsDev reports whether the running build is an unstamped (non-release) build.
// Callers use it to skip release-only UI cues (e.g. the changelog's "installed"
// marker) without duplicating the devVersion literal.
func IsDev() bool { return Version == "" || Version == devVersion }

const (
	// DefaultReleasesURL is the GitHub Releases API for this repo's latest
	// published (non-draft, non-prerelease) release — the STABLE channel.
	DefaultReleasesURL = "https://api.github.com/repos/SyntaxNyah/AsyncAO/releases/latest"
	// DefaultReleaseListURL is the full releases list, newest first,
	// PRERELEASES INCLUDED — the experimental channel's feed. Builds cut from
	// the test branch (MayAO-Test) publish as prerelease tags, so following
	// this list IS following that branch. per_page bounds the page (rule
	// §17.4); ten releases is far more history than the pick needs (it only
	// wants the newest published entry).
	DefaultReleaseListURL = "https://api.github.com/repos/SyntaxNyah/AsyncAO/releases?per_page=10"
	// checkTimeout bounds the one-shot probe so a hung endpoint can't stall the
	// caller's goroutine forever.
	checkTimeout = 10 * time.Second
	// maxBodyBytes caps the response read (rule §17.4: nothing unbounded). A
	// release-list page — even with long notes and many assets — fits well
	// under this; a hostile/huge body is truncated, not slurped.
	maxBodyBytes = 2 << 20
)

// Release is the user-facing result of a successful check: the newer version,
// its patch notes (shown on the "What's New" screen), the release page (for a
// "view online" fallback), and the direct asset download URL for the
// self-replace step ("" when the release shipped no matching asset).
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
	// sumsAssetName is the release asset the workflow publishes with one
	// "<sha256>  <filename>" line per binary (see .github/workflows/release.yml).
	// Its presence turns on authenticity-adjacent verification of the download;
	// its absence (every release cut before this shipped) leaves the update flow
	// exactly as it was — an unverified but integrity-capped download.
	sumsAssetName = "SHA256SUMS.txt"
	// maxSumsBytes caps the SHA256SUMS download (rule §17.4). A sums manifest is
	// a handful of 64-hex-plus-filename lines — kilobytes at most — so this is
	// generous while still finite against a hostile/mislinked asset.
	maxSumsBytes = 64 << 10
)

// ghRelease mirrors the subset of the GitHub Releases JSON we read.
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
	body, err := fetchJSON(ctx, releasesURL)
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

// CheckExperimental is the experimental-channel probe (Settings → Power
// user): ONE GET against the full releases list (prereleases included —
// listURL defaults to DefaultReleaseListURL) and the NEWEST published build
// wins whenever its tag differs from the running one. Unlike the stable
// channel it deliberately allows sideways and DOWNGRADE offers: hopping onto
// the test branch (whose prerelease tags rank below the stable they preview)
// and hopping back off it are both "updates" here, so semver-forward gating
// would strand people. The never-regress publishing rule still protects the
// STABLE feed; this is the opt-in exception, and the same strict per-platform
// assetMatch keeps the self-replace from ever renaming the WRONG asset (a .zip
// or .tar.gz) over the running binary. One residual macOS-only skew it does NOT
// cover — the bare-binary swap leaving a stale sibling lib/ behind across a
// dependency SONAME bump — is caught at swap time by StageReplace's
// preflightDarwinSwap (apply.go), which refuses before touching the install and
// points the user at the bundle tarball. See docs/CODE-SIGNING.md.
func CheckExperimental(ctx context.Context, listURL, current, assetMatch string) (*Release, error) {
	if current == "" || current == devVersion {
		return nil, nil // unstamped/dev build never self-updates
	}
	if listURL == "" {
		listURL = DefaultReleaseListURL
	}
	body, err := fetchJSON(ctx, listURL)
	if err != nil {
		return nil, err
	}
	var list []ghRelease
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("update: decoding release list: %w", err)
	}
	// GitHub returns the list newest-first: the first published (non-draft),
	// tagged entry is the channel's current build.
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

// fetchJSON performs the one bounded, UA-tagged GET both release channels share.
func fetchJSON(ctx context.Context, url string) ([]byte, error) {
	return fetchBounded(ctx, url, maxBodyBytes)
}

// fetchBounded is the one bounded, UA-tagged, timeout-capped GET the whole
// package shares (the release-list JSON and the SHA256SUMS manifest), with a
// per-call read cap so a tiny manifest doesn't inherit the JSON budget.
func fetchBounded(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
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
		return nil, fmt.Errorf("update: endpoint %s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

// newRelease converts a decoded GitHub release into the user-facing Release,
// resolving the platform asset by assetMatch (first name containing it wins;
// "" takes the first asset; no match leaves AssetURL empty) AND the SHA256SUMS
// asset for later verification. It scans every asset in ONE pass and does NOT
// early-break on the platform match, because the sums asset can appear either
// before or after the binary in the (server-ordered) list — an early break
// would drop it and silently disable verification.
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

// FetchSums downloads and parses the SHA256SUMS manifest at sumsURL, returning
// the hex digest published for assetName (matched exactly on the file name). It
// shares fetchJSON's bounded/UA-tagged/timeout discipline (a small maxSumsBytes
// cap — the manifest is tiny). A missing entry is an error: when a release
// publishes sums at all, EVERY shipped binary is listed, so a matched asset with
// no line means the manifest is wrong and the update must abort, not proceed
// unverified. Callers pass "" sumsURL for releases that shipped no manifest and
// skip verification entirely — they must not call this.
func FetchSums(ctx context.Context, sumsURL, assetName string) (string, error) {
	body, err := fetchBounded(ctx, sumsURL, maxSumsBytes)
	if err != nil {
		return "", err
	}
	hex, ok := parseSums(string(body), assetName)
	if !ok {
		return "", fmt.Errorf("update: %q missing from %s", assetName, sumsAssetName)
	}
	return hex, nil
}

// parseSums scans a SHA256SUMS manifest ("<hex>  <name>" per line, the
// sha256sum coreutils format; a leading "*" on the name marks binary mode) and
// returns the hex digest whose file name matches assetName. The name is
// compared on its base only, so a "dist/foo.exe" path in the manifest still
// matches "foo.exe".
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

// pathBase returns the final path element of a slash- or backslash-separated
// name (the sums manifest may carry a directory prefix). Kept local so the
// update package stays free of filepath's OS-specific separator behaviour.
func pathBase(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		return name[i+1:]
	}
	return name
}

// SelfUpdateAssetMatch returns the release-asset name substring that UNIQUELY
// identifies this platform's self-replaceable build, for Check's assetMatch.
//
// It is deliberately more specific than the bare GOOS. A release carries several
// assets whose name contains the OS token — the default build, the Discord-free
// build, and (on Windows) a DLL bundle .zip — and Check takes the FIRST match,
// so matching on "windows" alone is order-dependent and could hand the
// self-replace a .zip, which it would rename over the running .exe and brick the
// install. These tokens match EXACTLY the one swappable default binary per
// platform; the release workflow names assets to suit:
//
//	windows -> asyncao-windows-x86_64.exe     (bare exe; the runtime DLLs already sit beside it)
//	linux   -> AsyncAO-linux-x86_64.AppImage  (self-contained single file)
//	darwin  -> asyncao-macos-arm64            (arm64 binary, rewritten to load a sibling ./lib)
//
// Every non-default variant is named so it does NOT contain the platform's
// token: the Discord-free builds carry …-nodiscord…, the Windows DLL bundle is
// …-bundle.zip, and the macOS first-install tarballs are
// asyncao-macos-bundle-arm64.tar.gz / asyncao-macos-nodiscord-bundle-arm64.tar.gz.
// That last pair is the subtle one: the darwin token is the substring
// "macos-arm64", and "macos-bundle-arm64" does NOT contain it (the "-bundle-"
// breaks the run), so the self-updater picks the bare binary and never renames
// a .tar.gz over the running executable. An unknown GOOS falls back to the bare
// OS name (old behaviour: best-effort first match). Match is case-insensitive
// (Check lowercases both sides), so the .AppImage capitalisation is fine.
func SelfUpdateAssetMatch(goos string) string {
	switch goos {
	case "windows":
		return "windows-x86_64.exe"
	case "linux":
		return "linux-x86_64.appimage"
	case "darwin":
		return "macos-arm64"
	default:
		return goos
	}
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
