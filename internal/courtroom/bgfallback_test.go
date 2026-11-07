package courtroom

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// bgRig is newCourtroomRig with a mount folder the test controls, so a
// background can be present or absent by construction.
type bgRig struct {
	room  *Courtroom
	sess  *Session
	mgr   *assets.Manager
	mount string
}

func newBGRig(t *testing.T) *bgRig {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)

	mount := t.TempDir()
	local := assets.NewLocalFetcher([]string{mount})
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: assets.NewResolver(prefs), Prefs: prefs, T2: t2, Disk: disk,
		Source: local, LocalMode: true, Pool: pool, Decoder: decoder,
	})
	sess := NewSession(func(protocol.Packet) error { return nil }, "test-hdid")
	sess.Background = "court"
	room := NewCourtroom(NewURLBuilder(local.BaseURL()), mgr, sess, nil)
	return &bgRig{room: room, sess: sess, mgr: mgr, mount: mount}
}

// seedBG writes a real PNG payload at background/<bg>/<stem>.webp. The .webp
// spelling is what the zero-fallback Background format order probes; the decoder
// sniffs magic bytes, never the extension (assets/sniff.go), so PNG content is
// decoded correctly and the test needs no webp encoder.
func (r *bgRig) seedBG(t *testing.T, bg, stem string) {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.mount, "background", bg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stem+".webp"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// awaitDelivery waits for one decoded asset whose Base is want, returning the
// candidate URL that actually answered.
func awaitDelivery(t *testing.T, mgr *assets.Manager, want string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case d := <-mgr.Decoded():
			if d.Base == want {
				return d.URL
			}
		case w := <-mgr.Warnings():
			if w.Base == want {
				t.Fatalf("the background chain reported %q missing instead of falling back (tried %v)", w.Base, w.Tried)
			}
		case <-deadline:
			t.Fatalf("no delivery for %q", want)
		}
	}
}

// TestMissingPositionBackgroundFallsBackToTheWitnessView is the field report:
// a custom position ("night") that exists in one area's background and not in
// the next left the PREVIOUS room's background on screen, because the sticky
// scenery gate waits for the new base to become resident and a 404'd base never
// does.
//
// AO2 never reaches that state — get_pos_path only overrides the default
// witness view when the position's own file exists
// (../AO2-Client/src/path_functions.cpp:108-159), and set_scene repeats the wit
// arm once more before hiding the layer (../AO2-Client/src/courtroom.cpp:
// 4613-4626). A streaming client cannot stat, so the ladder is a candidate
// chain: the witness art answers under the POSITION's own base, which is the
// key Scene.BackgroundBase (and therefore the viewport) reads.
func TestMissingPositionBackgroundFallsBackToTheWitnessView(t *testing.T) {
	r := newBGRig(t)
	r.seedBG(t, "court", defaultPosBackground) // the area ships only the witness view

	r.room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "night", Message: "hi",
		EmoteMod: protocol.EmoteModIdle,
	}})

	want := r.room.urls.Background("court", "night")
	if r.room.Scene.BackgroundBase != want {
		t.Fatalf("Scene.BackgroundBase = %q, want the position's own base %q", r.room.Scene.BackgroundBase, want)
	}
	got := awaitDelivery(t, r.mgr, want)
	if wantURL := want[:len(want)-len("night")] + defaultPosBackground + ".webp"; got != wantURL {
		t.Fatalf("the fallback served %q, want %q", got, wantURL)
	}
}

// TestAreaChangeRevalidatesThePositionBackground drives the exact live sequence:
// the room is staged at a custom position, then a BN moves it to a background
// that has no art for that position. The new area's witness view must answer
// under the position's base — nothing may be left waiting on a base that will
// never resolve.
func TestAreaChangeRevalidatesThePositionBackground(t *testing.T) {
	r := newBGRig(t)
	r.seedBG(t, "old", "night")              // the area we start in HAS the custom pos
	r.seedBG(t, "new", defaultPosBackground) // the area we move to does not
	r.room.Scene.Position = "night"

	r.room.HandleEvent(Event{Kind: EventBackground, Text: "old"})
	if got := awaitDelivery(t, r.mgr, r.room.urls.Background("old", "night")); got == "" {
		t.Fatal("the starting area's own art must resolve")
	}

	r.room.HandleEvent(Event{Kind: EventBackground, Text: "new"})
	want := r.room.urls.Background("new", "night")
	if r.room.Scene.BackgroundBase != want {
		t.Fatalf("after the area change Scene.BackgroundBase = %q, want %q", r.room.Scene.BackgroundBase, want)
	}
	got := awaitDelivery(t, r.mgr, want)
	if wantURL := want[:len(want)-len("night")] + defaultPosBackground + ".webp"; got != wantURL {
		t.Fatalf("the new area's scenery served %q, want the witness fallback %q", got, wantURL)
	}
}

// TestLegacyWitBackgroundAnswersTheWitnessPosition covers the second rung, and
// is the reason the ladder has two: get_pos_path's default pair is
// witnessempty/stand only when witnessempty exists, and wit/wit_overlay
// otherwise (../AO2-Client/src/path_functions.cpp:108-117). A background pack
// that ships only wit.* used to draw nothing at the witness position.
func TestLegacyWitBackgroundAnswersTheWitnessPosition(t *testing.T) {
	r := newBGRig(t)
	r.seedBG(t, "court", legacyPosBackground) // only wit.*, no witnessempty.*

	r.room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "wit", Message: "hi",
		EmoteMod: protocol.EmoteModIdle,
	}})

	want := r.room.urls.PositionScenery("court", "wit", nil).Background
	got := awaitDelivery(t, r.mgr, want)
	if wantURL := r.room.urls.Background("court", legacyPosBackground) + ".webp"; got != wantURL {
		t.Fatalf("the witness position served %q, want the legacy %q", got, wantURL)
	}
}

func TestPositionSceneryAsksAO2sNamesInOrderOnce(t *testing.T) {
	urls := NewURLBuilder("https://cdn/base/")
	for pos, want := range map[string][]string{
		"def":   {"def", "defenseempty", defaultPosBackground, legacyPosBackground},
		"wit":   {"wit", defaultPosBackground},
		"night": {"night", defaultPosBackground, legacyPosBackground},
		"":      {defaultPosBackground, legacyPosBackground},
	} {
		s := urls.PositionScenery("court", pos, nil)
		got := append([]string{s.Background}, s.Alts...)
		if len(got) != len(want) {
			t.Fatalf("%q: names %q, want %q", pos, got, want)
		}
		for i, name := range want {
			if got[i] != urls.Background("court", name) {
				t.Fatalf("%q: name %d is %q, want %q", pos, i, got[i], name)
			}
		}
	}
}

func TestSceneAssetsCarriesEveryNameAndEachNamesDesk(t *testing.T) {
	urls := NewURLBuilder("https://cdn/base/")
	const pos = "night"
	refs := SceneAssets(urls, "court", []Event{{
		Kind:    EventMessage,
		Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: pos},
	}})
	s := urls.PositionScenery("court", pos, nil)
	desks := map[string]bool{}
	for _, r := range refs {
		switch r.Type {
		case assets.AssetTypeBackground:
			if r.Base != s.Background || len(r.Alts) != len(s.Alts) {
				t.Fatalf("background ref %q with %q, want %q with %q", r.Base, r.Alts, s.Background, s.Alts)
			}
		case assets.AssetTypeDeskOverlay:
			if len(r.Alts) != 0 {
				t.Fatalf("desk ref %+v must not fall back", r)
			}
			desks[r.Base] = true
		}
	}
	for _, desk := range []string{"night_overlay", "stand", "wit_overlay"} {
		if !desks[urls.Background("court", desk)] {
			t.Errorf("no desk ref for %s in %v", desk, desks)
		}
	}
}

// TestTheDeskKeepsItsNoFallbackContract pins the deliberate asymmetry. AO2's
// set_scene desk arm has NO fallback — it hides the layer
// (../AO2-Client/src/courtroom.cpp:4628-4634) — because an author suppresses a
// desk by not shipping the file, and 2.8 unique-position backgrounds ship <pos>
// with no <pos>_overlay for exactly that reason (#44). If the background ladder
// is ever "symmetrised" onto the desk, a custom position starts drawing the
// witness stand under it.
func TestTheDeskKeepsItsNoFallbackContract(t *testing.T) {
	r := newBGRig(t)
	r.seedBG(t, "court", "night") // the position's background exists
	r.seedBG(t, "court", "stand") // the DEFAULT desk exists — and must not be borrowed
	r.seedBG(t, "court", defaultPosBackground)

	night := r.room.urls.Background("court", "night")
	r.room.Assets = assetFake{files: map[string]string{night: night + config.ExtPNG}}
	r.room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "night", Message: "hi",
		EmoteMod: protocol.EmoteModIdle, DeskMod: 1,
	}})

	deskBase := r.room.urls.Background("court", "night_overlay")
	if r.room.Scene.DeskBase != deskBase {
		t.Fatalf("Scene.DeskBase = %q, want %q", r.room.Scene.DeskBase, deskBase)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case d := <-r.mgr.Decoded():
			if d.Base == deskBase {
				t.Fatalf("the desk must NOT fall back; it was served %q", d.URL)
			}
		case w := <-r.mgr.Warnings():
			if w.Base == deskBase {
				return // conclusively missing → DeskDrawn hides the layer. Correct.
			}
		case <-deadline:
			t.Fatal("the desk never resolved either way")
		}
	}
}
