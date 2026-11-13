package host

import (
	"log"
	"os"
	"path/filepath"
	"strings"
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
	f := openAppFile(fileDirs{exe: blocker, cache: cache}, appName, crashLogName, os.O_CREATE|os.O_WRONLY)
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
	f := openAppFile(fileDirs{exe: exeDir, cache: t.TempDir()}, "AsyncAO test", crashLogName, os.O_CREATE|os.O_WRONLY)
	if f == nil {
		t.Fatal("no crash log opened")
	}
	defer f.Close()
	if want := filepath.Join(exeDir, crashLogName); f.Name() != want {
		t.Errorf("opened %s, want %s", f.Name(), want)
	}
}

func TestCrashLogMayBeUnavailable(t *testing.T) {
	if f := openAppFile(fileDirs{}, "AsyncAO test", crashLogName, os.O_CREATE|os.O_WRONLY); f != nil {
		f.Close()
		t.Errorf("opened %s with no folder known", f.Name())
	}
}

// TestLogToFileKeepsEveryLine: the debug log file gets each log line as it is written, even with no usable stderr
// (a release build started without a console); a fresh one starts empty, and a restarted run appends to it.
func TestLogToFileKeepsEveryLine(t *testing.T) {
	path := filepath.Join(appDirs().exe, debugLogName)
	t.Cleanup(func() { _ = os.Remove(path) })
	prev, prevStderr := log.Writer(), os.Stderr
	t.Cleanup(func() {
		log.SetOutput(prev)
		os.Stderr = prevStderr
	})
	noConsole, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	_ = noConsole.Close() // every write to it now fails, like a GUI process's missing stderr
	os.Stderr = noConsole

	const first, second = "first run line", "restarted run line"
	closeLog := LogToFile("AsyncAO test", true)
	log.Print(first)
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), first) {
		t.Fatalf("the line wasn't in %s while the run was going: %q, %v", path, data, err)
	}
	closeLog()

	closeLog = LogToFile("AsyncAO test", false)
	log.Print(second)
	closeLog()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), first) || !strings.Contains(string(data), second) {
		t.Errorf("a restarted run lost the earlier lines: %q", data)
	}

	closeLog = LogToFile("AsyncAO test", true)
	closeLog()
	if data, _ := os.ReadFile(path); strings.Contains(string(data), first) {
		t.Errorf("a fresh run kept the old lines: %q", data)
	}
}
