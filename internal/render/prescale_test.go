package render

import (
	"image"
	"image/color"
	"testing"
	"time"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// prescaleWinW and prescaleWinH are newHeadlessRenderer's window.
	prescaleWinW = 640
	prescaleWinH = 480
	// prescaleTolerance is the per-channel difference two draws of the same pixel may have.
	prescaleTolerance = 8
	// prescaleMaxOffPct is the share of pixels allowed past it, for a one-pixel rounding at an edge.
	prescaleMaxOffPct = 1
	percent           = 100
	argbBytes         = 4
)

// halvesDecoded is a w×h frame with its left half left and its right half right, so a mirror shows.
func halvesDecoded(w, h int, left, right color.RGBA) *assets.Decoded {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := left
			if x >= w/2 {
				c = right
			}
			img.SetRGBA(x, y, c)
		}
	}
	return &assets.Decoded{Width: w, Height: h, Frames: []*image.RGBA{img}, Delays: []time.Duration{0},
		Opaque: left.A == channelMax && right.A == channelMax}
}

// readWindow returns the window's pixels.
func readWindow(t *testing.T, ren *sdl.Renderer) []byte {
	t.Helper()
	pix := make([]byte, prescaleWinW*prescaleWinH*argbBytes)
	if err := ren.ReadPixels(&sdl.Rect{W: prescaleWinW, H: prescaleWinH}, uint32(sdl.PIXELFORMAT_ARGB8888),
		unsafe.Pointer(&pix[0]), prescaleWinW*argbBytes); err != nil {
		t.Skipf("ReadPixels: %v", err)
	}
	return pix
}

// renderOnce clears the window and draws scene through v at the render scale.
func renderOnce(t *testing.T, ren *sdl.Renderer, v *Viewport, scene *courtroom.Scene, vp sdl.Rect, scale float32) []byte {
	t.Helper()
	_ = ren.SetScale(1, 1)
	_ = ren.SetDrawColor(0, 0, 0, channelMax)
	_ = ren.Clear()
	_ = ren.SetScale(scale, scale)
	v.Update(scene, 0)
	v.Render(ren, scene, vp)
	_ = ren.SetScale(1, 1)
	return readWindow(t, ren)
}

// offPct is the share of pixels whose channels differ by more than prescaleTolerance.
func offPct(a, b []byte) int {
	off := 0
	for i := 0; i < len(a); i += argbBytes {
		for c := 0; c < argbBytes; c++ {
			d := int(a[i+c]) - int(b[i+c])
			if d > prescaleTolerance || d < -prescaleTolerance {
				off++
				break
			}
		}
	}
	return off * percent / (len(a) / argbBytes)
}

// TestPrescaledDrawMatchesDirectDraw pins the software renderer's pre-scaled frames to what a direct scaled copy
// draws, through a run of changes that must each rebuild the cached frame: a mirror, a render scale, a smaller
// stage, another sprite, a dimmed pair.
func TestPrescaledDrawMatchesDirectDraw(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 0)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	if !store.software {
		t.Skip("not SDL's software renderer")
	}
	red, green := color.RGBA{R: channelMax, A: channelMax}, color.RGBA{G: channelMax, A: channelMax}
	blue, clear := color.RGBA{B: channelMax, A: channelMax}, color.RGBA{}
	const spriteW, spriteH, bgW, bgH = 48, 64, 64, 48
	for base, d := range map[string]*assets.Decoded{
		"bg":    halvesDecoded(bgW, bgH, blue, green),
		"spk":   halvesDecoded(spriteW, spriteH, red, clear),
		"other": halvesDecoded(spriteW, spriteH, clear, green),
		"pair":  halvesDecoded(spriteW, spriteH, green, red),
	} {
		if err := store.Upload(base, d); err != nil {
			t.Fatalf("upload %s: %v", base, err)
		}
	}
	cached := NewViewport(store)
	if !cached.prescale {
		t.Fatal("a viewport on SDL's software renderer did not pre-scale")
	}
	full := sdl.Rect{X: 0, Y: 0, W: prescaleWinW, H: prescaleWinH}
	small := sdl.Rect{X: 40, Y: 30, W: 400, H: 300}
	scene := &courtroom.Scene{BackgroundBase: "bg",
		Speaker: courtroom.SpriteLayer{Active: "spk", Visible: true}}
	steps := []struct {
		name  string
		edit  func()
		vp    sdl.Rect
		scale float32
	}{
		{"first draw", func() {}, full, 1},
		{"mirrored", func() { scene.Speaker.Flip = true }, full, 1},
		{"render scale", func() {}, small, 1.5},
		{"smaller stage", func() {}, small, 1},
		{"another sprite", func() { scene.Speaker.Active = "other" }, small, 1},
		{"dimmed pair", func() {
			scene.PairActive = true
			scene.Pair = courtroom.SpriteLayer{Active: "pair", Visible: true, OffsetX: 20}
			cached.SetSpriteFX(SpriteFX{Spotlight: true, SpotlightLevel: 1})
		}, full, 1},
	}
	for _, st := range steps {
		st.edit()
		direct := &Viewport{store: store, fx: cached.fx}
		want := renderOnce(t, ren, direct, scene, st.vp, st.scale)
		got := renderOnce(t, ren, cached, scene, st.vp, st.scale)
		if pct := offPct(got, want); pct > prescaleMaxOffPct {
			t.Errorf("%s: %d%% of pixels differ from a direct draw", st.name, pct)
		}
	}
}

// TestCoversStage pins when the stage fill under the background may be skipped: only for a resident, opaque
// background that reaches every edge, and never while a shake moves it.
func TestCoversStage(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 0)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	solid, seeThrough := color.RGBA{B: channelMax, A: channelMax}, color.RGBA{B: channelMax, A: channelMax / 2}
	const wide, tall, narrow = 64, 48, 24
	for base, d := range map[string]*assets.Decoded{
		"opaque":      halvesDecoded(wide, tall, solid, solid),
		"translucent": halvesDecoded(wide, tall, solid, seeThrough),
		"narrow":      halvesDecoded(narrow, tall, solid, solid),
	} {
		if err := store.Upload(base, d); err != nil {
			t.Fatalf("upload %s: %v", base, err)
		}
	}
	vp := sdl.Rect{X: 0, Y: 0, W: prescaleWinW, H: prescaleWinH}
	cases := []struct {
		name  string
		scene courtroom.Scene
		want  bool
	}{
		{"opaque and filling", courtroom.Scene{BackgroundBase: "opaque"}, true},
		{"translucent", courtroom.Scene{BackgroundBase: "translucent"}, false},
		{"narrower than the stage", courtroom.Scene{BackgroundBase: "narrow"}, false},
		{"narrow but stretched", courtroom.Scene{BackgroundBase: "narrow", SceneryStretch: true}, true},
		{"shaking", courtroom.Scene{BackgroundBase: "opaque", ShakeLeft: time.Second}, false},
		{"not loaded", courtroom.Scene{BackgroundBase: "missing"}, false},
		{"no background", courtroom.Scene{}, false},
	}
	for _, tc := range cases {
		v := NewViewport(store)
		v.Update(&tc.scene, 0)
		if got := v.CoversStage(&tc.scene, vp); got != tc.want {
			t.Errorf("%s: CoversStage = %v, want %v", tc.name, got, tc.want)
		}
	}
}
