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
	"net/url"
	"os"
	"path"
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
	// FromPack counts files that came from the user's own local mounts rather
	// than the server. The caller warns on it: those bytes are the user's private
	// art, and a bundle is a file they hand to someone else.
	FromPack int
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
		url, data, fromPack, ok := resolveRef(ctx, mgr, ref)
		if !ok {
			continue
		}
		rel, under := strings.CutPrefix(url, origin)
		if !under {
			continue // external host (e.g. an http music link) — not part of THIS origin
		}
		rel = strings.TrimPrefix(rel, "/")
		if fromPack {
			res.FromPack++
		}
		if err := writeAsset(destDir, rel, data); err != nil {
			// One un-writable name (e.g. a decoded segment carrying a
			// Windows-illegal character like ':' or '?') must not sink the whole
			// export — skip it and keep bundling, the same way the renderer degrades
			// on a single missing asset. It simply 404s on replay, like any gap.
			continue
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
// It resolves through the user's local packs as well as the server (the
// ResolveRawLayered path, which never teaches the server's learned formats and
// never writes pack bytes into the server-keyed caches), and reports which
// answered so the caller can both place the file correctly and warn about
// bundling private art.
func resolveRef(ctx context.Context, mgr *assets.Manager, ref courtroom.AssetRef) (string, []byte, bool, bool) {
	if ref.Exact {
		if data, err := mgr.FetchRaw(ctx, ref.Base); err == nil && len(data) > 0 {
			return ref.Base, data, false, true
		}
		return "", nil, false, false
	}
	for _, base := range append([]string{ref.Base}, ref.Alts...) {
		if base == "" {
			continue
		}
		url, data, fromPack, ok := mgr.ResolveRawLayered(base, ref.Type)
		if !ok {
			continue
		}
		if fromPack {
			// A pack answers under a local:// transport URL, which is NOT under the
			// origin. Return the SERVER-side path the asset would have been fetched
			// at instead — base plus whichever extension the pack file carried — so
			// the caller's origin cut works uniformly and the bundled file lands
			// exactly where replay will look for it. Returning the transport URL
			// would fail that prefix test and silently drop every pack asset.
			return base + path.Ext(url), data, true, true
		}
		return url, data, false, true
	}
	return "", nil, false, false
}

// writeAsset writes one bundled asset under destDir at its DECODED, human-readable
// relative path (DiskPath), refusing path escapes.
func writeAsset(destDir, rel string, data []byte) error {
	disk := DiskPath(rel)
	if disk == "" || strings.Contains(disk, "..") {
		return fmt.Errorf("archive: refusing bad relative path %q", rel)
	}
	full := filepath.Join(destDir, filepath.FromSlash(disk))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// DiskPath maps an origin-relative URL path to the clean, human-readable path a
// bundle writes on disk: it percent-DECODES each segment (so "%20"→space,
// "%5B"→"[", "%28"→"(", and a "%2F"→a real separator) and lexically collapses
// "."/".." clamped at the root. Without this, a bundle recorded every asset at
// its raw URL spelling — "characters/drio%2Fbyakuya%20togami", "sounds/music/
// daily%20life/%5Bresign%5D…" — folders no human or other AO client can read
// (#40 follow-up). A URL MUST escape spaces, so the clean names can only be
// recovered here, at the URL→disk boundary; replay resolves them because the
// local fetcher already tries a percent-decoded spelling (LocalFetcher.Fetch
// Attempt 2), keeping the bundle replayable byte-for-byte.
func DiskPath(rel string) string {
	var out []string
	for _, seg := range strings.Split(rel, "/") {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			dec = seg // malformed escape (%zz) — keep the literal, never fabricate a name
		}
		// A decoded segment can itself hold "/" (a decoded "%2F") or "."/".."; split
		// again so those become real separators / traversal, then clamp.
		for _, part := range strings.Split(dec, "/") {
			switch part {
			case "", ".":
				// skip empty ("" from a leading/double slash) and current-dir
			case "..":
				if len(out) > 0 {
					out = out[:len(out)-1] // pop — clamped, can never escape the bundle root
				}
			default:
				out = append(out, part)
			}
		}
	}
	return strings.Join(out, "/")
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
