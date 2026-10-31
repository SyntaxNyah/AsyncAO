package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCrashLogFallsBackToTheCacheFolder(t *testing.T) {
	cache := t.TempDir()
	const appName = "AsyncAO test"
	// A folder that cannot be written: a file stands where the parent folder should be.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, crashLogMode); err != nil {
		t.Fatal(err)
	}
	f := openCrashLog(blocker, cache, appName)
	if f == nil {
		t.Fatal("no crash log opened")
	}
	defer f.Close()
	if want := filepath.Join(cache, appName, crashLogName); f.Name() != want {
		t.Errorf("opened %s, want %s", f.Name(), want)
	}
}

func TestCrashLogPrefersBesideTheExecutable(t *testing.T) {
	exeDir := t.TempDir()
	f := openCrashLog(exeDir, t.TempDir(), "AsyncAO test")
	if f == nil {
		t.Fatal("no crash log opened")
	}
	defer f.Close()
	if want := filepath.Join(exeDir, crashLogName); f.Name() != want {
		t.Errorf("opened %s, want %s", f.Name(), want)
	}
}

func TestCrashLogMayBeUnavailable(t *testing.T) {
	if f := openCrashLog("", "", "AsyncAO test"); f != nil {
		f.Close()
		t.Errorf("opened %s with no folder known", f.Name())
	}
}
