package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// cacheMoveWait bounds how long the test waits for the move goroutine.
const cacheMoveWait = 10 * time.Second

// waitCacheMove polls like the frame loop does until the move lands.
func waitCacheMove(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(cacheMoveWait)
	for settings.cacheMoving {
		if time.Now().After(deadline) {
			t.Fatal("the cache move never finished")
		}
		a.pollCacheFolder()
		time.Sleep(time.Millisecond)
	}
}

func TestCacheFolderMoveSavesTheFolder(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	disk, err := cache.NewDiskCache(cache.DiskRootIn(t.TempDir()), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	a := &App{d: Deps{Prefs: prefs, Disk: disk}}
	saved := settings
	t.Cleanup(func() { settings = saved })

	dir := t.TempDir()
	a.startCacheMove(dir, true)
	waitCacheMove(t, a)
	if got := prefs.Load().CacheDir(); got != dir {
		t.Fatalf("saved cache folder = %q, want %q", got, dir)
	}
	if disk.Root() != cache.DiskRootIn(dir) {
		t.Fatalf("cache root = %s, want it inside %s", disk.Root(), dir)
	}
	if settings.cacheStatusBad {
		t.Fatalf("a clean move must not report an error: %s", settings.cacheStatus)
	}

	// A folder inside the cache is refused, and the saved folder stays.
	a.startCacheMove(filepath.Join(disk.Root(), "inner"), true)
	waitCacheMove(t, a)
	if got := prefs.Load().CacheDir(); got != dir {
		t.Fatalf("a refused move changed the saved folder to %q", got)
	}
	if !settings.cacheStatusBad {
		t.Fatal("a refused move must report why")
	}
}
