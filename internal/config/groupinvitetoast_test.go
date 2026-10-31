package config

import (
	"path/filepath"
	"testing"
)

func TestGroupInviteOSToastIsOffByDefaultAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.GroupInviteOSToastOn() {
		t.Error("on by default")
	}
	p.SetGroupInviteOSToast(true)
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	p.Close()
	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if !reloaded.GroupInviteOSToastOn() {
		t.Error("the setting did not persist")
	}
}
