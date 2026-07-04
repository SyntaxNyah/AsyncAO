package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "v1.0.0", 0}, // v prefix optional
		{"v1.2.0", "v1.1.9", 1},
		{"v1.1.9", "v1.2.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.0", "v1.0.0", 0},     // missing patch == 0
		{"v1", "v1.0.0", 0},       // missing minor+patch == 0
		{"v1.2.3", "v1.2.3.4", 0}, // 4th component ignored (only 3 read)
		// Prerelease ranks below the same core release.
		{"v1.2.0-rc1", "v1.2.0", -1},
		{"v1.2.0", "v1.2.0-rc1", 1},
		{"v1.2.0-rc2", "v1.2.0-rc1", 1},
		// Malformed never reads as newer than a real version.
		{"garbage", "v1.0.0", -1},
		{"v1.0.0", "garbage", 1},
	}
	for _, tc := range cases {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ghJSON is a minimal GitHub "latest release" payload.
const ghJSON = `{
  "tag_name": "v1.4.0",
  "body": "## What's new\n- Faster boot\n- Bug fixes",
  "html_url": "https://github.com/SyntaxNyah/AsyncAO/releases/tag/v1.4.0",
  "assets": [
    {"name": "asyncao-linux.tar.gz", "browser_download_url": "https://example/linux.tar.gz"},
    {"name": "asyncao-windows-amd64.zip", "browser_download_url": "https://example/win.zip"}
  ]
}`

// ghListJSON is a minimal releases-list payload, newest first: a draft (must
// be skipped), then a test-branch prerelease, then the stable it previews.
const ghListJSON = `[
  {"tag_name": "v9.9.9", "draft": true, "prerelease": true, "body": "wip", "html_url": "https://example/draft", "assets": []},
  {"tag_name": "v1.55.0-test.2", "prerelease": true, "body": "test notes", "html_url": "https://example/test",
   "assets": [{"name": "asyncao-windows-x86_64.exe", "browser_download_url": "https://example/test.exe"}]},
  {"tag_name": "v1.54.5", "prerelease": false, "body": "stable notes", "html_url": "https://example/stable",
   "assets": [{"name": "asyncao-windows-x86_64.exe", "browser_download_url": "https://example/stable.exe"}]}
]`

// TestCheckExperimental pins the experimental channel: drafts never offer,
// the newest published entry wins on ANY tag difference (sideways/downgrade
// included — that's how you hop on AND off the test branch), and running the
// channel's newest build reports nothing.
func TestCheckExperimental(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ghListJSON))
	}))
	defer srv.Close()

	// A stable build hops ONTO the test branch (the prerelease tag ranks
	// BELOW no one — it differs, so it offers).
	rel, err := CheckExperimental(context.Background(), srv.URL, "v1.54.5", "windows-x86_64.exe")
	if err != nil {
		t.Fatalf("CheckExperimental: %v", err)
	}
	if rel == nil || rel.Tag != "v1.55.0-test.2" {
		t.Fatalf("must offer the newest published build, got %+v", rel)
	}
	if !rel.Prerelease {
		t.Error("a prerelease offer must be flagged (the UI says 'test build')")
	}
	if rel.AssetURL != "https://example/test.exe" {
		t.Errorf("asset match picked %q", rel.AssetURL)
	}

	// Running the channel's newest build → nothing to do (the draft above it
	// never counts).
	if rel, err = CheckExperimental(context.Background(), srv.URL, "v1.55.0-test.2", "windows"); err != nil || rel != nil {
		t.Fatalf("current test build must report no update, got %+v err=%v", rel, err)
	}

	// A dev build never self-updates on any channel.
	if rel, err = CheckExperimental(context.Background(), srv.URL, "dev", ""); err != nil || rel != nil {
		t.Fatalf("dev build must never update, got %+v err=%v", rel, err)
	}
}

// TestCheckExperimentalOffersStableReturn pins the way back OFF the test
// branch: when the newest published entry is a STABLE release and we run a
// prerelease, it offers even though the semver may not rank higher.
func TestCheckExperimentalOffersStableReturn(t *testing.T) {
	const listStableFirst = `[
	  {"tag_name": "v1.54.5", "prerelease": false, "body": "n", "html_url": "https://example/s", "assets": []}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(listStableFirst))
	}))
	defer srv.Close()
	rel, err := CheckExperimental(context.Background(), srv.URL, "v1.54.5-test.9", "")
	if err != nil {
		t.Fatalf("CheckExperimental: %v", err)
	}
	if rel == nil || rel.Tag != "v1.54.5" || rel.Prerelease {
		t.Fatalf("a test build must be offered the newest stable, got %+v", rel)
	}
}

func TestCheckNewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("Check must send a User-Agent (GitHub rejects empty)")
		}
		_, _ = w.Write([]byte(ghJSON))
	}))
	defer srv.Close()

	rel, err := Check(context.Background(), srv.URL, "v1.2.0", "windows")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel == nil {
		t.Fatal("a higher tag must report an available update")
	}
	if rel.Version != "1.4.0" || rel.Tag != "v1.4.0" {
		t.Errorf("version = %q tag = %q", rel.Version, rel.Tag)
	}
	if rel.Notes == "" || rel.PageURL == "" {
		t.Errorf("patch notes and page URL must be carried: notes=%q url=%q", rel.Notes, rel.PageURL)
	}
	if rel.AssetURL != "https://example/win.zip" {
		t.Errorf("assetMatch %q picked the wrong asset: %q", "windows", rel.AssetURL)
	}
}

// ghMultiAssetJSON is a realistic release carrying, for every platform, the
// default build + the Discord-free build + (Windows) a DLL bundle .zip, in
// SCRAMBLED order. Asset matching is the brick-risk path: a wrong pick gets
// renamed over the running exe on the next self-update, so Check MUST land on
// the one swappable default binary per platform regardless of order.
const ghMultiAssetJSON = `{
  "tag_name": "v2.0.0",
  "body": "notes",
  "html_url": "https://example/rel",
  "assets": [
    {"name": "asyncao-windows-x86_64-bundle.zip",          "browser_download_url": "https://example/win-bundle.zip"},
    {"name": "AsyncAO-linux-x86_64-nodiscord.AppImage",    "browser_download_url": "https://example/linux-nd.AppImage"},
    {"name": "asyncao-macos-nodiscord-arm64",              "browser_download_url": "https://example/mac-nd"},
    {"name": "asyncao-windows-x86_64.exe",                 "browser_download_url": "https://example/win.exe"},
    {"name": "AsyncAO-linux-x86_64.AppImage",              "browser_download_url": "https://example/linux.AppImage"},
    {"name": "asyncao-windows-x86_64-nodiscord.exe",       "browser_download_url": "https://example/win-nd.exe"},
    {"name": "asyncao-macos-arm64",                        "browser_download_url": "https://example/mac"},
    {"name": "asyncao-windows-x86_64-nodiscord-bundle.zip","browser_download_url": "https://example/win-nd-bundle.zip"}
  ]
}`

// TestSelfUpdatePicksSwappableDefault pins that SelfUpdateAssetMatch + Check
// land on the bare/self-contained default binary for each platform — never the
// .zip bundle (which a self-replace would rename over the .exe) or the
// Discord-free variant.
func TestSelfUpdatePicksSwappableDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ghMultiAssetJSON))
	}))
	defer srv.Close()

	want := map[string]string{
		"windows": "https://example/win.exe",
		"linux":   "https://example/linux.AppImage",
		"darwin":  "https://example/mac",
	}
	for goos, wantURL := range want {
		rel, err := Check(context.Background(), srv.URL, "v1.0.0", SelfUpdateAssetMatch(goos))
		if err != nil {
			t.Fatalf("%s: Check: %v", goos, err)
		}
		if rel == nil {
			t.Fatalf("%s: a higher tag must report an update", goos)
		}
		if rel.AssetURL != wantURL {
			t.Errorf("%s: self-update picked %q, want the swappable default %q", goos, rel.AssetURL, wantURL)
		}
	}
}

func TestCheckNotNewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ghJSON))
	}))
	defer srv.Close()

	for _, current := range []string{"v1.4.0", "v1.5.0", "v2.0.0"} {
		rel, err := Check(context.Background(), srv.URL, current, "")
		if err != nil {
			t.Fatalf("Check(%s): %v", current, err)
		}
		if rel != nil {
			t.Errorf("current %s is >= latest, must report no update, got %+v", current, rel)
		}
	}
}

func TestCheckDevNeverUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a dev build must not even hit the endpoint")
		_, _ = w.Write([]byte(ghJSON))
	}))
	defer srv.Close()

	for _, current := range []string{"dev", ""} {
		rel, err := Check(context.Background(), srv.URL, current, "")
		if err != nil || rel != nil {
			t.Errorf("current %q: want (nil,nil), got (%+v,%v)", current, rel, err)
		}
	}
}

func TestCheckServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := Check(context.Background(), srv.URL, "v1.0.0", ""); err == nil {
		t.Fatal("a non-200 response must surface an error")
	}
}
