// Package archive bundles a scene's assets into a self-contained folder so a
// recorded/built `.aorec` keeps its visuals even if the origin CDN goes away.
// It resolves every asset the scene needs through the SAME manager candidate
// logic the renderer uses (Manager.ResolveRaw / SceneAssets), then writes each
// at the exact origin-relative path replay will later request — symmetry by
// construction, proved by the export→replay round-trip test.
package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Result reports an export: the per-asset-type format extension each type
// resolved to (so a replay over the archive can seed the resolver and find the
// bundled files without re-probing), plus size stats for the UI.
type Result struct {
	Formats map[string]string // AssetType.Name() → ext, e.g. "CharSprite" → ".webp"
	Files   int
	Bytes   int64
}

// ExportAssets resolves every asset SceneAssets enumerates for the scene through
// mgr (the live/source manager, which knows the learned formats), and writes the
// bytes into destDir at the origin-relative path the asset resolved to. De-duped
// upstream by SceneAssets, so each unique asset is fetched + written once (small
// archives). Missing assets are skipped — the zero-fallback renderer degrades
// gracefully, so a 404 here just 404s on replay too, never aborts the export.
//
// The returned Formats is the archive's manifest: replay seeds the resolver from
// it so the bundled (possibly non-webp) files resolve on the first probe.
func ExportAssets(ctx context.Context, mgr *assets.Manager, origin, startBg string, events []courtroom.Event, destDir string) (*Result, error) {
	urls := courtroom.NewURLBuilder(origin)
	refs := courtroom.SceneAssets(urls, startBg, events)
	res := &Result{Formats: make(map[string]string)}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return res, err // cancelled — keep what we wrote
		}
		url, data, ok := resolveRef(ctx, mgr, ref)
		if !ok {
			continue
		}
		rel, under := strings.CutPrefix(url, origin)
		if !under {
			continue // external host (e.g. an http music link) — not part of THIS origin
		}
		rel = strings.TrimPrefix(rel, "/")
		if err := writeAsset(destDir, rel, data); err != nil {
			return res, err
		}
		res.Files++
		res.Bytes += int64(len(data))
		if !ref.Exact {
			if ext := filepath.Ext(rel); ext != "" {
				res.Formats[ref.Type.Name()] = ext
			}
		}
	}
	return res, nil
}

// resolveRef fetches one asset's bytes + the concrete URL it lives at. Exact refs
// (music) are a direct fetch; bases probe candidates, walking the alternate
// sprite spellings (bare X, then the "(a)/X" folder — EmoteAlts order).
func resolveRef(ctx context.Context, mgr *assets.Manager, ref courtroom.AssetRef) (string, []byte, bool) {
	if ref.Exact {
		if data, err := mgr.FetchRaw(ctx, ref.Base); err == nil && len(data) > 0 {
			return ref.Base, data, true
		}
		return "", nil, false
	}
	if url, data, ok := mgr.ResolveRaw(ref.Base, ref.Type); ok {
		return url, data, true
	}
	for _, alt := range ref.Alts {
		if alt == "" {
			continue
		}
		if url, data, ok := mgr.ResolveRaw(alt, ref.Type); ok {
			return url, data, true
		}
	}
	return "", nil, false
}

// writeAsset writes one bundled asset under destDir at its forward-slash relative
// path, refusing path escapes.
func writeAsset(destDir, rel string, data []byte) error {
	if rel == "" || strings.Contains(rel, "..") {
		return fmt.Errorf("archive: refusing bad relative path %q", rel)
	}
	full := filepath.Join(destDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// SeedFormats teaches a replay resolver the formats the archive bundled (keyed
// by the archive's own origin/host) so the bundled — possibly non-webp — files
// resolve on the first candidate probe instead of missing under the webp-first
// default list. Call before replaying a bundled archive.
func SeedFormats(resolver *assets.Resolver, origin string, formats map[string]string) {
	host := assets.HostOf(origin)
	for name, ext := range formats {
		if t, ok := assets.TypeFromName(name); ok {
			resolver.RecordSuccess(host, t, ext)
		}
	}
}
