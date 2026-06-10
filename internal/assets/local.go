package assets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// LocalScheme prefixes asset "URLs" served from local mount folders in
// no-streaming mode. The origin embeds a hash of the mount set, so different
// mount configurations occupy disjoint cache keyspace — exactly like two
// asset hosts never colliding.
const LocalScheme = "local://"

// LocalFetcher serves asset bytes from user-chosen mount folders instead of
// the network: the "Legacy support for servers without an asset server"
// checkbox. Mounts are searched in order, first hit wins (AO2-Client mount
// path semantics — any folder layout, not just a default /base).
//
// It satisfies the manager's Fetcher contract; a file missing from every
// mount maps to network.ErrAssetNotFound so learned formats, fallback
// chains, and missing-asset warnings behave identically to streaming.
type LocalFetcher struct {
	mounts []string
	origin string
}

// NewLocalFetcher roots a fetcher at the given ordered mount folders (each
// containing characters/, background/, sounds/, ...). Empty entries are
// dropped.
func NewLocalFetcher(mounts []string) *LocalFetcher {
	cleaned := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m == "" {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(m))
	}
	// The origin identifies this exact mount set (order included) in cache
	// keys and the learned-format table.
	id := cache.Key(strings.Join(cleaned, string(os.PathListSeparator)))
	return &LocalFetcher{
		mounts: cleaned,
		origin: LocalScheme + "m-" + id + "/",
	}
}

// BaseURL returns the origin bases are built from: local://m-<mountset>/.
// Courtroom URL building appends the same relative paths it would append to
// an http asset URL.
func (l *LocalFetcher) BaseURL() string {
	return l.origin
}

// Mounts returns the resolved mount list (Settings UI display).
func (l *LocalFetcher) Mounts() []string {
	out := make([]string, len(l.mounts))
	copy(out, l.mounts)
	return out
}

// Fetch reads the file addressed by a local:// URL, trying each mount in
// order. The context is accepted for interface symmetry; local reads are not
// cancellable.
func (l *LocalFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	rel, ok := strings.CutPrefix(url, l.origin)
	if !ok {
		return nil, fmt.Errorf("assets: %q is not under local origin %q", url, l.origin)
	}
	// Reject path escapes; AO asset names never need "..".
	if strings.Contains(rel, "..") {
		return nil, fmt.Errorf("assets: refusing path escape %q", rel)
	}
	if len(l.mounts) == 0 {
		return nil, fmt.Errorf("%w: no local mount folders configured", network.ErrAssetNotFound)
	}
	relNative := filepath.FromSlash(rel)
	for _, mount := range l.mounts {
		data, err := os.ReadFile(filepath.Join(mount, relNative))
		if err == nil && len(data) > 0 {
			return data, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("assets: reading local asset %s: %w", filepath.Join(mount, relNative), err)
		}
	}
	return nil, fmt.Errorf("%w: %s (searched %d mounts)", network.ErrAssetNotFound, rel, len(l.mounts))
}
