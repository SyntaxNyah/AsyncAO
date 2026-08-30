package ui

// The UI half of the conclusive-miss brake.
//
// The manager's own suite (assets/conclusivemiss_test.go) proves the memory is
// recorded and that every Prefetch entry point consults it. These prove the two
// things only this package can answer: that the DEMAND CADENCE stops, and that a
// cell which can never fill stops holding the frame pump awake.
//
// Both halves matter and they failed together in the field: a character roster
// where most entries ship no char_icon and no emotions/button<N>_off art sent a
// 404 every couple of seconds per visible cell, indefinitely, and the client sank
// under the pool jobs and disk probes behind them.

import (
	"go/ast"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// missGateSettle bounds the wait for the pipeline to establish a miss.
	// LocalMode resolution is synchronous file I/O, so this is slack, not a
	// cadence — a fixture that needs all of it is broken, not slow.
	missGateSettle = 5 * time.Second
	// missGatePollStep spaces the settle poll. Short enough that the wait costs
	// milliseconds in the ordinary case.
	missGatePollStep = 2 * time.Millisecond
	// missGateDrawFrames bounds the draw loop in the frame-pump gate. Each pass is
	// one drawEmoteImageButton call against an empty pack; the misses land in the
	// first few, so overrunning this means the draw stopped demanding at all.
	missGateDrawFrames = 400
)

// wireEmptyPackManager installs a streaming-shaped Manager over an EMPTY local
// pack, leaving a.d.Store and a.ctx alone.
//
// Deliberately not wireLocalManager: that helper stands up its own renderer and
// replaces a.d.Store, so an App staged with a live Ctx would end up drawing
// textures created on a different renderer. Everything else matches it —
// LocalMode, so resolution is synchronous file I/O and the pipeline settles
// deterministically.
func wireEmptyPackManager(t *testing.T, a *App) *assets.LocalFetcher {
	t.Helper()
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

	local := assets.NewLocalFetcher([]string{t.TempDir()}) // empty: nothing resolves
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  assets.NewResolver(a.d.Prefs),
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    local,
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	// One extension per type keeps the probe chains short and the fixture's
	// intent legible; the defaults differ per type and none of that is what
	// these gates are about.
	for _, typ := range []assets.AssetType{assets.AssetTypeCharIcon, assets.AssetTypeEmoteButton} {
		a.d.Prefs.SetFormatOrder(typ.Name(), []string{config.ExtPNG})
	}
	return local
}

// drainManager empties the manager's result channels so the pipeline keeps
// moving while a gate polls. Results are never dropped by the manager (rule
// §17.7), so a gate that ignores them stalls the pool once the buffers fill.
func drainManager(a *App) {
	for {
		select {
		case d := <-a.d.Manager.Decoded():
			if d.Asset != nil {
				d.Asset.Release()
			}
		case <-a.d.Manager.Warnings():
		default:
			return
		}
	}
}

// settleMissUI waits until the manager has established that base is absent.
func settleMissUI(t *testing.T, a *App, base string, typ assets.AssetType) {
	t.Helper()
	deadline := time.Now().Add(missGateSettle)
	for time.Now().Before(deadline) {
		drainManager(a)
		if a.d.Manager.IsConclusiveMiss(base, typ) {
			return
		}
		time.Sleep(missGatePollStep)
	}
	t.Fatalf("the manager never established that %q is absent — the fixture is not exercising the miss path, "+
		"so everything below it would pass vacuously", base)
}

// TestDemandAssetStopsAskingForAConclusiveMiss is the cadence gate, and it is the
// user-visible half of the field report: "every 2 seconds I see a 404".
//
// demandAsset's retry loop only ever terminated on T1 RESIDENCY, and an asset the
// server does not have can never become resident, so a visible cell re-asked for
// it every charIconRetryInterval for as long as it was on screen. The budget
// decrement is the observable: one ask spends one slot, and a gated cell must
// spend none.
func TestDemandAssetStopsAskingForAConclusiveMiss(t *testing.T) {
	a := testTabApp(t)
	local := wireEmptyPackManager(t, a)
	a.installAssetOrigin(local.BaseURL())

	base := a.urls.CharIcon("gold_ship")
	var stamps []time.Time
	a.iconAskBudget = charIconAskPerFrame

	// The FIRST pass must really ask — otherwise the "stopped asking" assertion
	// below is measuring a demand that never started.
	if !a.demandAsset(&stamps, 1, 0, base, assets.AssetTypeCharIcon) {
		t.Fatal("the first demand for an unprobed base reported the art can never arrive — nothing has been probed yet")
	}
	if a.iconAskBudget != charIconAskPerFrame-1 {
		t.Fatalf("the first demand spent %d budget slots, want 1 — it never reached Prefetch, so this gate is vacuous",
			charIconAskPerFrame-a.iconAskBudget)
	}

	settleMissUI(t, a, base, assets.AssetTypeCharIcon)

	// Open the retry window and refill the budget: neither may be what stops the
	// next ask. Only the miss gate may.
	stamps[0] = time.Time{}
	a.iconAskBudget = charIconAskPerFrame
	if a.demandAsset(&stamps, 1, 0, base, assets.AssetTypeCharIcon) {
		t.Error("demandAsset reported a conclusively-absent icon as still arriving — callers use that answer to keep " +
			"the frame pump awake for art that is never coming")
	}
	if a.iconAskBudget != charIconAskPerFrame {
		t.Errorf("a conclusively-absent icon still spent %d budget slot(s) — the 2 s re-ask cadence is still running",
			charIconAskPerFrame-a.iconAskBudget)
	}
	// And it stays stopped: the field symptom was the REPEAT, so one silent pass
	// proves nothing.
	for i := 0; i < 20; i++ {
		stamps[0] = time.Time{}
		a.demandAsset(&stamps, 1, 0, base, assets.AssetTypeCharIcon)
	}
	if a.iconAskBudget != charIconAskPerFrame {
		t.Errorf("20 further passes over an absent icon spent %d budget slot(s), want 0",
			charIconAskPerFrame-a.iconAskBudget)
	}

	// The negative control: the gate keys on the BASE, so a sibling asset of the
	// same character is untouched by its neighbour's verdict. Without this, a gate
	// that simply stopped demanding everything would pass.
	sibling := a.urls.EmoteButton("gold_ship", 1, false)
	a.iconAskBudget = charIconAskPerFrame
	var siblingStamps []time.Time
	if !a.demandAsset(&siblingStamps, 1, 0, sibling, assets.AssetTypeEmoteButton) {
		t.Fatal("an unprobed sibling base was reported absent — one asset's verdict is being applied to another")
	}
	if a.iconAskBudget != charIconAskPerFrame-1 {
		t.Error("an unprobed sibling base was never asked for — the gate is suppressing demand it has no verdict on")
	}
}

// TestRetryMissingAssetsResumesTheDemand pins the Settings escape hatch end to
// end. The memory has no clock by design (assets.missSet), so if the button did
// not genuinely re-open the demand a server that added the missing files
// mid-session would stay half-invisible until a restart — which is the whole
// trade this design accepted, and the button is what makes it acceptable.
func TestRetryMissingAssetsResumesTheDemand(t *testing.T) {
	a := testTabApp(t)
	local := wireEmptyPackManager(t, a)
	a.installAssetOrigin(local.BaseURL())

	base := a.urls.CharIcon("mejiro_mcqueen")
	var stamps []time.Time
	a.iconAskBudget = charIconAskPerFrame
	a.demandAsset(&stamps, 1, 0, base, assets.AssetTypeCharIcon)
	settleMissUI(t, a, base, assets.AssetTypeCharIcon)

	if n := a.d.Manager.ConclusiveMissCount(); n == 0 {
		t.Fatal("nothing was remembered, so the flush below has nothing to prove")
	}
	a.d.Manager.ForgetConclusiveMisses()
	if n := a.d.Manager.ConclusiveMissCount(); n != 0 {
		t.Fatalf("%d chain(s) survived the flush", n)
	}

	stamps[0] = time.Time{}
	a.iconAskBudget = charIconAskPerFrame
	if !a.demandAsset(&stamps, 1, 0, base, assets.AssetTypeCharIcon) {
		t.Error("after the flush the icon is still reported as never arriving")
	}
	if a.iconAskBudget != charIconAskPerFrame-1 {
		t.Error("after the flush the icon was still not asked for — the button cannot recover a repacked server")
	}
}

// TestBlankEmoteCellDoesNotHoldTheFramePumpAwake is the "sub 10fps" half.
//
// A cell with neither emotions/button<N> art nor a char_icon draws a permanently
// grey box. It used to set frameDemandPending unconditionally on blankness, which
// tells NextWakeDelay to keep re-rendering at the demand cadence — so an idle
// client with such a grid on screen never settled, forever, for art that could not
// arrive. Now the flag rides demandAsset's answer.
//
// Drives the real drawEmoteImageButton rather than reasoning about the flag,
// because the bug was in the RELATIONSHIP between the two.
func TestBlankEmoteCellDoesNotHoldTheFramePumpAwake(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()
	local := wireEmptyPackManager(t, a)
	a.installAssetOrigin(local.BaseURL())

	const me = "silence_suzuka"
	a.emotes = make([]courtroom.Emote, 1)
	btn := sdl.Rect{X: 0, Y: 0, W: 48, H: 48}
	buttonBase := a.urls.EmoteButton(me, 1, false)
	iconBase := a.urls.CharIcon(me)

	// Frame 1 must FLAG: nothing has been probed, so the art may still arrive and
	// the pump has to stay awake to receive it. Without this the gate would pass
	// against a draw that never flagged anything.
	a.iconAskBudget = charIconAskPerFrame
	a.frameDemandPending = false
	a.drawEmoteImageButton(btn, me, 0, false, "normal")
	if !a.frameDemandPending {
		t.Fatal("a blank cell whose art is still unprobed did not hold the pump awake — it would sleep through " +
			"the arriving texture, and the gate below would be vacuous")
	}

	// Keep drawing until both chains are exhausted. The draw is the only thing
	// issuing these demands, so this also proves the cell reaches the miss path
	// through the shipped call site and not a test-only shortcut.
	settled := false
	for i := 0; i < missGateDrawFrames && !settled; i++ {
		a.iconAskBudget = charIconAskPerFrame
		a.emoteAsk, a.emoteIconAsk = nil, nil // reopen the retry window; the wall clock is not what this gate measures
		a.drawEmoteImageButton(btn, me, 0, false, "normal")
		drainManager(a)
		settled = a.d.Manager.IsConclusiveMiss(buttonBase, assets.AssetTypeEmoteButton) &&
			a.d.Manager.IsConclusiveMiss(iconBase, assets.AssetTypeCharIcon)
	}
	if !settled {
		t.Fatalf("after %d frames the cell's art was still not established absent — the draw stopped demanding",
			missGateDrawFrames)
	}

	// The permanently-grey frame: still blank, but nothing can change that, so the
	// pump is free to sleep.
	a.iconAskBudget = charIconAskPerFrame
	a.emoteAsk, a.emoteIconAsk = nil, nil
	a.frameDemandPending = false
	a.drawEmoteImageButton(btn, me, 0, false, "normal")
	if a.frameDemandPending {
		t.Error("a permanently-blank emote cell still held the frame pump awake — the client stays pinned at the " +
			"demand cadence, re-rendering for art that can never arrive")
	}
}

// TestInstallAssetOriginIsTheOnlyPlaceASessionOriginIsMinted is the encapsulation
// gate (rule §17.11): the reconnect flush must not be bypassable.
//
// The conclusive-miss memory has no expiry, so (re)installing an origin is the
// ONE moment a server gets a clean slate. installAssetOrigin does both jobs in one
// place precisely so they cannot drift; a second function that assigned a.urls a
// freshly-built URLBuilder would serve a previous visit's verdicts with no way to
// clear them short of the Settings button.
//
// A census over the whole package rather than a remembered list of functions: the
// bypass a gate like this exists to catch is a NEW call site, which by definition
// is not on any list. It is a deletion-catcher, not a mirror — it contains no copy
// of what installAssetOrigin does.
func TestInstallAssetOriginIsTheOnlyPlaceASessionOriginIsMinted(t *testing.T) {
	const owner = "installAssetOrigin"
	seenOwner := false
	packageFuncs(t, func(file, fn string, body *ast.BlockStmt) {
		mints := false
		ast.Inspect(body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "urls" {
					continue
				}
				// a.urls, not some other struct's urls field: the App's session
				// builder is the only one this rule governs (a preview or an
				// export stages its own, deliberately).
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "a" {
					continue
				}
				// A NEW builder, not a refinement of the installed one —
				// pollCasingProbe's `a.urls = a.urls.WithCharCase(...)` keeps the
				// same origin and must stay legal.
				if i < len(as.Rhs) && containsCall(as.Rhs[i], "NewURLBuilder") {
					mints = true
				}
			}
			return true
		})
		if fn == owner {
			seenOwner = true
			if !mints {
				t.Errorf("%s: %s no longer builds a URLBuilder — the origin install moved, and this gate now "+
					"guards nothing", file, fn)
			}
			if !containsCall(body, "ForgetConclusiveMissesUnder") {
				t.Errorf("%s: %s installs an origin without giving that origin's remembered misses a fresh look — "+
					"a server that repacked between visits stays half-invisible until a restart", file, fn)
			}
			return
		}
		if mints {
			t.Errorf("%s: %s assigns a.urls a freshly-built URLBuilder. Session origins are minted in %s alone, "+
				"which is where the conclusive-miss flush lives; minting one here serves the previous visit's "+
				"verdicts with no event able to clear them.", file, fn, owner)
		}
	})
	if !seenOwner {
		t.Fatalf("no %s in this package — it was renamed or removed, and this gate checked nothing", owner)
	}
}
