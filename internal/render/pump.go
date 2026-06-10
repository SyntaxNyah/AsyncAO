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
			if d.Err != nil {
				log.Printf("render: asset %s failed: %v", d.Base, d.Err)
			}
			return
		}
		bytes := d.Asset.PixelBytes()
		if err := p.store.Upload(d.Base, d.Asset); err != nil {
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
