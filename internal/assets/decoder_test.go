package assets

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/kettek/apng"
)

// --- fixtures (generated in-memory; stdlib formats only) ----------------------

func encodePNG(t testing.TB, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeGIF(t testing.TB, frames int) []byte {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: 8, Height: 8}}
	palette := color.Palette{color.Transparent, color.Black, color.White}
	for i := 0; i < frames; i++ {
		img := image.NewPaletted(image.Rect(0, 0, 8, 8), palette)
		for p := range img.Pix {
			img.Pix[p] = uint8(1 + i%2)
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 5) // 50 ms
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func encodeAPNG(t testing.TB, frames int) []byte {
	t.Helper()
	a := apng.APNG{}
	for i := 0; i < frames; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for p := 0; p < len(img.Pix); p += 4 {
			img.Pix[p] = uint8(50 * (i + 1))
			img.Pix[p+3] = 0xFF
		}
		a.Frames = append(a.Frames, apng.Frame{
			Image:            img,
			DelayNumerator:   1,
			DelayDenominator: 20, // 50 ms
		})
	}
	var buf bytes.Buffer
	if err := apng.Encode(&buf, a); err != nil {
		t.Fatalf("encode apng: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t testing.TB) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// --- sniffing ------------------------------------------------------------------

func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want Format
	}{
		{"png", encodePNG(t, 4, 4, color.White), FormatPNG},
		{"apng", encodeAPNG(t, 2), FormatAPNG},
		{"gif", encodeGIF(t, 2), FormatGIF},
		{"jpeg", encodeJPEG(t), FormatJPEG},
		{"ogg", []byte("OggS\x00more"), FormatOgg},
		{"wav", append([]byte("RIFF\x10\x00\x00\x00WAVE"), make([]byte, 8)...), FormatWAV},
		{"mp3-id3", []byte("ID3\x04\x00rest"), FormatMP3},
		{"mp3-sync", []byte{0xFF, 0xFB, 0x90, 0x00}, FormatMP3},
		{"webp-static", webpStaticHeader(), FormatWebP},
		{"webp-anim", webpAnimHeader(), FormatWebPAnim},
		{"junk", []byte{0xDE, 0xAD, 0xBE, 0xEF}, FormatUnknown},
		{"short", []byte{0x89}, FormatUnknown},
	}
	for _, tc := range cases {
		if got := Sniff(tc.data); got != tc.want {
			t.Errorf("Sniff(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// webpStaticHeader fabricates a minimal RIFF/WEBP/VP8 header (sniffable, not
// decodable).
func webpStaticHeader() []byte {
	b := []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	return append(b, make([]byte, 16)...)
}

// webpAnimHeader fabricates a RIFF/WEBP/VP8X header with the ANIM flag.
func webpAnimHeader() []byte {
	b := []byte("RIFF\x30\x00\x00\x00WEBPVP8X")
	b = append(b, 0x0A, 0x00, 0x00, 0x00) // VP8X chunk size
	b = append(b, vp8xANIMFlag)           // flags
	return append(b, make([]byte, 12)...)
}

func TestSniffIsImage(t *testing.T) {
	for f, want := range map[Format]bool{
		FormatPNG: true, FormatAPNG: true, FormatWebP: true, FormatWebPAnim: true,
		FormatGIF: true, FormatJPEG: true,
		FormatOgg: false, FormatWAV: false, FormatMP3: false, FormatUnknown: false,
	} {
		if got := f.IsImage(); got != want {
			t.Errorf("%v.IsImage() = %v, want %v", f, got, want)
		}
	}
}

// --- decoding ------------------------------------------------------------------

func TestDecodePNGStatic(t *testing.T) {
	d, err := DecodeImage(encodePNG(t, 16, 12, color.RGBA{R: 255, A: 255}), true)
	if err != nil {
		t.Fatalf("DecodeImage(png): %v", err)
	}
	defer d.Release()
	if d.Animated || len(d.Frames) != 1 || d.Width != 16 || d.Height != 12 {
		t.Errorf("png decode: animated=%v frames=%d %dx%d", d.Animated, len(d.Frames), d.Width, d.Height)
	}
	if got := d.Frames[0].RGBAAt(8, 6); got.R != 255 || got.A != 255 {
		t.Errorf("pixel = %+v, want red", got)
	}
	if d.PixelBytes() != 16*12*4 {
		t.Errorf("PixelBytes = %d, want %d", d.PixelBytes(), 16*12*4)
	}
}

func TestDecodeGIFAnimated(t *testing.T) {
	d, err := DecodeImage(encodeGIF(t, 3), true)
	if err != nil {
		t.Fatalf("DecodeImage(gif): %v", err)
	}
	defer d.Release()
	if !d.Animated || len(d.Frames) != 3 || len(d.Delays) != 3 {
		t.Fatalf("gif decode: animated=%v frames=%d delays=%d", d.Animated, len(d.Frames), len(d.Delays))
	}
	for i, delay := range d.Delays {
		if delay != 50*time.Millisecond {
			t.Errorf("frame %d delay = %v, want 50ms", i, delay)
		}
	}
}

func TestDecodeGIFFirstFrameOnly(t *testing.T) {
	// PlayAnimations=false: decode only the first frame but still report
	// the payload as animated (decode-level toggle, spec §4).
	d, err := DecodeImage(encodeGIF(t, 3), false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	if !d.Animated {
		t.Error("Animated flag lost with PlayAnimations=false")
	}
	if len(d.Frames) != 1 {
		t.Errorf("frames = %d, want 1", len(d.Frames))
	}
}

func TestDecodeAPNGAnimated(t *testing.T) {
	d, err := DecodeImage(encodeAPNG(t, 2), true)
	if err != nil {
		t.Fatalf("DecodeImage(apng): %v", err)
	}
	defer d.Release()
	if !d.Animated || len(d.Frames) != 2 {
		t.Fatalf("apng decode: animated=%v frames=%d", d.Animated, len(d.Frames))
	}
	for i, delay := range d.Delays {
		if delay != 50*time.Millisecond {
			t.Errorf("frame %d delay = %v, want 50ms", i, delay)
		}
	}
	// Frame pixels must differ (composition actually happened).
	if d.Frames[0].Pix[0] == d.Frames[1].Pix[0] {
		t.Error("apng frames identical; composition broken")
	}
}

func TestDecodeJPEG(t *testing.T) {
	d, err := DecodeImage(encodeJPEG(t), true)
	if err != nil {
		t.Fatalf("DecodeImage(jpeg): %v", err)
	}
	defer d.Release()
	if d.Animated || len(d.Frames) != 1 {
		t.Errorf("jpeg decode: animated=%v frames=%d", d.Animated, len(d.Frames))
	}
}

func TestDecodeRejectsJunk(t *testing.T) {
	if _, err := DecodeImage([]byte("definitely not an image"), true); err == nil {
		t.Error("junk payload decoded without error")
	}
}

// --- decoder pool ---------------------------------------------------------------

func TestDecoderPoolDeliversResults(t *testing.T) {
	p := NewDecoderPool(2)
	defer p.Close()

	results := make(chan *Decoded, 4)
	errs := make(chan error, 4)
	payload := encodePNG(t, 8, 8, color.White)
	for i := 0; i < 4; i++ {
		ok := p.Submit(DecodeRequest{
			URL:            "http://example.com/a.png",
			Data:           payload,
			Type:           AssetTypeCharSprite,
			PlayAnimations: true,
			OnDone: func(_ string, d *Decoded, err error) {
				if err != nil {
					errs <- err
					return
				}
				results <- d
			},
		})
		if !ok {
			t.Fatal("Submit refused while pool open")
		}
	}
	for i := 0; i < 4; i++ {
		select {
		case d := <-results:
			d.Release()
		case err := <-errs:
			t.Fatalf("decode error: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("decoder pool never delivered")
		}
	}
	if s := p.Stats(); s.Decoded != 4 || s.Failed != 0 {
		t.Errorf("Stats = %+v", s)
	}
}

func TestDecoderPoolCloseFailsQueuedJobs(t *testing.T) {
	p := NewDecoderPool(1)
	p.Close()
	heard := make(chan error, 1)
	ok := p.Submit(DecodeRequest{
		URL:  "x",
		Data: nil,
		OnDone: func(_ string, _ *Decoded, err error) {
			heard <- err
		},
	})
	if ok {
		t.Error("Submit accepted after Close")
	}
	select {
	case err := <-heard:
		if err == nil {
			t.Error("expected error for post-Close submit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-Close submit never heard back")
	}
}

func TestPixPoolRoundTrip(t *testing.T) {
	buf, token := getPixBuf(1024)
	if len(buf) != 1024 {
		t.Fatalf("len = %d", len(buf))
	}
	for i := range buf {
		buf[i] = 0xAA
	}
	putPixBuf(token)
	buf2, token2 := getPixBuf(2048)
	defer putPixBuf(token2)
	for i, b := range buf2 {
		if b != 0 {
			t.Fatalf("recycled buffer not zeroed at %d", i)
		}
	}
}

func TestPixPoolOversizeUnpooled(t *testing.T) {
	huge := pixClassSizes[len(pixClassSizes)-1] + 1
	buf, token := getPixBuf(huge)
	if token != nil {
		t.Error("oversize buffer claims pool token")
	}
	if len(buf) != huge {
		t.Errorf("len = %d, want %d", len(buf), huge)
	}
	putPixBuf(token) // must be a no-op
}

// TestBoundedFrameCount pins the per-asset decode budget: animations
// truncate to the frames whose decoded bytes fit maxDecodedAssetBytes
// (community sprite packs ship hundreds of full-canvas frames; unbounded,
// one page outgrew the whole T1 budget and the character became invisible
// when its owner talked). The clamp never drops below one frame.
func TestBoundedFrameCount(t *testing.T) {
	const w, h = 1000, 1000 // 4 MB per decoded frame
	fits := int(maxDecodedAssetBytes.Load()) / (w * h * rgbaBytesPerPixel)
	if fits < 2 {
		t.Fatalf("test geometry no longer fits the budget (fits=%d)", fits)
	}
	cases := []struct {
		name           string
		w, h, in, want int
	}{
		{"under budget untouched", w, h, fits - 1, fits - 1},
		{"exactly at budget", w, h, fits, fits},
		{"over budget truncated", w, h, fits + 50, fits},
		{"giant canvas clamps to one frame", 8000, 8000, 10, 1},
		{"degenerate canvas left alone", 0, 0, 7, 7},
	}
	for _, tc := range cases {
		if got := boundedFrameCount(tc.w, tc.h, tc.in); got != tc.want {
			t.Errorf("%s: boundedFrameCount(%d,%d,%d) = %d, want %d",
				tc.name, tc.w, tc.h, tc.in, got, tc.want)
		}
	}
}

// TestDecodeGIFDecimatesOversizedAnimation runs a real decode through the
// budget: a GIF whose frame count exceeds the cap decodes to exactly the
// bounded frame count, still flagged Animated — and, crucially, DECIMATED not
// TRUNCATED: the kept frames span the whole clip and the total playback time is
// preserved (a truncation would drop the tail and run short, snapping a long
// preanim to its talking pose a quarter of the way through — the bug this fixes).
func TestDecodeGIFDecimatesOversizedAnimation(t *testing.T) {
	const w, h = 500, 500
	fits := int(maxDecodedAssetBytes.Load()) / (w * h * rgbaBytesPerPixel)
	frames := fits + 3

	g := &gif.GIF{Config: image.Config{Width: w, Height: h}}
	pal := color.Palette{color.Black, color.White}
	for i := 0; i < frames; i++ {
		img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 5) // 5 centiseconds/frame
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}

	d, err := DecodeImage(buf.Bytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	if len(d.Frames) != fits {
		t.Errorf("decoded %d frames, want decimation to %d", len(d.Frames), fits)
	}
	if !d.Animated {
		t.Error("decimated animation must stay flagged Animated")
	}
	// Decimation preserves total duration; truncation to `fits` frames would
	// lose the last 3 frames' delays and run 3×50 ms short.
	var got time.Duration
	for _, dl := range d.Delays {
		got += dl
	}
	want := time.Duration(frames) * gifFrameDelay(g, 0)
	if got != want {
		t.Errorf("decimated total duration = %v, want %v (whole clip, delays folded)", got, want)
	}
}

// TestFrameDecimator pins the decimation primitive: exactly `keep` frames are
// materialised, spanning both endpoints (0 and total-1), with skipped frames'
// delays folded forward so the total is preserved.
func TestFrameDecimator(t *testing.T) {
	const total, keep = 100, 34
	fd := newFrameDecimator(total, keep)
	const per = 33 * time.Millisecond

	var kept []int
	var sum time.Duration
	for i := 0; i < total; i++ {
		if dur, ok := fd.step(i, per); ok {
			kept = append(kept, i)
			sum += dur
		}
	}
	if len(kept) != keep {
		t.Fatalf("kept %d frames, want %d", len(kept), keep)
	}
	if kept[0] != 0 {
		t.Errorf("first kept frame = %d, want 0 (start pose)", kept[0])
	}
	if kept[len(kept)-1] != total-1 {
		t.Errorf("last kept frame = %d, want %d (final pose)", kept[len(kept)-1], total-1)
	}
	for i := 1; i < len(kept); i++ {
		if kept[i] <= kept[i-1] {
			t.Fatalf("kept indices not strictly increasing at %d: %v", i, kept)
		}
	}
	if want := time.Duration(total) * per; sum != want {
		t.Errorf("folded total = %v, want %v (no time lost to decimation)", sum, want)
	}

	// keep ≥ total is the identity: every frame kept, delays unchanged.
	id := newFrameDecimator(5, 9)
	var n int
	for i := 0; i < 5; i++ {
		if dur, ok := id.step(i, per); ok {
			n++
			if dur != per {
				t.Errorf("identity frame %d folded delay = %v, want %v", i, dur, per)
			}
		}
	}
	if n != 5 {
		t.Errorf("identity kept %d frames, want 5 (no decimation below budget)", n)
	}
}

// TestDecoderThumbnailsFixedCellTypes pins decode-time thumbnailing: types
// drawn at fixed small cells (char icons, emote buttons) rescale to their
// cell size inside the decode pool, so a 500×500 pack icon costs ~16 KB of
// T1 instead of ~1 MB; every other type keeps its native size.
func TestDecoderThumbnailsFixedCellTypes(t *testing.T) {
	pool := NewDecoderPool(1)
	defer pool.Close()

	decode := func(data []byte, typ AssetType) *Decoded {
		t.Helper()
		done := make(chan *Decoded, 1)
		ok := pool.Submit(DecodeRequest{
			URL: "x", Data: data, Type: typ, PlayAnimations: true,
			OnDone: func(_ string, d *Decoded, err error) {
				if err != nil {
					t.Errorf("decode failed: %v", err)
				}
				done <- d
			},
		})
		if !ok {
			t.Fatal("submit refused")
		}
		select {
		case d := <-done:
			return d
		case <-time.After(managerWait):
			t.Fatal("decode timed out")
			return nil
		}
	}

	big := encodePNG(t, 256, 256, color.RGBA{R: 9, G: 99, B: 199, A: 255})

	icon := decode(big, AssetTypeCharIcon)
	if icon.Width != charIconDecodePx || icon.Height != charIconDecodePx {
		t.Errorf("char icon decoded to %dx%d, want %dx%d thumbnail",
			icon.Width, icon.Height, charIconDecodePx, charIconDecodePx)
	}
	icon.Release()

	button := decode(big, AssetTypeEmoteButton)
	if button.Width != emoteButtonDecodePx {
		t.Errorf("emote button decoded to %dx%d, want %d px thumbnail",
			button.Width, button.Height, emoteButtonDecodePx)
	}
	button.Release()

	sprite := decode(big, AssetTypeCharSprite)
	if sprite.Width != 256 || sprite.Height != 256 {
		t.Errorf("sprite decoded to %dx%d, want native 256x256", sprite.Width, sprite.Height)
	}
	sprite.Release()

	// Native art already at cell size passes through untouched.
	exact := decode(encodePNG(t, 40, 40, color.White), AssetTypeEmoteButton)
	if exact.Width != 40 || exact.Height != 40 {
		t.Errorf("native-size button rescaled to %dx%d", exact.Width, exact.Height)
	}
	exact.Release()
}
