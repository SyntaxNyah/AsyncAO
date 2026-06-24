package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestParticleWeatherZeroAlloc pins #124: an active weather overlay renders with zero per-frame
// heap allocations — the pool is fixed, the dot texture cached, the blit destination a scratch
// rect, and the respawn PRNG pure. Covers snow (uniform alpha) AND embers (per-particle fade +
// additive blend), the two draw paths. Off → an early return (byte-identical).
func TestParticleWeatherZeroAlloc(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	for _, base := range []string{"bg", "desk", "spk", "pair"} {
		if err := store.Upload(base, decodedFixture()); err != nil {
			t.Fatal(err)
		}
	}
	vp := NewViewport(store)
	defer vp.PurgePostFX() // frees the cached weather dot texture
	scene := benchScene(store)
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	for _, w := range []Weather{WeatherSnow, WeatherEmbers} {
		vp.SetWeather(w, 100)
		allocs := testing.AllocsPerRun(200, func() {
			vp.Update(scene, 16*time.Millisecond)
			vp.Render(ren, scene, rect)
		})
		if allocs != 0 {
			t.Errorf("weather %d render allocates %.1f objects/op, want 0 (#124 zero-perf constraint)", w, allocs)
		}
	}
}

// TestParticleSet pins the count scaling + the weather guard: intensity scales the active pool,
// an out-of-range weather falls back to None, and switching weather re-seeds.
func TestParticleSet(t *testing.T) {
	var f particleField
	f.set(WeatherSnow, 50)
	if f.weather != WeatherSnow || f.n != maxParticles/2 {
		t.Fatalf("snow@50 → weather %d n %d, want snow %d", f.weather, f.n, maxParticles/2)
	}
	if f.seededFor != WeatherSnow {
		t.Error("switching to snow didn't seed the pool")
	}
	f.set(WeatherCount, 100) // out of range → None
	if f.weather != WeatherNone {
		t.Errorf("out-of-range weather → %d, want None", f.weather)
	}
}

// TestParticleUpdateWraps pins that particles stay bounded — a big time step advances them past
// the edge and they respawn rather than flying off to infinity.
func TestParticleUpdateWraps(t *testing.T) {
	var f particleField
	f.set(WeatherSnow, 100)
	for i := 0; i < 5; i++ {
		f.update(2 * time.Second) // each step pushes y well past the bottom → respawn
	}
	for i := 0; i < f.n; i++ {
		if y := f.parts[i].y; y < -0.5 || y > 1.5 {
			t.Fatalf("particle %d escaped to y=%.2f (respawn wrap failed)", i, y)
		}
	}
}
