package ui

// contentseed_test.go — H1 field-report fix: an imported .demo's recorded origin
// is never the session origin, so its host is never format-seeded, and the
// zero-fallback probe walks the single default format against a server whose
// sprites are e.g. .gif/.png → "everything missing" + an empty exported stage.
//
// These tests pin the two-part fix:
//   1. ORIGIN SEEDING: the content report seeds <origin>/extensions.json before
//      probing, so a manifest-declared format resolves in one probe.
//   2. FULL-CHAIN PROBE FALLBACK: a host with NO manifest (no learned entry even
//      after seeding) is probed with the FULL configured chain, so a .png/.gif
//      server still resolves — and RecordSuccess learns it so a repeat pass is
//      single-probe. A genuinely absent asset stays missing after the full walk
//      (honesty preserved).
//   4. LOCAL MOUNTS: a recording whose origin is a mounted local base resolves
//      through the mounts in the probe path (AO2-parity).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// findCharStatus returns the probe status of the first character item whose Name
// contains sub (case-insensitive), plus whether such an item exists.
func findCharStatus(rep *ContentReport, sub string) (AssetStatus, bool) {
	for _, it := range rep.Categories[CatCharacter].Items {
		if strings.Contains(strings.ToLower(it.Name), strings.ToLower(sub)) {
			return it.Status, true
		}
	}
	return StatusUnknown, false
}

// TestContentSeedGifManifestResolves pins part 1: an origin that publishes an
// extensions.json declaring .gif for sprites, and serves the sprite ONLY as
// .gif, is FOUND — because the content job seeds the manifest before probing and
// the seeded host is single-probed at the declared format. Without seeding the
// probe would walk the zero-fallback [.webp] and report missing.
func TestContentSeedGifManifestResolves(t *testing.T) {
	sprite := pngBytes() // opaque bytes; the probe never decodes them
	manifest := `{"emote_extensions": [".gif"], "background_extensions": [".gif"]}`
	var gifHits atomic.Int32 // written in the server goroutine, read in the test → atomic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+assets.ManifestFileName:
			_, _ = w.Write([]byte(manifest))
		case r.URL.Path == "/characters/phoenix/(a)normal.gif":
			gifHits.Add(1)
			_, _ = w.Write(sprite)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{
		Origin:  srv.URL + "/",
		StartBg: "courtroom",
		Events:  []recEvent{msgEvent("Phoenix", "normal", "", "", "")},
	}
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("StartContentReport refused")
	}
	drainContentJob(t, a)

	rep := a.ContentJobReport()
	if rep == nil {
		t.Fatal("no report after probe")
	}
	if rep.Seed != seedApplied {
		t.Errorf("seed status = %v, want seedApplied (the manifest was served)", rep.Seed)
	}
	st, ok := findCharStatus(rep, "phoenix")
	if !ok {
		t.Fatalf("phoenix not enumerated; items: %v", itemStatuses(rep, CatCharacter))
	}
	if st != StatusFound {
		t.Errorf("phoenix (.gif via manifest) status = %v, want found", st)
	}
	if gifHits.Load() == 0 {
		t.Error("the .gif candidate was never probed — the manifest wasn't seeded before probing")
	}
}

// TestContentFullChainProbeFindsPNG pins part 2: an origin with NO manifest whose
// sprite exists only as .png is FOUND via the full-chain walk (the seed comes
// back absent, so the unlearned host is probed across the full configured chain,
// which includes .png). RecordSuccess must learn .png, so a second ResolveRaw is
// a single probe. A genuinely absent asset stays missing after the whole walk.
func TestContentFullChainProbeFindsPNG(t *testing.T) {
	sprite := pngBytes()
	// probes is written from the httptest server goroutine and read from the test
	// goroutine, so it needs its own lock (race-clean under -race).
	var (
		probesMu sync.Mutex
		probes   = map[string]int{}
	)
	probeCount := func(path string) int {
		probesMu.Lock()
		defer probesMu.Unlock()
		return probes[path]
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probesMu.Lock()
		probes[r.URL.Path]++
		probesMu.Unlock()
		if r.URL.Path == "/characters/phoenix/(a)normal.png" {
			_, _ = w.Write(sprite)
			return
		}
		// No extensions.json (404) and no other format — genuinely absent otherwise.
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{
		Origin:  srv.URL + "/",
		StartBg: "courtroom",
		Events: []recEvent{
			msgEvent("Phoenix", "normal", "", "", ""), // exists only as .png
			msgEvent("Ghost", "normal", "", "", ""),   // absent in every format
		},
	}
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("StartContentReport refused")
	}
	drainContentJob(t, a)

	rep := a.ContentJobReport()
	if rep == nil {
		t.Fatal("no report after probe")
	}
	if rep.Seed != seedAbsent {
		t.Errorf("seed status = %v, want seedAbsent (no manifest served)", rep.Seed)
	}
	if st, _ := findCharStatus(rep, "phoenix"); st != StatusFound {
		t.Errorf("phoenix (.png, no manifest) status = %v, want found via full-chain walk", st)
	}
	if st, _ := findCharStatus(rep, "ghost"); st != StatusMissing {
		t.Errorf("ghost (genuinely absent) status = %v, want missing after the full walk (honesty)", st)
	}

	// The winner must have been learned so a repeat resolve is single-probe.
	host := hostOfURL(rec.Origin)
	if ext, ok := a.d.Resolver.Learned(host, assets.AssetTypeCharSprite); !ok || ext != config.ExtPNG {
		t.Errorf("Learned(CharSprite) = %q,%v, want %q,true (RecordSuccess should have learned the .png winner)", ext, ok, config.ExtPNG)
	}

	// No NON-png candidate for phoenix's base may EVER have been probed on the
	// wire: .webp/.avif/.apng each 404 and .gif 404s, but the point is the walk
	// stopped at .png (found) — since .png is LAST in the CharSprite chain, every
	// earlier candidate was necessarily probed once, which is expected for the
	// diagnostic walk. What must NOT happen is a probe of a candidate AFTER .png or
	// a re-walk. Assert instead on the learned-first repeat below.

	// A second ResolveRaw (learned-first now) is exactly one probe of .png — served
	// from T2 (already fetched) so the wire count does NOT grow.
	base := rec.Origin + "characters/phoenix/(a)normal"
	before := probeCount("/characters/phoenix/(a)normal.png")
	if _, _, ok := a.d.Manager.ResolveRaw(base, assets.AssetTypeCharSprite); !ok {
		t.Fatal("learned-first ResolveRaw should still find the .png sprite")
	}
	if got := probeCount("/characters/phoenix/(a)normal.png"); got != before {
		t.Errorf(".png probed %d times on the learned-first repeat, want %d (a learned hit re-drains from T2, no new probe)", got, before)
	}
}

// TestContentMixedFormatNoManifestBothFound pins the mixed-format honesty case:
// a no-manifest origin (seedAbsent) whose TWO characters carry DIFFERENT per-asset
// formats — Alpha only .gif, Beta only .png — must report BOTH Found. Both sprites
// share one (host, CharSprite) learned slot, and the 8 probe workers run
// concurrently, so whichever sprite RecordSuccess-learns its format first would,
// under a "learned miss is terminal" policy, lock the OTHER sprite out: it would be
// single-probed at the sibling's format, 404, and be falsely reported Missing —
// nondeterministically by worker scheduling. The fix makes a learned MISS fall
// through to the full-chain walk on the diagnostic path, so each sprite is found at
// its own format regardless of probe order. Without the fix this flakes/fails by
// worker order; with it, both are deterministically Found.
func TestContentMixedFormatNoManifestBothFound(t *testing.T) {
	sprite := pngBytes() // opaque bytes; the probe never decodes them
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/characters/alpha/(a)normal.gif": // Alpha exists ONLY as .gif
			_, _ = w.Write(sprite)
		case "/characters/beta/(a)normal.png": // Beta exists ONLY as .png
			_, _ = w.Write(sprite)
		default:
			// No extensions.json (404) and no other format for either char.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{
		Origin:  srv.URL + "/",
		StartBg: "courtroom",
		Events: []recEvent{
			msgEvent("Alpha", "normal", "", "", ""), // only .gif
			msgEvent("Beta", "normal", "", "", ""),  // only .png
		},
	}
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("StartContentReport refused")
	}
	drainContentJob(t, a)

	rep := a.ContentJobReport()
	if rep == nil {
		t.Fatal("no report after probe")
	}
	if rep.Seed != seedAbsent {
		t.Errorf("seed status = %v, want seedAbsent (no manifest served)", rep.Seed)
	}
	if st, ok := findCharStatus(rep, "alpha"); !ok || st != StatusFound {
		t.Errorf("alpha (.gif, no manifest) status = %v ok=%v, want found — a sibling's learned .png must not lock it out; items: %v",
			st, ok, itemStatuses(rep, CatCharacter))
	}
	if st, ok := findCharStatus(rep, "beta"); !ok || st != StatusFound {
		t.Errorf("beta (.png, no manifest) status = %v ok=%v, want found — a sibling's learned .gif must not lock it out; items: %v",
			st, ok, itemStatuses(rep, CatCharacter))
	}
}

// TestContentSeedLocalMountResolves pins part 4: a recording whose origin is a
// mounted local base (local:// scheme, NOT http) skips seeding (there is no
// extensions.json to fetch) and resolves the sprite through the mounts via the
// full-chain probe — AO2 resolving a demo against local content folders.
func TestContentSeedLocalMountResolves(t *testing.T) {
	mount := t.TempDir()
	// The sprite exists on-disk only as .png under the mount — the zero-fallback
	// default is [.webp], so only the full-chain walk finds it.
	seedMount(t, mount, "characters/phoenix/(a)normal.png", pngBytes())

	mgr, origin := localMgr(t, mount)
	if !strings.HasPrefix(origin, assets.LocalScheme) {
		t.Fatalf("mount origin %q is not a local:// origin", origin)
	}

	// probeRef directly (the worker's unit) over the real enumeration ref: a
	// mounted .png sprite must resolve through the full-chain walk against the
	// local mounts (the seed path correctly skips a non-http origin).
	ref := charSpriteRef(t, origin, "Phoenix", "normal")
	url, status := probeRef(mgr, ref)
	if status != StatusFound {
		t.Fatalf("mounted .png sprite status = %v, want found (probe must honor local mounts)", status)
	}
	if !strings.HasSuffix(url, ".png") {
		t.Errorf("resolved URL = %q, want the .png the mount actually holds", url)
	}
}

// TestExportSeedBeforeWarm pins part 1b: the export's format-seed phase runs
// BEFORE the warm and publishes the manifest into the resolver, so the warm's
// first candidate for a sprite is the manifest-declared format (here .gif), not
// the zero-fallback default (.webp). Asserts the seed's OBSERVABLE — the learned
// table — rather than T1 residency (a headless app runs no upload Pump, so a
// submitted warm ref never becomes resident; the learned format is what proves
// "seeded before the warm builds candidates").
func TestExportSeedBeforeWarm(t *testing.T) {
	manifest := `{"emote_extensions": [".gif"], "background_extensions": [".gif"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+assets.ManifestFileName {
			_, _ = w.Write([]byte(manifest))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	a.d.Prefs.SetFormatAutoDetect(true) // the seed gate respects the same pref the session path does
	origin := srv.URL + "/"
	host := hostOfURL(origin)

	// Pre-seed sanity: nothing learned yet, so the warm's first candidate WOULD be
	// the zero-fallback .webp (the bug).
	if ext, ok := a.d.Resolver.Learned(host, assets.AssetTypeCharSprite); ok {
		t.Fatalf("CharSprite already learned (%q) before seeding — test can't prove the seed did it", ext)
	}

	// Build a seeding-phase export job and fire the seed, exactly as startSceneExport
	// does after the load lands (no viewport / capture target needed — seeding runs
	// ahead of both).
	a.gif = &gifExportJob{}
	a.gifExporting = true
	a.startExportSeed(a.gif, origin)
	if !a.gif.seeding {
		t.Fatal("startExportSeed did not enter the seeding phase")
	}

	// Drive the render-thread tick until the seed lands and the phase clears.
	for i := 0; i < 2000 && a.gif != nil && a.gif.seeding; i++ {
		a.tickGifExport()
		time.Sleep(time.Millisecond)
	}
	if a.gif == nil {
		t.Fatal("export job vanished during seeding")
	}
	if a.gif.seeding {
		t.Fatal("seeding phase never completed")
	}

	// The observable: the manifest's .gif is now the learned CharSprite format, so
	// the warm's first PrefetchChain candidate is base+".gif" — the manifest-
	// declared format, not the .webp default.
	if ext, ok := a.d.Resolver.Learned(host, assets.AssetTypeCharSprite); !ok || ext != config.ExtGIF {
		t.Errorf("Learned(CharSprite) = %q,%v after export seed, want %q,true (warm would probe the wrong format otherwise)", ext, ok, config.ExtGIF)
	}
}

// charSpriteRef returns the enumeration ref probeRef would see for char/emote,
// built by the SAME enumerateContent the report uses so the test exercises a real
// ref (base + bare-name alt spellings included).
func charSpriteRef(t *testing.T, origin, char, emote string) courtroom.AssetRef {
	t.Helper()
	rec := &sceneRecording{
		Origin:  origin,
		StartBg: "courtroom",
		Events:  []recEvent{msgEvent(char, emote, "", "", "")},
	}
	r := enumerateContent(rec.Origin, rec.StartBg, recEvents(rec))
	for _, it := range r.Categories[CatCharacter].Items {
		return it.ref
	}
	t.Fatalf("no character sprite ref enumerated for %s/%s", char, emote)
	return courtroom.AssetRef{}
}
