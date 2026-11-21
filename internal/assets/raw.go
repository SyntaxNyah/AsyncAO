package assets

import (
	"context"
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// playerFiles marks the source of the player's own folders and packs, so a lookup can say a file came from them.
type playerFiles interface{ playerFiles() }

func (f *bridgeFolders) playerFiles() {}

// Raw reads a complete URL through the media system without decoding it, waiting for it, so call it off the render
// thread. A file found nowhere is network.ErrAssetNotFound; it is asked quietly, as no one shows a missing raw file.
func Raw(ctx context.Context, m *media.Manager, url string) ([]byte, error) {
	a, err := m.Load(ctx, RawKey(url), media.Now, media.Quiet)
	if err != nil {
		return nil, err
	}
	return rawResult(a, url)
}

// Found is where Resolve found a file: the URL that answered, its bytes, and whether the player's folders held it.
type Found struct {
	URL      string
	Data     []byte
	FromPack bool
}

// Resolve finds base as an asset of type t in the live order: memory and the disk cache, the player's folders in every
// format, then the web, whose hits teach the host's format. Unlisted also asks the web for the formats the host's list
// leaves out, for export. It waits, so call it off the render thread.
func Resolve(ctx context.Context, m *media.Manager, base string, t AssetType, unlisted bool) (Found, bool) {
	key := ChainKey(base, t)
	key.Kind = media.KindRaw
	flags := media.Quiet
	if unlisted {
		flags |= media.Unlisted
	}
	a, err := m.Load(ctx, key, media.Now, flags)
	if err != nil {
		return Found{}, false
	}
	data, err := rawResult(a, base)
	if err != nil || len(data) == 0 {
		return Found{}, false
	}
	hit := a.Hit()
	_, pack := hit.Source.(playerFiles)
	return Found{URL: string(hit.File), Data: data, FromPack: pack}, true
}

// rawResult is a finished raw Asset's bytes, or why it has none.
func rawResult(a *media.Asset, name string) ([]byte, error) {
	if data, ok := a.Result().([]byte); ok && a.Ready() {
		return data, nil
	}
	if a.State() == media.Missing {
		return nil, fmt.Errorf("%w: %s", network.ErrAssetNotFound, name)
	}
	if err := a.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("assets: %s ended %v", name, a.State())
}
