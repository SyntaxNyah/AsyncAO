package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestNormalizeAssetHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://mirror.example/base", "https://mirror.example/base/"},
		{"  http://mirror.example/base/  ", "http://mirror.example/base/"},
		{"https://mirror.example/base/?x=y#top", "https://mirror.example/base/"},
		{"mirror.example/base", ""},
		{"ftp://mirror.example/base", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeAssetHost(c.in); got != c.want {
			t.Errorf("NormalizeAssetHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanAssetHostsKeepsOrderDropsRepeatsAndCaps(t *testing.T) {
	in := []string{"https://b.example/", "not a url", "https://a.example", "https://b.example"}
	want := []string{"https://b.example/", "https://a.example/"}
	if got := CleanAssetHosts(in); !slices.Equal(got, want) {
		t.Fatalf("CleanAssetHosts = %v, want %v", got, want)
	}
	var many []string
	for i := range AssetHostCap + 1 {
		many = append(many, fmt.Sprintf("https://host%d.example/", i))
	}
	if got := CleanAssetHosts(many); len(got) != AssetHostCap {
		t.Fatalf("CleanAssetHosts kept %d hosts, want the cap of %d", len(got), AssetHostCap)
	}
}

func TestServerAssetHostsSurviveReload(t *testing.T) {
	const key = "wss://example.test/ws"
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.SetServerAssetHosts(key, []string{"https://second.example/base", "https://first.example/base"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	want := []string{"https://second.example/base/", "https://first.example/base/"}
	if got := r.Load().ServerAssetHosts(key); !slices.Equal(got, want) {
		t.Fatalf("reloaded hosts = %v, want %v", got, want)
	}
	if got := r.Load().ServerAssetHosts("wss://other.test/ws"); len(got) != 0 {
		t.Fatalf("another server has hosts %v, want none", got)
	}
}
