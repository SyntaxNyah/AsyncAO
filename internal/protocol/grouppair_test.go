package protocol

import (
	"encoding/json"
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
)

func TestFromGP(t *testing.T) {
	raw := []byte(`{"$header":"GP","group_id":"100","members":[
		{"uid":100,"char_id":0,"name":"Phoenix","emote":"normal","offset":{"x":0,"y":0},"flip":"none","order":0},
		{"uid":101,"char_id":1,"name":"Maya","emote":"normal","offset":{"x":10,"y":20},"flip":"horizontal","order":1}
	]}`)

	var gp packetutil.GP
	if err := json.Unmarshal(raw, &gp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	g := FromGP(&gp)
	if g == nil || g.GroupID != "100" {
		t.Fatalf("bad group: %+v", g)
	}
	if g.Empty() {
		t.Fatalf("group of 2 should not be empty")
	}
	if len(g.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(g.Members))
	}
	m0, m1 := g.Members[0], g.Members[1]
	if m0.CharID != 0 || m0.Order != 0 || m0.UID != 100 {
		t.Errorf("member[0]: %+v", m0)
	}
	if m1.Order != 1 || m1.Flip != aolib.FlipHorizontal || m1.OffsetX != 10 || m1.OffsetY != 20 {
		t.Errorf("member[1]: %+v", m1)
	}
}

func TestGroupPairFromPacket(t *testing.T) {
	gp := &packetutil.GP{GroupID: "7", Members: []packetutil.GPMember{
		{UID: 1, CharID: 0, Order: 0},
		{UID: 2, CharID: 1, Order: 1},
	}}
	p := NewTypedPacket(gp)
	g := GroupPairFromPacket(p)
	if g == nil || g.GroupID != "7" || len(g.Members) != 2 {
		t.Fatalf("GroupPairFromPacket: %+v", g)
	}
	if got := GroupPairFromPacket(NewPacket("ZZ")); got != nil {
		t.Fatalf("non-GP packet should yield nil, got %+v", got)
	}
}

func TestParseAdditionalChars(t *testing.T) {
	raw := []byte(`[
		{"charid":0,"name":"Phoenix","emote":"normal","side":"def","offset":{"x":0,"y":0},"flip":"none","order":0},
		{"charid":1,"name":"Maya","emote":"normal","side":"pro","offset":{"x":10,"y":20},"flip":"horizontal","order":1}
	]`)

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	members := ParseAdditionalChars(v)
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	m0, m1 := members[0], members[1]
	if m0.CharID != 0 || m0.Name != "Phoenix" || m0.Side != "def" || m0.OffsetX != 0 || m0.OffsetY != 0 || m0.Flip != aolib.FlipNone {
		t.Errorf("member[0]: %+v", m0)
	}
	if m1.CharID != 1 || m1.Side != "pro" || m1.OffsetX != 10 || m1.OffsetY != 20 || m1.Flip != aolib.FlipHorizontal || m1.Order != 1 {
		t.Errorf("member[1]: %+v", m1)
	}

	if got := ParseAdditionalChars(nil); got != nil {
		t.Errorf("nil -> %+v, want nil", got)
	}
	if got := ParseAdditionalChars("not-an-array"); got != nil {
		t.Errorf("non-array -> %+v, want nil", got)
	}
}
