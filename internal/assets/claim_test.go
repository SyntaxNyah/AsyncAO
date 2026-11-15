package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
)

const (
	claimTestSide   = 8
	claimTestFrames = 3
	claimFrameBytes = claimTestSide * claimTestSide * rgbaBytesPerPixel

	// claimBigSide makes each background claim a quarter of imageClaimLimit.
	claimBigSide   = 2048
	claimBigShare  = imageClaimLimit / (claimBigSide * claimBigSide * rgbaBytesPerPixel)
	claimBigAsks   = 2 * claimBigShare
	claimTestQuiet = 200 * time.Millisecond
	// claimTestWait covers several 16 MiB decodes on one background worker under the race detector on a small runner.
	claimTestWait = time.Minute
)

// encodeGIFGlobal is a GIF whose frames share one global colour table.
func encodeGIFGlobal(t *testing.T, frames int) []byte {
	t.Helper()
	palette := color.Palette{color.Transparent, color.Black, color.White}
	g := &gif.GIF{Config: image.Config{Width: claimTestSide, Height: claimTestSide, ColorModel: palette}}
	for range frames {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, claimTestSide, claimTestSide), palette))
		g.Delay = append(g.Delay, 1)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeClaimReadsEveryFormatsHeader(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want int64
	}{
		{"png", encodePNG(t, claimTestSide, claimTestSide, color.White), claimFrameBytes},
		{"apng", encodeAPNG(t, claimTestFrames), claimTestFrames * claimFrameBytes},
		{"gif with a table per frame", encodeGIF(t, claimTestFrames), claimTestFrames * claimFrameBytes},
		{"gif with one global table", encodeGIFGlobal(t, claimTestFrames), claimTestFrames * claimFrameBytes},
		{"jpeg", encodeJPEG(t), claimFrameBytes},
		{"not an image", []byte("OggS\x00more"), unknownImageClaim},
	}
	for _, c := range cases {
		if got := decodeClaim(c.data, true); got != c.want {
			t.Errorf("%s: claim %d, want %d", c.name, got, c.want)
		}
	}
	if got := decodeClaim(encodeAPNG(t, claimTestFrames), false); got != claimFrameBytes {
		t.Errorf("an animation with animations off claims %d, want one frame %d", got, claimFrameBytes)
	}
}

func TestDecodeClaimStopsAtTheAnimatedBudget(t *testing.T) {
	const budget = claimFrameBytes * (claimTestFrames - 1)
	old := maxAnimatedDecodedAssetBytes.Swap(budget)
	t.Cleanup(func() { maxAnimatedDecodedAssetBytes.Store(old) })
	if got := decodeClaim(encodeAPNG(t, claimTestFrames), true); got != budget {
		t.Fatalf("claim %d, want the animated budget %d", got, budget)
	}
}

func TestDecodeClaimSurvivesTruncatedHeaders(t *testing.T) {
	for _, data := range [][]byte{encodeAPNG(t, claimTestFrames), encodeGIF(t, claimTestFrames), encodeJPEG(t)} {
		for n := range len(data) {
			if got := decodeClaim(data[:n], true); got <= 0 {
				t.Fatalf("%d bytes of a %s claim %d", n, Sniff(data), got)
			}
		}
	}
}

func TestBackgroundDecodesWaitForUploads(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, claimBigSide, claimBigSide))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	host := &bridgeFakeHost{files: map[string][]byte{}}
	var bases []string
	for i := range claimBigAsks + 1 {
		base := fmt.Sprintf("%sbackground/b/%d", bridgeTestOrigin, i)
		host.files[base+".png"] = buf.Bytes()
		bases = append(bases, base)
	}
	rig := newBridgeRig(t, host)
	for _, base := range bases[:claimBigAsks] {
		rig.manager.Media().Get(ChainKey(base, AssetTypeBackground), media.Later, 0) // AssetType: Background
	}

	var held []Delivery
	take := func() { rig.manager.TakeDeliveries(func(d Delivery) { held = append(held, d) }) }
	waitWithin(t, "the first background decodes", claimTestWait, func() bool { take(); return len(held) >= claimBigShare })
	time.Sleep(claimTestQuiet)
	take()
	if len(held) != claimBigShare {
		t.Fatalf("%d background decodes delivered while none were uploaded, want %d", len(held), claimBigShare)
	}

	rig.manager.Media().Get(ChainKey(bases[claimBigAsks], AssetTypeBackground), media.Now, 0) // AssetType: Background
	waitWithin(t, "the current message's decode", claimTestWait, func() bool { take(); return len(held) > claimBigShare })

	for _, d := range held {
		d.Asset.Release()
		rig.manager.Done(d)
	}
	done := len(held)
	held = nil
	waitWithin(t, "the rest after uploads", claimTestWait, func() bool {
		rig.manager.TakeDeliveries(func(d Delivery) {
			d.Asset.Release()
			rig.manager.Done(d)
			done++
		})
		return done == claimBigAsks+1
	})
}
