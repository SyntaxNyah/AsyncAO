package packetutil

import (
	"strings"
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

func TestIsJSON(t *testing.T) {
	if !IsJSON([]byte(`{"$header":"ID"}`)) {
		t.Fatal("JSON object should be detected")
	}
	if IsJSON([]byte("CC#1#2#3#%")) {
		t.Fatal("Fanta frame must not be detected as JSON")
	}
	if IsJSON(nil) {
		t.Fatal("empty frame must not be detected as JSON")
	}
}

func TestBuildWireFanta(t *testing.T) {
	cases := []struct {
		header string
		args   []string
		want   string
	}{
		{"CC", []string{"1", "2", "hdid"}, "CC#1#2#hdid#%"},
		{"HI", []string{"hdid"}, "HI#hdid#%"},
		{"CH", []string{"7"}, "CH#7#%"},
		{"ID", []string{"AsyncAO", "2.11.0-asyncao"}, "ID#AsyncAO#2.11.0-asyncao#%"},
		// escaping: # % $ & -> <num> <percent> <dollar> <and>
		{"CT", []string{"name", "hello#world"}, "CT#name#hello<num>world#%"},
		{"CT", []string{"a&b", "c%d$e"}, "CT#a<and>b#c<percent>d<dollar>e#%"},
		// MS has no typed model yet: falls through to positional framing and
		// must preserve every field verbatim.
		{"MS", []string{"0", "pre", "char", "emote", "msg"}, "MS#0#pre#char#emote#msg#%"},
	}
	for _, c := range cases {
		got, err := BuildWire(c.header, c.args, aolib.WireFanta)
		if err != nil {
			t.Fatalf("BuildWire(%q, Fanta): %v", c.header, err)
		}
		if string(got) != c.want {
			t.Errorf("BuildWire(%q, Fanta) = %q, want %q", c.header, got, c.want)
		}
	}
}

func TestBuildWireJSON(t *testing.T) {
	raw, err := BuildWire("CC", []string{"1", "2", "hdid"}, aolib.WireJSON)
	if err != nil {
		t.Fatalf("BuildWire(CC, JSON): %v", err)
	}
	v, err := aolib.Decode(raw, aolib.WireJSON)
	if err != nil {
		t.Fatalf("decode CC JSON %q: %v", raw, err)
	}
	cc, ok := v.(*aolib.CC)
	if !ok {
		t.Fatalf("decoded %T, want *aolib.CC", v)
	}
	if cc.PlayerID != 1 || cc.CharID != 2 || cc.CharPassword != "hdid" {
		t.Errorf("CC = %+v, want PlayerID=1 CharID=2 CharPassword=hdid", cc)
	}

	// CH (the keepalive) round-trips too.
	raw, err = BuildWire("CH", []string{"7"}, aolib.WireJSON)
	if err != nil {
		t.Fatalf("BuildWire(CH, JSON): %v", err)
	}
	v, err = aolib.Decode(raw, aolib.WireJSON)
	if err != nil {
		t.Fatalf("decode CH JSON %q: %v", raw, err)
	}
	if ch, ok := v.(*aolib.CH); !ok || ch.CharID != 7 {
		t.Errorf("CH = %+v (%T), want CharID=7", v, v)
	}
}

func TestDecodeBodyFanta(t *testing.T) {
	d := NewDecoder()
	hdr, args, err := d.DecodeBody([]byte("PN#5#100#%"))
	if err != nil {
		t.Fatalf("DecodeBody(PN): %v", err)
	}
	if hdr != "PN" || strings.Join(args, ",") != "5,100" {
		t.Errorf("got %q %v, want PN [5 100]", hdr, args)
	}

	// Escaped fields are unescaped on the way back out.
	hdr, args, err = d.DecodeBody([]byte("CT#name#hello<num>world#%"))
	if err != nil {
		t.Fatalf("DecodeBody(CT): %v", err)
	}
	if hdr != "CT" || len(args) != 2 || args[1] != "hello#world" {
		t.Errorf("got %q %v, want CT [name hello#world]", hdr, args)
	}
}

func TestDecodeBodyJSON(t *testing.T) {
	d := NewDecoder()

	// Encode a server->client ID packet via aolib, then decode it back to the
	// same positional fields the switch consumes.
	raw, err := aolib.Encode(&aolib.IDToClient{PlayerID: 42, Software: "Nyathena", Version: "2.4.3"}, aolib.WireJSON)
	if err != nil {
		t.Fatalf("encode IDToClient JSON: %v", err)
	}
	hdr, args, err := d.DecodeBody(raw)
	if err != nil {
		t.Fatalf("DecodeBody(ID JSON) %q: %v", raw, err)
	}
	if hdr != "ID" || strings.Join(args, ",") != "42,Nyathena,2.4.3" {
		t.Errorf("got %q %v, want ID [42 Nyathena 2.4.3]", hdr, args)
	}

	// PN with a description survives the fold-back.
	raw, err = aolib.Encode(&aolib.PN{PlayerCount: 5, MaxPlayers: 100, ServerDescription: "test room"}, aolib.WireJSON)
	if err != nil {
		t.Fatalf("encode PN JSON: %v", err)
	}
	hdr, args, err = d.DecodeBody(raw)
	if err != nil {
		t.Fatalf("DecodeBody(PN JSON): %v", err)
	}
	if hdr != "PN" || strings.Join(args, ",") != "5,100,test room" {
		t.Errorf("got %q %v, want PN [5 100 \"test room\"]", hdr, args)
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	fields := []string{"plain", "hash#tag", "amp&ersand", "per%cent", "dol$lar", "all#&%$"}
	escaped := EscapeAll(fields)
	back := UnescapeAll(escaped)
	for i := range fields {
		if back[i] != fields[i] {
			t.Errorf("round-trip %d: %q -> %q -> %q", i, fields[i], escaped[i], back[i])
		}
	}
}
