package render

// Theme media — the bounded user-art tier (v1.90.0 W4).
// docs/wip/THEME-EDITOR-DESIGN.md §Q2.
//
// A native theme may declare imported art (`[media]`) and procedural generator
// tiles (`generator =`). Both land as PINNED texture pages, because chrome losing
// an eviction fight against streaming sprites is the exact black-flash / glitching-
// buttons defect UploadPinned was introduced to fix. Pinned means eviction-exempt,
// and eviction-exempt means the ceiling has to live at the door instead.
//
// THE KEYS RIDE theme://. Reusing the reserved scheme (rather than inventing a
// second `userimg://`) inherits reservedKey(), the App's pollThemeApply upload loop,
// its theme-swap prune, themePage's generation-keyed negative cache and healTheme's
// paced re-kick with zero new code — the single largest code reduction in this
// design.
//
//	theme://m/<themeid>/<mediaid>   imported art   (themeid = the sanitized folder)
//	theme://g/<paramhash>           generator tile (content-addressed)
//
// THE THEME NAMESPACE ON m/ IS MANDATORY, and it is a correctness requirement, not
// tidiness. The swap prune drops the keys the NEW theme does not declare; with bare
// ids (`theme://m/bg`) two themes that both declare `bg` leave the old page resident
// and the new theme silently renders the old texture. `g/` needs no namespace
// because a content address cannot collide with a different content.
//
// TWO ENFORCEMENT POINTS, because a bug in one is a budget breach and the budget IS
// the product: internal/ui plans the whole media set against ThemeMediaByteCap
// BEFORE it decodes anything, and UploadThemeMedia refuses at the store.
// UploadPinned rejects both sub-prefixes outright, so no future caller can route
// around the accounting by reaching for the older, un-gated door.

import (
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

const (
	// ThemeMediaKeyPrefix / ThemeGenKeyPrefix are the two sub-namespaces of
	// theme://. EXPORTED because internal/ui builds the keys and this package
	// rejects everything else that tries: one spelling, in one place, checked at
	// the door.
	ThemeMediaKeyPrefix = "theme://m/"
	ThemeGenKeyPrefix   = "theme://g/"

	// themeMediaBudgetNum / themeMediaBudgetDen scale the theme-media allowance off
	// the user's OWN T1 budget rather than fixing it. At the default 64 MiB that is
	// 24 MiB — 37.5% of T1, landing total commitment at T2 128 + T1 64 + 24 = 216 of
	// the 256 MiB GOMEMLIMIT. A fixed 48 MiB (the obvious alternative) would be 75%
	// of the default T1 and a 1.5x overcommit at the 32 MiB minimum.
	themeMediaBudgetNum, themeMediaBudgetDen = 3, 8
	// themeMediaByteFloor / themeMediaByteCeil clamp the ratio at the ends of the
	// 32..256 MiB range it is applied to: the ratio alone gives 12 MiB at 32 (too
	// little to hold one full-canvas backdrop) and 96 MiB at 256 (past the point
	// where pinned art is a decoration budget at all).
	themeMediaByteFloor = 8 << 20
	themeMediaByteCeil  = 48 << 20
	// themeMediaItemDiv bounds ONE page at budget/8 — exactly half the per-asset T1
	// decode cap (cache/memory.go's decodeCapBudgetDiv = 4). 8 MiB at the default.
	// Sitting at exactly half the decode cap is what makes decimation PREDICTABLE
	// rather than emergent: an animation the decoder was willing to hand over can
	// still be refused here, and it is refused for a reason with a number attached.
	themeMediaItemDiv = 8

	// ThemeMediaCap / ThemeGenCap bound the COUNTS (hard rule 4). Bytes alone are
	// not a bound: 24 tiny pages and 24 000 tiny pages have the same byte cost and
	// very different map, prune and upload costs.
	//
	// 24 covers every one of the fourteen shipped style presets with headroom (the
	// densest declares none at all — they are generator-only by design). 12 tiles at
	// GenTileMaxPx² ARGB is 3 MiB, inside the media budget at every T1 size.
	ThemeMediaCap = 24
	ThemeGenCap   = 12
)

// ThemeMediaByteCap is the LIVE theme-media allowance for a T1 budget, in bytes.
//
// It is a FUNCTION rather than a constant because TexBudgetMiB is a power-user
// preference: someone who raised T1 to 256 MiB to hold a 4000-character roster has
// not thereby asked for 96 MiB of pinned decoration, and someone who dropped it to
// 32 MiB on a small machine must still be able to apply a theme. internal/ui's
// producer-side cap is an alias of this call, never a copy, so the planner and the
// store can never disagree about the number.
func ThemeMediaByteCap(texBudgetMiB int) int64 {
	budget := int64(texBudgetMiB) << 20
	if budget <= 0 {
		budget = int64(T1BudgetBytes)
	}
	cap := budget * themeMediaBudgetNum / themeMediaBudgetDen
	if cap < themeMediaByteFloor {
		cap = themeMediaByteFloor
	}
	if cap > themeMediaByteCeil {
		cap = themeMediaByteCeil
	}
	return cap
}

// ThemeMediaItemByteCap is the most ONE page may take out of that allowance.
// Named separately from the whole because the two refusals mean different things to
// the user and get different inline fixes: over the item cap is "downscale this
// image", over the total is "remove something".
func ThemeMediaItemByteCap(texBudgetMiB int) int64 {
	return ThemeMediaByteCap(texBudgetMiB) / themeMediaItemDiv
}

// ThemeMediaKey builds the imported-art key for a theme id and a media id. The ONE
// producer of the spelling, so the prune, the planner and the painter cannot
// disagree about what a page is called.
func ThemeMediaKey(themeID, mediaID string) string {
	return ThemeMediaKeyPrefix + themeID + "/" + mediaID
}

// ThemeGenKey builds the content-addressed key for a generator tile. Hex, lower
// case, fixed width — so two runs of the same theme produce byte-identical keys and
// the disk-free tier still behaves like a cache.
func ThemeGenKey(hash uint64) string {
	return fmt.Sprintf("%s%016x", ThemeGenKeyPrefix, hash)
}

// IsThemeMediaKey reports whether a key belongs to either sub-namespace — the
// predicate UploadPinned refuses on and the swap prune selects on.
func IsThemeMediaKey(key string) bool {
	return strings.HasPrefix(key, ThemeMediaKeyPrefix) || strings.HasPrefix(key, ThemeGenKeyPrefix)
}

// ErrThemeMediaBudget is returned when an upload would breach the byte allowance
// or one of the two counts. It is a REFUSAL AT THE STORE, the second of the two
// enforcement points; internal/ui's planner is supposed to have caught it first and
// turned it into a self-describing placeholder element, so reaching here at all
// means the planner and the store disagreed and the budget won.
var ErrThemeMediaBudget = fmt.Errorf("render: theme media exceeds its budget")

// UploadThemeMedia is UploadPinned with the admission gate. It is the ONLY producer
// of theme://m/ and theme://g/ pages.
//
// The Decoded is released on every path including the refusals, because the caller
// handed ownership over: a refusal that leaked the decode buffer would make the
// budget check itself the leak.
//
// Render thread only.
func (s *TextureStore) UploadThemeMedia(key string, d *assets.Decoded, texBudgetMiB int) error {
	if !IsThemeMediaKey(key) {
		d.Release()
		return fmt.Errorf("render: %q is not a theme-media key (%s… / %s…)", key, ThemeMediaKeyPrefix, ThemeGenKeyPrefix)
	}
	// The counts first: they are the cheap check, and they are the one a byte
	// budget cannot express. Replacing an EXISTING key is always allowed — it is a
	// re-upload of a page the theme already declared (a heal after a purge, or the
	// editor changing one image), and refusing it would leave the theme worse than
	// before.
	_, replacing := s.pinned[key]
	if !replacing {
		gen := strings.HasPrefix(key, ThemeGenKeyPrefix)
		if gen && s.themeGenCount >= ThemeGenCap {
			d.Release()
			return fmt.Errorf("%w: %d generator tiles already resident (cap %d)", ErrThemeMediaBudget, s.themeGenCount, ThemeGenCap)
		}
		if !gen && s.themeMediaCount >= ThemeMediaCap {
			d.Release()
			return fmt.Errorf("%w: %d images already resident (cap %d)", ErrThemeMediaBudget, s.themeMediaCount, ThemeMediaCap)
		}
	}
	// The bytes, measured BEFORE the upload: PixelBytes is the decoded payload and
	// the page's own byte count is that same number, so the check is exact rather
	// than optimistic. Checking after building the page would mean creating textures
	// only to destroy them, which is the leak uploadTier's own cap check exists to
	// close one tier down.
	want := d.PixelBytes()
	item := ThemeMediaItemByteCap(texBudgetMiB)
	if want > item {
		d.Release()
		return fmt.Errorf("%w: one page is %d bytes, per-item cap %d", ErrThemeMediaBudget, want, item)
	}
	// A replacement frees its predecessor's bytes, so it is measured against the
	// budget MINUS what it is about to give back. Without this, re-uploading the
	// same page at the cap would refuse itself.
	resident := s.themeMediaBytes
	if old, ok := s.pinned[key]; ok {
		resident -= old.bytes
	}
	if total := ThemeMediaByteCap(texBudgetMiB); resident+want > total {
		d.Release()
		return fmt.Errorf("%w: %d resident + %d incoming exceeds %d", ErrThemeMediaBudget, resident, want, total)
	}
	return s.uploadPinnedTracked(key, d, true)
}

// The three residency readouts.
//
// RENDER THREAD ONLY. They read plain ints written by noteThemeMediaAdded /
// noteThemeMediaRemoved on the upload and prune paths, which are render-thread-only
// by construction — so these are plain reads rather than atomics, and every caller
// has to be on that thread too.
//
// The doc here used to say they feed "the 1 Hz metrics sampler", which is a
// GOROUTINE (internal/metrics), and that is the dangerous kind of wrong: the
// neighbouring store counters are atomics for exactly that reason, so a reader
// taking the sentence at face value would wire an unsynchronised int read into a
// sampler and get a -race failure (hard rule 8) that reads like a bug in the
// sampler. The live callers are the editor's budget bar and the F8 debug panel,
// both on the render thread. If a sampler ever does want these, PROMOTE the fields
// to atomics first — do not relax the sentence again.
//
// ThemeMediaBytes is the resident theme-media payload, in bytes.
func (s *TextureStore) ThemeMediaBytes() int64 { return s.themeMediaBytes }

// ThemeMediaCount is how many imported-art pages are resident (generator tiles are
// counted separately — they are a different cap because they are a different cost).
// Render thread only; see ThemeMediaBytes.
func (s *TextureStore) ThemeMediaCount() int { return s.themeMediaCount }

// ThemeGenCount is how many generator tiles are resident. Render thread only; see
// ThemeMediaBytes.
func (s *TextureStore) ThemeGenCount() int { return s.themeGenCount }

// noteThemeMediaAdded / noteThemeMediaRemoved keep the two counters and the byte
// total in step with the pinned map. They are called from the THREE places that can
// change it — UploadThemeMedia, Remove and Purge — and from nowhere else, which is
// what makes "the accounting matches the map" checkable rather than hopeful
// (TestPinnedBytesStayUnderThemeBudgetAcrossThreeApplies walks the map and compares).
func (s *TextureStore) noteThemeMediaAdded(key string, bytes int64) {
	if !IsThemeMediaKey(key) {
		return
	}
	s.themeMediaBytes += bytes
	if strings.HasPrefix(key, ThemeGenKeyPrefix) {
		s.themeGenCount++
		return
	}
	s.themeMediaCount++
}

func (s *TextureStore) noteThemeMediaRemoved(key string, bytes int64) {
	if !IsThemeMediaKey(key) {
		return
	}
	s.themeMediaBytes -= bytes
	if s.themeMediaBytes < 0 {
		s.themeMediaBytes = 0 // an accounting bug must not become a negative budget
	}
	if strings.HasPrefix(key, ThemeGenKeyPrefix) {
		if s.themeGenCount > 0 {
			s.themeGenCount--
		}
		return
	}
	if s.themeMediaCount > 0 {
		s.themeMediaCount--
	}
}
