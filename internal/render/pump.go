package render

import (
	"log"
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// Pump uploads decoded assets each frame: live-message assets at once, speculative ones within budget.
type Pump struct {
	store *TextureStore
	mgr   *assets.Manager
	// carry holds budget-deferred uploads, bounded by the manager's decoded channel capacity plus its backlog.
	carry []assets.DecodedAsset
	// IsLive reports whether a base belongs to the message on screen, which bypasses the budget.
	IsLive func(base string) bool

	// uploadErrs and transientErrs are debug counters; atomic so the panel read can't race.
	uploadErrs    atomic.Int64
	transientErrs atomic.Int64
}

// PumpStats is a snapshot of the pump's debug counters.
type PumpStats struct {
	UploadErrs    int64 // GPU texture-upload failures (over-budget canvas, OOM)
	TransientErrs int64 // network-stage failures that reached the pump (not cached)
}

// Stats snapshots the error counters using atomic loads, so it never blocks Frame.
func (p *Pump) Stats() PumpStats {
	return PumpStats{
		UploadErrs:    p.uploadErrs.Load(),
		TransientErrs: p.transientErrs.Load(),
	}
}

func NewPump(store *TextureStore, mgr *assets.Manager, isLive func(base string) bool) *Pump {
	if isLive == nil {
		isLive = func(string) bool { return false }
	}
	return &Pump{store: store, mgr: mgr, IsLive: isLive}
}

// Frame uploads pending decodes within budget; render thread only.
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
			// Network-stage failures skip the negative cache: the bytes were never seen, so the next demand must retry.
			if d.Err != nil && d.Transient {
				p.transientErrs.Add(1)
				return
			}
			// Bad cached bytes: purge T2/T3 by d.URL (their key, never d.Base), or the same blob re-promotes forever.
			if d.Err != nil {
				p.mgr.PurgeCorrupt(d.URL)
			}
			// d.Base is the server's URL even for pack bytes, so MarkFailed would hide the server's good copy.
			if d.Err != nil && d.FromPack {
				if p.mgr.QuarantinePack(d.URL) {
					log.Printf("render: local pack file %s failed to decode (%v) — using the server's copy instead", d.URL, d.Err)
				}
				// A partial page left resident after the full set fails would stop the server's copy being fetched.
				p.store.Remove(d.Base)
				// No MarkFailed or clearFailed: pack bytes must never affect the server's base.
				return
			}
			// Back off the prefetch, logging once per decodeFailTTL.
			if d.Err != nil && p.store.MarkFailed(d.Base) {
				log.Printf("render: asset %s failed: %v", d.Base, d.Err)
			}
			return
		}
		// The carry can reorder a partial after its own full set; uploading it would reduce the animation to one frame.
		if d.Asset.Partial && !d.Asset.Stream {
			if page, ok := p.store.Get(d.Base); ok && len(page.Frames) > len(d.Asset.Frames) {
				d.Asset.Release()
				return
			}
		}
		bytes := d.Asset.PixelBytes()
		if d.Asset.Animated && !d.Asset.Stream {
			// Log before uploading, as the upload releases the frames.
			log.Printf("[anim-upload] %s animated=%v frames=%d bytes=%dMiB", d.Base, d.Asset.Animated, len(d.Asset.Frames), bytes>>mibShift)
		}
		// Small UI art goes to the small tier, so sprite streaming can't evict the visible grids.
		var err error
		if d.Asset.Stream {
			err = p.store.AppendStream(d.Base, d.Asset)
		} else if smallTexTier(d.Type) {
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

	// Carried-over deferrals first.
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
