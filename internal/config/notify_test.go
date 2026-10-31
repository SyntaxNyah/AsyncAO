package config

import (
	"path/filepath"
	"testing"
)

// TestCallwordOSToastPref pins the #M4 callword desktop-toast pref: OFF by default and a
// round-trip through the setter (covers the load-merge).
func TestCallwordOSToastPref(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if p.Load().CallwordOSToast {
		t.Error("callword OS toast should default OFF")
	}
	p.Update(func(
		sn *Settings) {

		sn.
			CallwordOSToast =

			true

	})
	if !p.Load().CallwordOSToast {
		t.Error("SetCallwordOSToast(true) didn't take")
	}
}

// TestGroupInviteOSToastPref pins the group-invite desktop-toast pref: OFF by default and a
// round-trip through Update (covers the load-merge).
func TestGroupInviteOSToastPref(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if p.Load().GroupInviteOSToast {
		t.Error("group-invite OS toast should default OFF")
	}
	p.Update(func(sn *Settings) {
		sn.GroupInviteOSToast = true
	})
	if !p.Load().GroupInviteOSToast {
		t.Error("GroupInviteOSToast=true didn't take")
	}
}

// TestMentionSelfPref pins the #203 self-mention pref: OFF by default and a
// round-trip through the setter (covers the load-merge).
func TestMentionSelfPref(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if p.Load().MentionSelf {
		t.Error("self-mention should default OFF")
	}
	p.Update(func(
		sn *Settings) {

		sn.
			MentionSelf =

			true

	})

	if !p.Load().MentionSelf {
		t.Error("SetMentionSelf(true) didn't take")
	}
}
