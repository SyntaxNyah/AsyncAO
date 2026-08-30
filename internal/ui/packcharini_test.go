package ui

// The UI half of "a mounted base answers char.ini too" (issue #72).
//
// internal/assets proves FetchRawLayered reads the pack. These prove the two
// things only this package can answer: that the ini readers actually CALL it —
// the whole defect was a working pack layer nothing on this side reached — and
// that a source change drops the PARSED copies, which no texture invalidation
// touches.

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// packAppOrigin is a server the test never stands up. That is the point: every
// byte these gates see has to come from the mounted folder, so a fetch that
// reaches the network fails by timing out rather than by quietly succeeding.
const packAppOrigin = "http://pack.invalid.test/base/"

// packApp is testTabApp plus a STREAMING-shaped Manager (LocalMode false, which
// is what leaves the mount layer live) over a folder holding files.
func packApp(t *testing.T, files map[string]string) *App {
	t.Helper()
	a := testTabApp(t)

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

	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver: assets.NewResolver(a.d.Prefs),
		Prefs:    a.d.Prefs,
		T2:       t2,
		Disk:     disk,
		Source:   network.NewClient(),
		Pool:     pool,
		Decoder:  decoder,
	})

	dir := t.TempDir()
	writePackFiles(t, dir, files)
	idx, errs := assets.BuildMountIndex([]string{dir})
	if len(errs) != 0 {
		t.Fatalf("index build: %v", errs)
	}
	t.Cleanup(idx.Retire)
	a.d.Manager.SetMountLayer(assets.NewMountLayer(idx, []string{dir}, []string{packAppOrigin}))

	a.urls = courtroom.NewURLBuilder(packAppOrigin)
	a.sess = &courtroom.Session{}
	a.charINIres = make(chan charINIFetch, 1)
	return a
}

// writePackFiles lays out a mount folder for these gates. The sibling helper in
// mountlayer_test.go writes ONE placeholder file; these gates need real content,
// since the content is the thing under test.
func writePackFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// awaitCharINI drains the load the way the frame loop does, failing rather than
// hanging. A timeout here means the read went to packAppOrigin, which does not
// exist — i.e. the pack was not consulted.
func awaitCharINI(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.charINIBusy {
		a.pollCharINI()
		if time.Now().After(deadline) {
			t.Fatal("the char.ini load never settled — it is not reading the mount")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestMountedPackSuppliesTheEmoteList is issue #72 end to end, at the surface the
// reporter was looking at: the emote strip. A character whose folder lives in the
// user's own mount must bring its own emotes, and before this the list was read
// off the server while the sprites beside it came from the pack.
func TestMountedPackSuppliesTheEmoteList(t *testing.T) {
	a := packApp(t, map[string]string{
		"characters/witch/char.ini": "[Emotions]\nnumber = 2\n" +
			"1 = Grin#-#grin#0#\n2 = Glare#-#glare#0#\n",
	})
	a.iniChar = "witch" // activeCharName's override rung — a worn local folder

	a.loadCharINI()
	awaitCharINI(t, a)

	if len(a.emotes) != 2 {
		t.Fatalf("emote list = %d entries (%v), want the PACK's 2 — the ini was read off the server", len(a.emotes), a.emotes)
	}
	if a.emotes[0].Anim != "grin" || a.emotes[1].Anim != "glare" {
		t.Errorf("emotes = %q/%q, want grin/glare", a.emotes[0].Anim, a.emotes[1].Anim)
	}
	if a.warnLine != "" {
		t.Errorf("warned %q about a char.ini it successfully read", a.warnLine)
	}
}

// TestPackCharMetaComesFromTheMount is the same gate for the OTHER ini reader:
// the per-speaker metadata (showname, blips, chatbox skin, effects, scaling,
// idle pose). It has its own fetch and its own cache, so it can regress alone.
func TestPackCharMetaComesFromTheMount(t *testing.T) {
	a := packApp(t, map[string]string{
		"characters/witch/char.ini": "[Options]\nshowname = The Witch\nblips = female\n",
	})

	if m := a.charMetaFor("witch"); m.done {
		t.Fatal("the first ask must be a miss that starts the fetch")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.pollCharMeta()
		if m := a.charMetaFor("witch"); m.done {
			if m.showname != "The Witch" {
				t.Errorf("showname = %q, want the PACK's %q", m.showname, "The Witch")
			}
			if m.blips != "female" {
				t.Errorf("blips = %q, want the PACK's %q", m.blips, "female")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the char.ini metadata fetch never settled — it is not reading the mount")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestRescanForgetsEveryParsedINI pins the invalidation. An ini is parsed into UI
// state the texture caches know nothing about, so without this a sprite maker's
// edit-rescan-look loop shows the answer from before the edit for the rest of the
// session — the feature reads as broken even though the fetch is now correct.
func TestRescanForgetsEveryParsedINI(t *testing.T) {
	a := packApp(t, map[string]string{"characters/witch/char.ini": "[Options]\nshowname = X\n"})
	a.charMetaCache = map[string]charMeta{a.charINIURL("witch"): {showname: "STALE", done: true}}
	a.previewChar = "witch"
	a.previewAnims = []string{"stale"}
	a.previewLabels = []string{"Stale"}
	a.previewEmoteIdx = 3
	a.iniWarmed = "witch"

	a.rescanLocalPacks(nil)

	if len(a.charMetaCache) != 0 {
		t.Errorf("charMetaCache survived a source change (%v) — every speaker keeps their pre-edit ini", a.charMetaCache)
	}
	if a.previewChar != "" || a.previewAnims != nil || a.previewLabels != nil || a.previewEmoteIdx != 0 {
		t.Errorf("the preview kept its latch (%q/%v) — re-hovering the same character redraws the pre-edit emotes",
			a.previewChar, a.previewAnims)
	}
	if a.iniWarmed != "" {
		t.Error("the hover-warm latch survived — the next hover skips the refetch")
	}
}

// TestEveryCharINIReadIsLayered is the encapsulation gate (rule §17.11).
//
// The bug this feature fixes was not a missing capability — the pack layer
// worked — it was a set of call sites reaching for the SERVER-ONLY lane. That
// mistake is invisible: FetchRaw returns perfectly good bytes, just the wrong
// side's. So the gate is a census with a justified allow-list rather than a list
// of the readers we happen to remember: a NEW text fetch defaults to failing
// here, and its author has to say which side it means.
//
// It is a deletion-catcher too — it contains no copy of what FetchRawLayered
// does, and it fails if any of the named readers stops calling it.
func TestEveryCharINIReadIsLayered(t *testing.T) {
	// The functions that mean the SERVER's copy, and why. Each is a case where a
	// pack answering would be wrong, not merely unhelpful.
	unlayered := map[string]string{
		// extensions.json is the server's own format declaration and seeds that
		// host's learned probe order; a pack carved out of another server ships a
		// copy that would reseed it from the wrong conventions.
		"seedOriginFormats": "the manifest is the SERVER's format declaration",
		// Learns THIS host's character-folder casing. A pack answering would teach
		// the server a convention only the pack follows.
		"probeCasing": "casing is a property of the server's filesystem",
		// iniswap.txt and the backgrounds autoindex are server-curated listings; a
		// pack has no equivalent, so answering would replace a real list with none.
		"ensureIniList": "iniswap.txt + the backgrounds listing are server-curated",
		"ensureBgList":  "an autoindex listing has no pack equivalent",
		// The content report and the exporters deliberately distinguish the two
		// sources themselves (ResolveRawLayered reports which arm answered), so
		// their raw reads mean the server's copy specifically.
		"probeRef":         "the report resolves pack bytes through ResolveRawLayered",
		"fetchBundleBytes": "bundling reads what the SERVER would serve",
		"fetchTempAudio":   "the video muxer's source is the resolved server URL",
	}
	// Readers that must keep reaching the layered lane. Naming them is what makes
	// this a deletion-catcher: silently dropping the call would otherwise leave a
	// green suite and a feature that quietly stopped working.
	layered := map[string]bool{
		"loadCharINI":         false, // the worn character's emote list
		"ensurePreviewEmotes": false, // try-before-wear
		"charMetaFetchOne":    false, // showname / blips / skin / effects / scaling / idle
	}

	packageFuncs(t, func(file, fn string, body *ast.BlockStmt) {
		if containsCall(body, "FetchRawLayered") {
			if _, named := layered[fn]; named {
				layered[fn] = true
			}
		}
		if !containsCall(body, "FetchRaw") {
			return
		}
		if _, ok := unlayered[fn]; ok {
			return
		}
		t.Errorf("%s: %s reads an asset through FetchRaw, the SERVER-ONLY lane. If it reads a char.ini, an "+
			"effects.ini or anything else a content pack ships, use FetchRawLayered — a user's own folder "+
			"answering their own character is the whole of issue #72. If the server's copy is genuinely what "+
			"is meant, add %s to this gate's allow-list with the reason.", file, fn, fn)
	})

	for fn, seen := range layered {
		if !seen {
			t.Errorf("%s no longer calls FetchRawLayered — the ini reader it owns went back to reading the "+
				"server's copy over the user's own folder", fn)
		}
	}
}
