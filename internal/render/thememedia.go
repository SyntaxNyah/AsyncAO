package render

// Theme media, the bounded user-art tier: internal/ui plans before decoding, and UploadThemeMedia refuses.

import (
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

const (
	// ThemeMediaKeyPrefix and ThemeGenKeyPrefix are theme:// sub-namespaces; exported as internal/ui builds the keys.
	ThemeMediaKeyPrefix = "theme://m/"
	ThemeGenKeyPrefix   = "theme://g/"

	// The allowance scales off the user's T1 budget (37.5%): a fixed amount would overcommit a small T1.
	themeMediaBudgetNum, themeMediaBudgetDen = 3, 8
	// Floor and ceiling at the ends of T1's range: the ratio alone fits no full backdrop, or exceeds decoration.
	themeMediaByteFloor = 8 << 20
	themeMediaByteCeil  = 48 << 20
	// themeMediaItemDiv bounds one page at budget/8, half the per-asset T1 decode cap, so refusals are predictable.
	themeMediaItemDiv = 8

	// ThemeMediaCap and ThemeGenCap bound counts, as bytes alone don't bound map, prune and upload costs.
	ThemeMediaCap = 24
	ThemeGenCap   = 12
)

// ThemeMediaByteCap is the allowance for a T1 budget; internal/ui's cap calls it, so the two can't disagree.
func ThemeMediaByteCap(texBudgetMiB int) int64 {
	budget := int64(texBudgetMiB) << mibShift
	if budget <= 0 {
		budget = int64(T1BudgetBytes)
	}
	cap := budget * themeMediaBudgetNum / themeMediaBudgetDen
	cap = value.Clamp(cap, themeMediaByteFloor, themeMediaByteCeil)
	return cap
}

// ThemeMediaItemByteCap is the most one page may take; separate because the user's fix differs.
func ThemeMediaItemByteCap(texBudgetMiB int) int64 {
	return ThemeMediaByteCap(texBudgetMiB) / themeMediaItemDiv
}

// ThemeMediaKey builds the imported-art key, the one place that spells it.
func ThemeMediaKey(themeID, mediaID string) string {
	return ThemeMediaKeyPrefix + themeID + "/" + mediaID
}

// ThemeGenKey builds a tile's content-addressed key in fixed-width hex, so runs of a theme give identical keys.
func ThemeGenKey(hash uint64) string {
	return fmt.Sprintf("%s%016x", ThemeGenKeyPrefix, hash)
}

// IsThemeMediaKey reports whether a key is in either sub-namespace.
func IsThemeMediaKey(key string) bool {
	return strings.HasPrefix(key, ThemeMediaKeyPrefix) || strings.HasPrefix(key, ThemeGenKeyPrefix)
}

// ErrThemeMediaBudget is the store's refusal; reaching it means internal/ui's planner disagreed with the store.
var ErrThemeMediaBudget = fmt.Errorf("render: theme media exceeds its budget")

// UploadThemeMedia is UploadPinned with the budget check; it releases the Decoded on every path. Render thread.
func (s *TextureStore) UploadThemeMedia(key string, d *assets.Decoded, texBudgetMiB int) error {
	if !IsThemeMediaKey(key) {
		d.Release()
		return fmt.Errorf("render: %q is not a theme-media key (%s… / %s…)", key, ThemeMediaKeyPrefix, ThemeGenKeyPrefix)
	}
	// Replacing a key is always allowed: refusing a page the theme already declared leaves it worse off.
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
	// Measure before upload, as checking later means creating textures only to destroy them.
	want := d.PixelBytes()
	item := ThemeMediaItemByteCap(texBudgetMiB)
	if want > item {
		d.Release()
		return fmt.Errorf("%w: one page is %d bytes, per-item cap %d", ErrThemeMediaBudget, want, item)
	}
	// A replacement is measured minus what it gives back, or re-uploading at the cap would refuse itself.
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

// ThemeMediaBytes is the resident theme-media payload in bytes.
func (s *TextureStore) ThemeMediaBytes() int64 { return s.themeMediaBytes }

// ThemeMediaCount is how many imported-art pages are resident, excluding generator tiles; render thread only.
func (s *TextureStore) ThemeMediaCount() int { return s.themeMediaCount }

// ThemeGenCount is how many generator tiles are resident; render thread only.
func (s *TextureStore) ThemeGenCount() int { return s.themeGenCount }

// noteThemeMediaAdded/Removed keep the ledger in step; only UploadThemeMedia, Remove and Purge may call them.
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
