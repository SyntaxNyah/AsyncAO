// Package archive bundles a scene's assets into a folder so an .aorec keeps its visuals if the origin goes away.
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
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

// Result reports each asset type's resolved format, so replay can seed the resolver, plus size stats.
type Result struct {
	Formats map[string]string // AssetType.Name() → ext, e.g. "CharSprite" → ".webp"
	Files   int
	Bytes   int64
	// FromPack counts files from the user's local mounts; the caller warns, as that private art gets shared.
	FromPack int
}

// ExportAssets writes every asset SceneAssets enumerates through mgr, which knows the learned formats.
// A missing asset is skipped rather than aborting: it just 404s on replay too.
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
		if err := WriteAsset(destDir, rel, data); err != nil {
			// One unwritable name (a decoded ':' or '?' on Windows) must not sink the export; it simply 404s on replay.
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

// resolveRef probes candidates and alternate sprite spellings (EmoteAlts order). ResolveRawLayered checks
// local packs but never teaches learned formats or writes pack bytes into server-keyed caches.
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
			// A pack answers under a local:// URL, so return the server-side path; the transport URL would fail the
			// caller's prefix test and silently drop every pack asset.
			return base + path.Ext(url), data, true, true
		}
		return url, data, false, true
	}
	return "", nil, false, false
}

// WriteAsset writes at the decoded path; safepath refuses a ".." from a decoded "%5C", a rooted path, or a ':'.
func WriteAsset(destDir, rel string, data []byte) error {
	full, err := safepath.Join(destDir, DiskPath(rel))
	if err != nil {
		return fmt.Errorf("archive: refusing bad relative path %q: %w", rel, err)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// DiskPath percent-decodes segments and clamps "."/".." at the root, so folders stay readable to humans and
// other AO clients; replay resolves them because the local fetcher also tries a percent-decoded spelling.
func DiskPath(rel string) string {
	var out []string
	for _, seg := range strings.Split(rel, "/") {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			dec = seg // malformed escape (%zz) — keep the literal, never fabricate a name
		}
		// A decoded segment can hold "/" or "."/".."; split again so they become real separators, then clamp.
		for _, part := range strings.Split(dec, "/") {
			switch part {
			case "", ".":
				// Skip "" from a leading or double slash, and the current dir.
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

// SeedFormats teaches a replay resolver the archive's formats, so non-webp files resolve on the first probe.
func SeedFormats(resolver *assets.Resolver, origin string, formats map[string]string) {
	host := assets.HostOf(origin)
	for name, ext := range formats {
		if t, ok := assets.TypeFromName(name); ok {
			resolver.RecordSuccess(host, t, ext)
		}
	}
}
