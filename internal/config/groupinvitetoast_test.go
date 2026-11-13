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
	if p.Load().GroupInviteOSToast {
		t.Error("on by default")
	}
	p.Update(func(sn *Settings) { sn.GroupInviteOSToast = true })
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if !reloaded.Load().GroupInviteOSToast {
		t.Error("the setting did not persist")
	}
}
