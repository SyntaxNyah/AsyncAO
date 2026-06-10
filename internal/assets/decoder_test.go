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
	// the payload as animated (decode-level toggle, PROMPT.md §4).
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
