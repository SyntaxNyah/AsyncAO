package assets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	packFileMode = 0o644
	packDirMode  = 0o755
	// packLater moves a folder's modified time past any file system's timestamp precision.
	packLater = time.Minute
)

// packRig wires a Manager against a real httptest origin plus the player's folder.
type packRig struct {
	rig    *testRig
	origin string
	dir    string
	mu     sync.Mutex
	hits   map[string]int
}

// newPackRig serves server and writes pack into a mounted folder, counting every request path, so a test can assert
// the folder cost the network nothing.
func newPackRig(tb testing.TB, server, pack map[string]string) *packRig {
	tb.Helper()
	pr := &packRig{hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		pr.mu.Lock()
		pr.hits[r.URL.Path]++
		pr.mu.Unlock()
		body, ok := server[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)
	pr.origin = srv.URL + "/base/"
	pr.dir = tb.TempDir()
	writePack(tb, pr.dir, pack)
	pr.rig = newRig(tb, network.NewClientNotFoundTTL(0, netproxy.Direct()), false)
	pr.rig.manager.SetFolders([]string{pr.dir}, []string{pr.origin})
	return pr
}

func (pr *packRig) base(rel string) string { return pr.origin + rel }

func (pr *packRig) hitsFor(path string) int {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.hits[path]
}

func writePack(tb testing.TB, root string, files map[string]string) {
	tb.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), packDirMode); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), packFileMode); err != nil {
			tb.Fatal(err)
		}
	}
}

// awaitNetFetches polls the manager's own counter, which a warm can be observed through without racing the handler.
func awaitNetFetches(t *testing.T, m *Manager, want int64) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.Stats().NetFetches >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// A sprite maker's own char.ini decides their emote list, showname, blips and chatbox, so the folder answers it.
func TestFolderServesCharINI(t *testing.T) {
	const ini = "[Options]\nshowname = Witch\n"
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": ini},
	)

	data, err := pr.rig.manager.FetchRaw(context.Background(), pr.base("characters/witch/char.ini"))
	if err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if string(data) != ini {
		t.Errorf("got %q, want the folder's ini", data)
	}
	if n := pr.hitsFor("/base/characters/witch/char.ini"); n != 0 {
		t.Errorf("the server was asked %d time(s) for a file the folder holds", n)
	}
	if got := pr.rig.manager.Stats().MountFetches; got != 1 {
		t.Errorf("MountFetches = %d, want 1", got)
	}
}

// The order is memory, disk cache, folders, web: folders save traffic, they don't override what the server sent.
func TestCachedServerCopyBeatsTheFolders(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{},
	)
	url := pr.base("characters/witch/char.ini")
	if _, err := pr.rig.manager.FetchRaw(context.Background(), url); err != nil {
		t.Fatalf("seeding the server copy: %v", err)
	}
	writePack(t, pr.dir, map[string]string{"characters/witch/char.ini": "FOLDER INI"})

	data, err := pr.rig.manager.FetchRaw(context.Background(), url)
	if err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if string(data) != "SERVER INI" {
		t.Errorf("got %q, want the cached server copy", data)
	}
}

func TestFolderMissFallsThroughToServer(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/other/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)

	data, err := pr.rig.manager.FetchRaw(context.Background(), pr.base("characters/other/char.ini"))
	if err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if string(data) != "SERVER INI" {
		t.Errorf("got %q, want the server's copy", data)
	}
	if st := pr.rig.manager.Stats(); st.MountFetches != 0 || st.NetFetches != 1 {
		t.Errorf("MountFetches = %d, NetFetches = %d; want 0 and 1", st.MountFetches, st.NetFetches)
	}
}

// Folder bytes must never land in a cache under the server's URL: they would outlive the folder there and be served
// as the server's own content after it is unmounted.
func TestFolderTextWritesNoServerKeyedCache(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)
	url := pr.base("characters/witch/char.ini")

	if _, err := pr.rig.manager.FetchRaw(context.Background(), url); err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if _, ok := pr.rig.manager.t2.Get(url); ok {
		t.Error("folder bytes were cached in memory under the server's URL")
	}
	if _, ok := pr.rig.manager.disk.Get(url); ok {
		t.Error("folder bytes were written to the disk cache under the server's URL")
	}
}

func TestFolderTextNeverTeachesLearnedFormats(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)
	host := HostOf(pr.origin)
	before := pr.rig.manager.resolver.LearnedList(host, AssetTypeCharSprite)

	if _, err := pr.rig.manager.FetchRaw(context.Background(), pr.base("characters/witch/char.ini")); err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if after := pr.rig.manager.resolver.LearnedList(host, AssetTypeCharSprite); len(after) != len(before) {
		t.Errorf("the folder taught the server host a format: %v -> %v", before, after)
	}
}

// A folder is often carved out of some server's base and ships its extensions.json; serving it would reseed the
// current host's format order from the folder author's conventions.
func TestFolderNeverAnswersTheServerFormatList(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/" + ManifestFileName: "SERVER MANIFEST"},
		map[string]string{ManifestFileName: "PACK MANIFEST"},
	)

	data, err := pr.rig.manager.FetchRaw(context.Background(), pr.base(ManifestFileName))
	if err != nil {
		t.Fatalf("FetchRaw(%s): %v", ManifestFileName, err)
	}
	if string(data) != "SERVER MANIFEST" {
		t.Errorf("got %q — the folder answered for the server's own format list", data)
	}
}

// Warming a file the folder answers buys nothing and would spend a request on a URL the server need not have.
func TestPrefetchRawSkipsWhatTheFolderHolds(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)

	pr.rig.manager.PrefetchRaw(pr.base("characters/witch/char.ini"), network.PriorityLow)

	if awaitNetFetches(t, pr.rig.manager, 1) {
		t.Error("the warm asked the server for a file the folder holds")
	}
}

func TestPrefetchRawStillWarmsWhatTheFolderLacks(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/other/char.ini": "SERVER INI"},
		map[string]string{"characters/witch/char.ini": "PACK INI"},
	)

	pr.rig.manager.PrefetchRaw(pr.base("characters/other/char.ini"), network.PriorityLow)

	if !awaitNetFetches(t, pr.rig.manager, 1) {
		t.Error("the server was never asked — mounting a folder disarmed the warm for everything else")
	}
}

// A file dropped into a folder after the client looked for it is found once the folders are re-checked, before the
// web is asked again, and the web's 404 memory is kept (hard rule 6).
func TestFileDroppedInAfterAMissIsFoundOnRecheck(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{},
		map[string]string{"characters/witch/(a)normal.png": "x"},
	)
	url := pr.base("characters/witch/char.ini")
	if _, err := pr.rig.manager.FetchRaw(context.Background(), url); err == nil {
		t.Fatal("found a char.ini that is not there yet")
	}
	asked := pr.hitsFor("/base/characters/witch/char.ini")

	writePack(t, pr.dir, map[string]string{"characters/witch/char.ini": "DROPPED IN"})
	later := time.Now().Add(packLater)
	if err := os.Chtimes(filepath.Join(pr.dir, "characters", "witch"), later, later); err != nil {
		t.Fatal(err)
	}
	pr.rig.manager.RecheckFolders()

	data, err := pr.rig.manager.FetchRaw(context.Background(), url)
	if err != nil || string(data) != "DROPPED IN" {
		t.Fatalf("after RecheckFolders: %q, %v", data, err)
	}
	if n := pr.hitsFor("/base/characters/witch/char.ini"); n != asked {
		t.Errorf("the web was asked again (%d → %d) for a file the folder now holds", asked, n)
	}
}

func TestFetchRawWithoutFoldersAllocatesNoMore(t *testing.T) {
	pr := newPackRig(t,
		map[string]string{"/base/characters/witch/char.ini": "SERVER INI"},
		map[string]string{},
	)
	url := pr.base("characters/witch/char.ini")
	ctx := context.Background()
	if _, err := pr.rig.manager.FetchRaw(ctx, url); err != nil {
		t.Fatalf("priming: %v", err)
	}
	withFolders := testing.AllocsPerRun(100, func() { _, _ = pr.rig.manager.FetchRaw(ctx, url) })
	pr.rig.manager.SetFolders(nil, nil)
	without := testing.AllocsPerRun(100, func() { _, _ = pr.rig.manager.FetchRaw(ctx, url) })
	if withFolders > without {
		t.Errorf("a memory hit allocates %v with folders vs %v without: the cache is answered first", withFolders, without)
	}
}
