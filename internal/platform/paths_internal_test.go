package platform

import (
	"path/filepath"
	"testing"
)

func TestSamePathIgnoresCaseOnlyOnWindows(t *testing.T) {
	const upper, lower = "/Games/AO", "/games/ao"
	if !samePathOn(Windows, upper, lower) {
		t.Error("windows: case must not matter")
	}
	if samePathOn(Linux, upper, lower) {
		t.Error("linux: case must matter")
	}
	if !samePathOn(Linux, upper, upper+"/.") {
		t.Error("paths are compared cleaned")
	}
	if samePathOn(Linux, "", "") {
		t.Error("an unknown path matches nothing")
	}
}

func TestUserFoldersIn(t *testing.T) {
	const home = "home"
	got := UserFoldersIn(home)
	want := UserFolders{Home: home, Desktop: filepath.Join(home, "Desktop"), Downloads: filepath.Join(home, "Downloads")}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := UserFoldersIn(""); got != (UserFolders{}) {
		t.Errorf("no home: got %+v", got)
	}
}
