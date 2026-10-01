package packetutil

import (
	"strings"
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

func TestVoiceCodecs(t *testing.T) {
	RegisterVoiceCodecs()

	// --- outbound (client→server) Fanta ---
	cases := []struct {
		header string
		args   []string
		want   string
	}{
		{"VS_JOIN", nil, "VS_JOIN#%"},
		{"VS_LEAVE", nil, "VS_LEAVE#%"},
		{"VS_SPEAK", []string{"1"}, "VS_SPEAK#1#%"},
		{"VS_FRAME", []string{"b64"}, "VS_FRAME#b64#%"},
	}
	for _, c := range cases {
		got, err := BuildWire(c.header, c.args, aolib.WireFanta)
		if err != nil {
			t.Fatalf("BuildWire(%s, Fanta): %v", c.header, err)
		}
		if string(got) != c.want {
			t.Errorf("BuildWire(%s, Fanta) = %q, want %q", c.header, got, c.want)
		}
	}

	// --- outbound JSON ---
	raw, err := BuildWire("VS_SPEAK", []string{"1"}, aolib.WireJSON)
	if err != nil {
		t.Fatalf("BuildWire(VS_SPEAK, JSON): %v", err)
	}
	if !strings.HasPrefix(string(raw), "{") || !strings.Contains(string(raw), `"$header":"VS_SPEAK"`) || !strings.Contains(string(raw), `"on":true`) {
		t.Errorf("VS_SPEAK JSON = %q", raw)
	}

	// --- inbound (server→client) Fanta folds to positional ---
	d := NewDecoder()
	hdr, args, err := d.DecodeBody([]byte("VS_CAPS#1#0#10#opus#48000#20#4096#%"))
	if err != nil {
		t.Fatalf("DecodeBody(VS_CAPS): %v", err)
	}
	if hdr != "VS_CAPS" || strings.Join(args, ",") != "1,0,10,opus,48000,20,4096" {
		t.Errorf("VS_CAPS = %q %v", hdr, args)
	}

	// --- inbound JSON folds to the same positional form ---
	hdr, args, err = d.DecodeBody([]byte(`{"$header":"VS_PEERS","uids":[1,2,3]}`))
	if err != nil {
		t.Fatalf("DecodeBody(VS_PEERS JSON): %v", err)
	}
	if hdr != "VS_PEERS" || strings.Join(args, ",") != "1,2,3" {
		t.Errorf("VS_PEERS JSON = %q %v", hdr, args)
	}

	hdr, args, err = d.DecodeBody([]byte(`{"$header":"VS_SPEAK","uid":7,"on":true}`))
	if err != nil {
		t.Fatalf("DecodeBody(VS_SPEAK JSON): %v", err)
	}
	if hdr != "VS_SPEAK" || strings.Join(args, ",") != "7,1" {
		t.Errorf("VS_SPEAK JSON = %q %v", hdr, args)
	}
}
