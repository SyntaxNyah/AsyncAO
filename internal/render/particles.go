package render

import (
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Particle weather: a local-only overlay from a fixed pool and one cached dot, with no per-frame allocation.

type Weather uint8

const (
	WeatherNone Weather = iota
	WeatherSnow
	WeatherRain
	WeatherSakura
	WeatherEmbers
	WeatherCount // number of weathers, for the UI cycle
)

const (
	// maxParticles caps the pool; the intensity slider scales the active count down from it.
	maxParticles = 240
	// weatherIntensityMax is the top of the 1..100 weather intensity setting.
	weatherIntensityMax = 100
	// xorshift32 shift triple (Marsaglia 2003).
	xorshiftA = 13
	xorshiftB = 17
	xorshiftC = 5
	// particleBaseDivisor sets the base dot size as vp.H / divisor; particleDotTex is the texture resolution.
	particleBaseDivisor = 72
	particleDotTex      = 16
)

// weatherParams tunes one weather; velocities and sway are viewport fractions, so it is resolution-independent.
type weatherParams struct {
	r, g, b   uint8
	vy        float32 // base vertical velocity (fraction/sec); negative = rises (embers)
	vyJitter  float32
	swayAmp   float32 // horizontal sway amplitude (fraction)
	swaySpeed float32 // sway phase advance (rad/sec)
	sizeMin   float32 // dot size factor range (× the base px size)
	sizeMax   float32
	stretchY  float32 // vertical stretch (rain streaks)
	alpha     uint8
	additive  bool // glow (embers)
	fade      bool // dim as it rises (embers)
}

// weatherTable indexes by Weather (WeatherNone is a zero entry, never drawn).
//
//nolint:mnd // a named data table (rule 9)
var weatherTable = [WeatherCount]weatherParams{
	WeatherNone:   {},
	WeatherSnow:   {r: 255, g: 255, b: 255, vy: 0.10, vyJitter: 0.04, swayAmp: 0.025, swaySpeed: 1.2, sizeMin: 0.5, sizeMax: 1.2, stretchY: 1, alpha: 210},
	WeatherRain:   {r: 170, g: 195, b: 255, vy: 0.85, vyJitter: 0.15, swayAmp: 0.004, swaySpeed: 0.5, sizeMin: 0.3, sizeMax: 0.5, stretchY: 5, alpha: 120},
	WeatherSakura: {r: 255, g: 170, b: 195, vy: 0.12, vyJitter: 0.04, swayAmp: 0.05, swaySpeed: 1.6, sizeMin: 0.7, sizeMax: 1.4, stretchY: 1, alpha: 220},
	WeatherEmbers: {r: 255, g: 140, b: 45, vy: -0.16, vyJitter: 0.06, swayAmp: 0.015, swaySpeed: 1.0, sizeMin: 0.4, sizeMax: 1.0, stretchY: 1, alpha: 210, additive: true, fade: true},
}

// particle is one drop or flake, positioned in viewport fractions.
type particle struct {
	x, y  float32
	vy    float32 // this particle's vertical velocity (base ± jitter)
	phase float32 // sway phase accumulator
	size  float32 // size factor in [sizeMin,sizeMax]
}

// particleField is the pool and its cached dot texture.
type particleField struct {
	weather   Weather
	n         int // active particle count (intensity-scaled)
	parts     [maxParticles]particle
	tex       *sdl.Texture
	rng       uint32  // xorshift state (never 0)
	seededFor Weather // re-seed the pool when the weather changes (different motion)
	dst       sdl.Rect
	clipSave  sdl.Rect
}

func WeatherName(w Weather) string {
	switch w {
	case WeatherSnow:
		return "Snow"
	case WeatherRain:
		return "Rain"
	case WeatherSakura:
		return "Sakura"
	case WeatherEmbers:
		return "Embers"
	default:
		return "None"
	}
}

// SetWeather sets the weather and intensity each frame; it re-seeds only when the weather changed.
func (v *Viewport) SetWeather(w Weather, intensity int) { v.particles.set(w, intensity) }

func (f *particleField) set(w Weather, intensity int) {
	if w >= WeatherCount {
		w = WeatherNone
	}
	f.weather = w
	if w == WeatherNone {
		return
	}
	intensity = value.Clamp(intensity, 1, weatherIntensityMax)
	f.n = maxParticles * intensity / weatherIntensityMax
	if w != f.seededFor {
		f.reseed(w)
		f.seededFor = w
	}
}

// randf returns a deterministic float in [0,1) via xorshift32.
func (f *particleField) randf() float32 {
	if f.rng == 0 {
		f.rng = 0x9e3779b9
	}
	f.rng ^= f.rng << xorshiftA
	f.rng ^= f.rng >> xorshiftB
	f.rng ^= f.rng << xorshiftC
	return float32(f.rng) / float32(^uint32(0))
}

// reseed scatters every particle, once per weather change.
func (f *particleField) reseed(w Weather) {
	p := weatherTable[w]
	for i := range f.parts {
		f.parts[i] = particle{
			x:     f.randf(),
			y:     f.randf(),
			vy:    p.vy + (f.randf()*2-1)*p.vyJitter,
			phase: f.randf() * 2 * math.Pi,
			size:  p.sizeMin + f.randf()*(p.sizeMax-p.sizeMin),
		}
	}
}

// update advances active particles and wraps them at the far edge; sway applies at draw, so x never permanently drifts.
func (f *particleField) update(dt time.Duration) {
	if f.weather == WeatherNone || f.n == 0 {
		return
	}
	p := weatherTable[f.weather]
	dtSec := float32(dt.Seconds())
	for i := 0; i < f.n; i++ {
		pt := &f.parts[i]
		pt.y += pt.vy * dtSec
		pt.phase += p.swaySpeed * dtSec
		switch {
		case pt.vy >= 0 && pt.y > 1.05: // fell off the bottom → respawn at the top, new column
			pt.y -= 1.1
			pt.x = f.randf()
		case pt.vy < 0 && pt.y < -0.05: // rose off the top → respawn at the bottom
			pt.y += 1.1
			pt.x = f.randf()
		}
	}
}

// draw blits active particles clipped to the stage, so edge flakes can't bleed past.
func (f *particleField) draw(ren *sdl.Renderer, vp sdl.Rect) {
	if f.weather == WeatherNone || f.n == 0 {
		return
	}
	if f.tex == nil {
		if f.tex = buildParticleDot(ren); f.tex == nil {
			return
		}
	}
	p := weatherTable[f.weather]
	needClip := !ren.IsClipEnabled()
	if needClip {
		f.clipSave = vp
		_ = ren.SetClipRect(&f.clipSave)
	}
	_ = f.tex.SetColorMod(p.r, p.g, p.b)
	if p.additive {
		_ = f.tex.SetBlendMode(sdl.BLENDMODE_ADD)
	}
	if !p.fade {
		_ = f.tex.SetAlphaMod(p.alpha)
	}
	basePx := vp.H / particleBaseDivisor
	if basePx < 1 {
		basePx = 1
	}
	for i := 0; i < f.n; i++ {
		pt := &f.parts[i]
		sway := p.swayAmp * float32(math.Sin(float64(pt.phase)))
		w := int32(pt.size * float32(basePx))
		if w < 1 {
			w = 1
		}
		h := w
		if p.stretchY > 1 {
			h = int32(float32(w) * p.stretchY)
		}
		px := vp.X + int32((pt.x+sway)*float32(vp.W))
		py := vp.Y + int32(pt.y*float32(vp.H))
		if p.fade { // embers dim as they rise (y → 0 near the top)
			_ = f.tex.SetAlphaMod(uint8(float32(p.alpha) * value.Clamp(pt.y, 0, 1)))
		}
		f.dst = sdl.Rect{X: px - w/2, Y: py - h/2, W: w, H: h}
		_ = ren.Copy(f.tex, nil, &f.dst)
	}
	_ = f.tex.SetAlphaMod(math.MaxUint8) // restore the shared cached texture
	_ = f.tex.SetColorMod(math.MaxUint8, math.MaxUint8, math.MaxUint8)
	if p.additive {
		_ = f.tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	}
	if needClip {
		_ = ren.SetClipRect(nil)
	}
}

// purge frees the dot texture; render thread only.
func (f *particleField) purge() {
	if f.tex != nil {
		_ = f.tex.Destroy()
		f.tex = nil
	}
}

// buildParticleDot makes the white soft round dot, tinted per weather at draw.
func buildParticleDot(ren *sdl.Renderer) *sdl.Texture {
	const n = particleDotTex
	pix := make([]byte, n*n*texBytesPerPixel)
	c := float64(n-1) / 2
	maxD := math.Hypot(c, c)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := math.Hypot(float64(x)-c, float64(y)-c) / maxD // 0 centre → 1 edge
			a := value.Clamp(1-d, 0, 1)
			a *= a // softer falloff
			i := (y*n + x) * texBytesPerPixel
			pix[i], pix[i+1], pix[i+2] = math.MaxUint8, math.MaxUint8, math.MaxUint8
			pix[i+3] = byte(a * math.MaxUint8)
		}
	}
	t, err := uploadPixels(ren, pix, n, n)
	if err != nil {
		return nil
	}
	return t
}
