package render

import (
	"log"
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// Pump moves decoded assets from the manager onto the GPU each frame,
// honoring the §8 upload budget: live-message assets upload immediately,
// speculative (prefetch-ahead) uploads stop at speculativeUploadMaxTextures
// textures or speculativeUploadMaxBytes bytes per frame to protect 16 ms.
type Pump struct {
	store *TextureStore
	mgr   *assets.Manager
	// carry holds budget-deferred uploads for the next frame (bounded by
	// the manager's decoded channel capacity + this slice's own backlog).
	carry []assets.DecodedAsset
	// IsLive reports whether a base belongs to the message on screen
	// (bypasses the speculative budget).
	IsLive func(base string) bool

	// uploadErrs / transientErrs are debug-visibility counters surfaced in the
	// F8 panel (#50). atomic so the panel read (also render thread today, but
	// cheap and race-proof) never trips the detector against these hot-path
	// adds; they are add-only from Frame and never gate any behaviour.
	uploadErrs    atomic.Int64
	transientErrs atomic.Int64
}

// PumpStats is a lazy snapshot of the pump's debug counters for the F8 panel.
type PumpStats struct {
	UploadErrs    int64 // GPU texture-upload failures (over-budget canvas, OOM)
	TransientErrs int64 // network-stage failures that reached the pump (not cached)
}

// Stats snapshots the pump's error counters. Read on the render thread by the
// F8 diagnostics; the values are atomic loads, so it never blocks Frame.
func (p *Pump) Stats() PumpStats {
	return PumpStats{
		UploadErrs:    p.uploadErrs.Load(),
		TransientErrs: p.transientErrs.Load(),
	}
}

// NewPump wires the upload pump.
func NewPump(store *TextureStore, mgr *assets.Manager, isLive func(base string) bool) *Pump {
	if isLive == nil {
		isLive = func(string) bool { return false }
	}
	return &Pump{store: store, mgr: mgr, IsLive: isLive}
}

// Frame uploads pending decodes within budget. Render thread only.
func (p *Pump) Frame() {
	uploadedTextures := 0
	uploadedBytes := int64(0)

	withinBudget := func(d *assets.DecodedAsset) bool {
		if p.IsLive(d.Base) {
			return true // live-message uploads are never deferred
		}
		return uploadedTextures < speculativeUploadMaxTextures &&
			uploadedBytes < speculativeUploadMaxBytes
	}
	upload := func(d assets.DecodedAsset) {
		if d.Err != nil || d.Asset == nil {
			// TRANSIENT (network-stage) failures never enter the negative
			// cache: the bytes were never seen, so the asset is fine and the
			// next demand (message prefetch, scene warm) must retry it.
			// Pinning these for decodeFailTTL made one flaky-origin backoff
			// window blank every asset it touched for 30 s — the "whole
			// server's files go missing in waves" report. They're counted,
			// not logged (a backoff burst would flood the log).
			if d.Err != nil && d.Transient {
				p.transientErrs.Add(1)
				return
			}
			// A DECODE failure (corrupt/truncated bytes — DecodeImage only ever
			// errors on malformed payloads; a too-big canvas fails later at
			// UPLOAD, not here) means the cached bytes are poison. Purge them
			// from T2/T3 by the FULL fetch URL (d.URL, the T2/T3 key — never
			// d.Base) so the next demand refetches clean bytes instead of
			// re-promoting the same corrupt blob from disk forever. The negative
			// cache below still paces retries to one refetch per decodeFailTTL
			// window, so this cannot thrash the network.
			if d.Err != nil {
				p.mgr.PurgeCorrupt(d.URL)
			}
			// PROVENANCE SPLIT (v1.89.0 layering). d.Base is the SERVER's URL even
			// when the bytes came from the user's local pack, so MarkFailed(d.Base)
			// below would take the SERVER's perfectly good copy of this asset offline
			// for decodeFailTTL because ONE file in a hand-assembled pack is damaged.
			// Quarantine the pack ENTRY instead and leave the base clean, so the very
			// next demand re-enters resolution, skips the bad entry, and fetches the
			// server's copy.
			if d.Err != nil && d.FromPack {
				if p.mgr.QuarantinePack(d.URL) {
					log.Printf("render: local pack file %s failed to decode (%v) — using the server's copy instead", d.URL, d.Err)
				}
				// A progressively-decoded animation uploads frame 0 successfully and can
				// THEN fail on the full set, leaving a partial page resident under the
				// SERVER's base. Resolution short-circuits on a resident texture, so
				// without dropping it here the server's copy would never be fetched and
				// the quarantine alone could not heal the asset.
				p.store.Remove(d.Base)
				// Deliberately NO MarkFailed and no clearFailed: on the pack path the
				// negative cache is left untouched entirely. Bytes that are provably not
				// the server's must never gate the server's own base — including when
				// QuarantinePack returns false (the layer was swapped mid-decode, or the
				// URL belongs to a replay archive).
				return
			}
			// Record to the negative cache so the manager's prefetch gate backs
			// off — and log only once per decodeFailTTL, not on every retry.
			if d.Err != nil && p.store.MarkFailed(d.Base) {
				log.Printf("render: asset %s failed: %v", d.Base, d.Err)
			}
			return
		}
		// Progressive ordering guard: the budget carry can reorder a
		// first-frame partial AFTER its own full set — uploading it then
		// would regress the resident animation to one frame.
		if d.Asset.Partial {
			if page, ok := p.store.Get(d.Base); ok && len(page.Frames) > len(d.Asset.Frames) {
				d.Asset.Release()
				return
			}
		}
		bytes := d.Asset.PixelBytes()
		// Small-UI art (emote buttons, char icons) uploads into the shield
		// tier so sprite streaming can never evict the visible grids (the
		// "emote buttons visibly refresh" churn — textures.go).
		var err error
		if smallTexTier(d.Type) {
			err = p.store.UploadSmall(d.Base, d.Asset)
		} else {
			err = p.store.Upload(d.Base, d.Asset)
		}
		if err != nil {
			p.uploadErrs.Add(1)
			log.Printf("render: texture upload %s failed: %v", d.Base, err)
			return
		}
		uploadedTextures++
		uploadedBytes += bytes
	}

	// Carried-over deferrals first (older speculation).
	kept := p.carry[:0]
	for i := range p.carry {
		d := p.carry[i]
		if withinBudget(&d) {
			upload(d)
		} else {
			kept = append(kept, d)
		}
	}
	p.carry = kept

	// Fresh decodes.
	for {
		select {
		case d := <-p.mgr.Decoded():
			if withinBudget(&d) {
				upload(d)
			} else {
				p.carry = append(p.carry, d)
			}
		default:
			return
		}
	}
}
