package ui

// The UI half of "a mounted base answers char.ini too" (issue #72).
//
// internal/assets proves FetchRaw reads the pack. These prove the two
// things only this package can answer: that the ini readers actually CALL it —
// the whole defect was a working pack layer nothing on this side reached — and
// that a source change drops the PARSED copies, which no texture invalidation
// touches.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// packAppOrigin is a server the test never stands up. That is the point: every
// byte these gates see has to come from the mounted folder, so a fetch that
// reaches the network fails by timing out rather than by quietly succeeding.
const packAppOrigin = "http://pack.invalid.test/base/"

// packApp is testTabApp plus a streaming Manager with a folder holding files layered over packAppOrigin.
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
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)

	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver: assets.NewResolver(a.d.Prefs),
		Prefs:    a.d.Prefs,
		T2:       t2,
		Disk:     disk,
		Source:   network.NewClientNotFoundTTL(0, netproxy.Direct()),
		Decoder:  decoder,
	})
	t.Cleanup(a.d.Manager.CloseMedia)

	dir := t.TempDir()
	writePackFiles(t, dir, files)
	a.d.Manager.SetFolders([]string{dir}, []string{packAppOrigin})

	a.urls = courtroom.NewURLBuilder(packAppOrigin)
	a.sess = &courtroom.Session{}
	a.charINIres = make(chan charINIFetch, 1)
	return a
}

// writePackFiles lays out a folder holding files, keyed by slash path.
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

// TestCheckFoldersNowForgetsEveryParsedINI: a parsed ini is UI state no texture cache knows about, so without this
// an edited char.ini keeps its old answer for the rest of the session.
func TestCheckFoldersNowForgetsEveryParsedINI(t *testing.T) {
	a := packApp(t, map[string]string{"characters/witch/char.ini": "[Options]\nshowname = X\n"})
	a.charMetaCache = map[string]charMeta{a.charINIURL("witch"): {showname: "STALE", done: true}}
	a.previewChar = "witch"
	a.previewAnims = []string{"stale"}
	a.previewLabels = []string{"Stale"}
	a.previewEmoteIdx = 3
	a.iniWarmed = "witch"

	a.CheckFoldersNow()

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
