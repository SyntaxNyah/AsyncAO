package render

import (
	"log"

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

	uploadErrs int64
	// transientErrs counts network-stage failures that reached the pump
	// (debug visibility; they are deliberately neither logged nor
	// negative-cached — see the upload closure).
	transientErrs int64
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
				p.transientErrs++
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
			p.uploadErrs++
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
