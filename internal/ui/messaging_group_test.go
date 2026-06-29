package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestGroupStateMachine pins the role-sensitive transitions: invite sets owner +
// members, a group message implicitly adds its sender, only the OWNER can kick, and
// being kicked drops the group locally.
func TestGroupStateMachine(t *testing.T) {
	a := &App{}
	a.sess = &courtroom.Session{PlayerID: 7} // me = uid 7

	a.applyGroupInvite(100, "Squad", 3, "Owner")
	g := a.msgGroups[100]
	if g == nil || g.ownerUID != 3 || !g.hasMember(3) || !g.hasMember(7) {
		t.Fatalf("invite didn't set up the group: %+v", g)
	}

	a.applyGroupText(100, 5, "Bob", "hi all")
	if !g.hasMember(5) || len(g.lines) != 1 || g.lines[0].text != "hi all" {
		t.Errorf("group text not applied: %+v", g)
	}

	a.applyGroupKick(100, 5, 3) // a non-owner must not be able to kick
	if !g.hasMember(3) {
		t.Error("non-owner kick must be ignored")
	}

	a.applyGroupKick(100, 3, 5) // the owner kicks Bob
	if g.hasMember(5) {
		t.Error("owner kick should remove the member")
	}

	a.applyGroupKick(100, 3, 7) // the owner kicks ME → group dropped locally
	if _, ok := a.msgGroups[100]; ok {
		t.Error("being kicked should drop the group locally")
	}
}

// TestGroupTextUnknownIgnored: a group message for a group we never got an invite to
// is ignored (no phantom group created).
func TestGroupTextUnknownIgnored(t *testing.T) {
	a := &App{}
	a.sess = &courtroom.Session{PlayerID: 7}
	a.applyGroupText(999, 3, "X", "ghost")
	if len(a.msgGroups) != 0 {
		t.Error("group text for an unknown group must not create one")
	}
}
