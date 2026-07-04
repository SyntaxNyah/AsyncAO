package render

import (
	"image"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestApplyVariant pins the per-pixel sprite-style transforms on an ABGR8888 buffer
// (R,G,B,A per pixel): invert negates RGB and keeps alpha; grayscale uses Rec.601
// luma and keeps alpha; none is a no-op. Pure maths — no SDL.
func TestApplyVariant(t *testing.T) {
	// Invert: RGB negated, alpha untouched (incl. a semi-transparent pixel).
	inv := []byte{10, 20, 30, 255, 200, 100, 50, 128}
	applyVariant(inv, 2, 1, uint8(courtroom.VariantInvert))
	if want := []byte{245, 235, 225, 255, 55, 155, 205, 128}; !equalBytes(inv, want) {
		t.Errorf("invert = %v, want %v", inv, want)
	}

	// Grayscale: each pixel → its luma, alpha kept. Red 255 → 76; mix → 140.
	gray := []byte{255, 0, 0, 255, 100, 150, 200, 64}
	applyVariant(gray, 2, 1, uint8(courtroom.VariantGrayscale))
	if want := []byte{76, 76, 76, 255, 140, 140, 140, 64}; !equalBytes(gray, want) {
		t.Errorf("grayscale = %v, want %v", gray, want)
	}

	// None: untouched.
	none := []byte{10, 20, 30, 255}
	applyVariant(none, 1, 1, uint8(courtroom.VariantNone))
	if want := []byte{10, 20, 30, 255}; !equalBytes(none, want) {
		t.Errorf("none changed the buffer: %v", none)
	}
}

// TestApplyVariantRestyles pins a few of the "10 more restyles" per-pixel transforms (#M5+):
// redscale keeps luma in the red channel, threshold is 1-bit, infrared rotates channels — and
// alpha is always preserved.
func TestApplyVariantRestyles(t *testing.T) {
	red := []byte{100, 150, 50, 200}
	applyVariant(red, 1, 1, uint8(courtroom.VariantRedscale)) // luma 123 → red channel only
	if want := []byte{123, 0, 0, 200}; !equalBytes(red, want) {
		t.Errorf("redscale = %v, want %v", red, want)
	}
	th := []byte{100, 150, 50, 200}
	applyVariant(th, 1, 1, uint8(courtroom.VariantThreshold)) // luma 123 ≤ 127 → black, alpha kept
	if want := []byte{0, 0, 0, 200}; !equalBytes(th, want) {
		t.Errorf("threshold = %v, want %v", th, want)
	}
	ir := []byte{100, 150, 50, 200}
	applyVariant(ir, 1, 1, uint8(courtroom.VariantInfrared)) // channel rotate R<-G<-B, alpha kept
	if want := []byte{150, 50, 100, 200}; !equalBytes(ir, want) {
		t.Errorf("infrared = %v, want %v", ir, want)
	}
}

// TestApplyPixelArt pins the #77 mosaic: a block is averaged ALPHA-WEIGHTED (so a
// transparent neighbour can't drag the colour toward black), palette-quantised, and
// written back uniformly; the block's alpha becomes the mean. Here w<block, so both
// pixels are one cell: colour = the opaque pixel's (200,100,50) quantised to the
// 6-level palette (204,102,51); alpha = mean(255,0) = 127.
func TestApplyPixelArt(t *testing.T) {
	pix := []byte{200, 100, 50, 255, 0, 0, 0, 0}
	applyVariant(pix, 2, 1, uint8(courtroom.VariantPixelArt))
	if want := []byte{204, 102, 51, 127, 204, 102, 51, 127}; !equalBytes(pix, want) {
		t.Errorf("pixel art = %v, want %v", pix, want)
	}
}

// TestVariantPageInverts is the end-to-end proof: upload a known 2×1 base, build its
// invert variant, and read the variant's pixels back — confirming the render-target
// readback yields STRAIGHT (non-premultiplied) pixels (the NONE-blend copy), the
// transform is applied, and a repeat call is cached. Skips if the headless renderer
// has no render-target support.
func TestVariantPageInverts(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := &assets.Decoded{
		Width: 2, Height: 1,
		Frames: []*image.RGBA{{
			Pix:    []byte{10, 20, 30, 255, 200, 100, 50, 128}, // one opaque + one semi-transparent pixel
			Stride: 8,
			Rect:   image.Rect(0, 0, 2, 1),
		}},
	}
	if err := store.Upload("base/x", base); err != nil {
		t.Fatalf("upload: %v", err)
	}

	v, ok := store.VariantPage("base/x", courtroom.VariantInvert)
	if !ok {
		t.Skip("render targets unavailable on this headless renderer")
	}
	if len(v.Frames) != 1 {
		t.Fatalf("variant frames = %d, want 1", len(v.Frames))
	}
	got, err := store.readbackFrame(v.Frames[0], 2, 1)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if want := []byte{245, 235, 225, 255, 55, 155, 205, 128}; !equalBytes(got, want) {
		t.Errorf("inverted variant pixels = %v, want %v (RGB negated, alpha kept)", got, want)
	}
	if v2, _ := store.VariantPage("base/x", courtroom.VariantInvert); v2 != v {
		t.Error("variant page must be cached (same pointer on a repeat call)")
	}
}

// TestApplyPaint pins the hue-paint colorize ramp (the v1.53.5 darkening fix): white
// stays white, black stays black, a mid-luma pixel takes the paint colour exactly,
// alpha is untouched, and brightness ORDER is preserved (a lighter source pixel is
// never darker after the paint) — the old grayscale×tint multiply failed the white
// case for every saturated hue, which was the "it gets darkened anyway" report.
func TestApplyPaint(t *testing.T) {
	// White / black / mid-gray / a semi-transparent colour pixel, painted red.
	pix := []byte{
		255, 255, 255, 255, // white → white (highlights survive)
		0, 0, 0, 255, // black → black (shadows survive)
		127, 127, 127, 255, // mid luma 127 → the paint colour itself
		40, 80, 160, 96, // arbitrary colour, alpha 96 → alpha kept
	}
	applyPaint(pix, 4, 1, 255, 0, 0, 0, 0, 0, 0)
	if want := []byte{255, 255, 255, 255}; !equalBytes(pix[0:4], want) {
		t.Errorf("white painted = %v, want %v (highlights must stay bright)", pix[0:4], want)
	}
	if want := []byte{0, 0, 0, 255}; !equalBytes(pix[4:8], want) {
		t.Errorf("black painted = %v, want %v", pix[4:8], want)
	}
	if want := []byte{255, 0, 0, 255}; !equalBytes(pix[8:12], want) {
		t.Errorf("mid-gray painted = %v, want %v (midtone = the paint colour)", pix[8:12], want)
	}
	if pix[15] != 96 {
		t.Errorf("alpha changed: %d, want 96", pix[15])
	}

	// Monotonic: painting must preserve the light/shadow ORDER of the sprite.
	prev := -1
	for y := 0; y <= 255; y += 5 {
		g := byte(y)
		p := []byte{g, g, g, 255}
		applyPaint(p, 1, 1, 60, 220, 90, 0, 0, 0, 0)
		l := luma601(p[0], p[1], p[2])
		if l < prev {
			t.Fatalf("luma order broken at source %d: painted luma %d < previous %d", y, l, prev)
		}
		prev = l
	}
}

// TestApplyPaintTwoTone pins the two-band paint ("head red, rest blue"): on an 8-row
// mid-gray column split at 50%, the top rows carry colour A, the bottom rows colour B,
// and the feather band lerps between them row by row (mid-gray luma 127 makes each
// output pixel exactly the row's colour, so the geometry is byte-checkable).
func TestApplyPaintTwoTone(t *testing.T) {
	const h = 8
	pix := make([]byte, h*4)
	for i := 0; i < h*4; i += 4 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = 127, 127, 127, 255
	}
	applyPaint(pix, 1, h, 255, 0, 0, 0, 0, 255, 50) // split at 50% → row 4; feather = paintFeatherMin = 2
	want := [h][3]byte{
		{255, 0, 0}, {255, 0, 0}, {255, 0, 0}, // rows 0..2: pure A
		{191, 0, 63},             // row 3: 1/4 into the feather band
		{127, 0, 127},            // row 4: the split row, half-way
		{63, 0, 191},             // row 5: 3/4
		{0, 0, 255}, {0, 0, 255}, // rows 6..7: pure B
	}
	for row := 0; row < h; row++ {
		i := row * 4
		if pix[i] != want[row][0] || pix[i+1] != want[row][1] || pix[i+2] != want[row][2] {
			t.Errorf("row %d = %d,%d,%d, want %d,%d,%d", row,
				pix[i], pix[i+1], pix[i+2], want[row][0], want[row][1], want[row][2])
		}
		if pix[i+3] != 255 {
			t.Errorf("row %d alpha changed: %d", row, pix[i+3])
		}
	}
}

// TestPaintSplitQuantize pins the split-key granularity: snapped to paintSplitQuant
// steps and clamped so a split can never quantise into "no split" or an empty band.
func TestPaintSplitQuantize(t *testing.T) {
	for _, tc := range []struct{ in, want uint8 }{
		{1, paintSplitQuant}, {2, 2}, {3, 2}, {50, 50}, {51, 50}, {99, 100 - paintSplitQuant},
	} {
		if got := paintSplitQuantize(tc.in); got != tc.want {
			t.Errorf("paintSplitQuantize(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestPaintQuantize pins the variant-key quantiser: endpoints exact (0→0, 255→255 —
// a pure hue must not round to an off-hue), and only paintQuantizeLevels distinct
// outputs across the whole channel range (the page-count bound).
func TestPaintQuantize(t *testing.T) {
	if q := paintQuantize(0); q != 0 {
		t.Errorf("paintQuantize(0) = %d, want 0", q)
	}
	if q := paintQuantize(255); q != 255 {
		t.Errorf("paintQuantize(255) = %d, want 255", q)
	}
	seen := map[uint8]bool{}
	for c := 0; c <= 255; c++ {
		seen[paintQuantize(uint8(c))] = true
	}
	if len(seen) != paintQuantizeLevels {
		t.Errorf("distinct quantised values = %d, want %d", len(seen), paintQuantizeLevels)
	}
	r, g, b := unpackPaint(packPaint(12, 200, 255))
	if r != 12 || g != 200 || b != 255 {
		t.Errorf("pack/unpack round-trip = %d,%d,%d", r, g, b)
	}
}

// TestEvictVariantPrefersPaint pins the variant-cache bound's policy: past the cap,
// a hue-paint entry is evicted before a classic effect (the silhouette/invert pages
// are hot every frame while in use; paint pages pile up only during colour tuning).
func TestEvictVariantPrefersPaint(t *testing.T) {
	bp := &TexturePage{variants: map[variantKey]*TexturePage{
		{effect: uint8(courtroom.VariantInvert)}:     {},
		{effect: variantPaint, paintA: 0xFF0000}:     {},
		{effect: uint8(courtroom.VariantSilhouette)}: {},
	}}
	evictVariant(bp)
	if len(bp.variants) != 2 {
		t.Fatalf("variants after evict = %d, want 2", len(bp.variants))
	}
	if _, still := bp.variants[variantKey{effect: variantPaint, paintA: 0xFF0000}]; still {
		t.Error("paint entry must be the preferred eviction victim")
	}
	// With no paint entry left, eviction still frees SOMETHING (the bound holds).
	evictVariant(bp)
	if len(bp.variants) != 1 {
		t.Errorf("variants after second evict = %d, want 1", len(bp.variants))
	}
}

// TestPaintPageBuildsAndCaches is the hue-paint end-to-end proof (mirrors
// TestVariantPageInverts): a known base colorized red — white pixel stays white,
// the readback confirms the paint pixels, and a repeat call with a slightly
// different colour that QUANTISES the same returns the cached page.
func TestPaintPageBuildsAndCaches(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := &assets.Decoded{
		Width: 2, Height: 1,
		Frames: []*image.RGBA{{
			Pix:    []byte{255, 255, 255, 255, 127, 127, 127, 255}, // white + mid-gray
			Stride: 8,
			Rect:   image.Rect(0, 0, 2, 1),
		}},
	}
	if err := store.Upload("base/paint", base); err != nil {
		t.Fatalf("upload: %v", err)
	}

	v, ok := store.PaintPage("base/paint", 255, 0, 0, 0, 0, 0, 0)
	if !ok {
		t.Skip("render targets unavailable on this headless renderer")
	}
	got, err := store.readbackFrame(v.Frames[0], 2, 1)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if want := []byte{255, 255, 255, 255, 255, 0, 0, 255}; !equalBytes(got, want) {
		t.Errorf("painted pixels = %v, want %v (white kept, midtone = paint colour)", got, want)
	}
	if v2, _ := store.PaintPage("base/paint", 254, 1, 1, 0, 0, 0, 0); v2 != v { // same after quantising
		t.Error("paint page must be cached across quantiser-equal colours")
	}
	// With NO split the second colour must not affect the key (one entry per colour A).
	if v3, _ := store.PaintPage("base/paint", 255, 0, 0, 9, 9, 9, 0); v3 != v {
		t.Error("splitless paint page must ignore the second colour in its key")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
