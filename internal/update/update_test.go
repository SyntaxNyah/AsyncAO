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
