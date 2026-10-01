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

func TestEncodeCustomVoice(t *testing.T) {
	RegisterVoiceCodecs()

	// Outbound (client→server) Fanta.
	for _, c := range []struct {
		header string
		val    aolib.Outgoing
		want   string
	}{
		{"VS_JOIN", &VS_JOINToServer{}, "VS_JOIN#%"},
		{"VS_LEAVE", &VS_LEAVEToServer{}, "VS_LEAVE#%"},
		{"VS_SPEAK", &VS_SPEAKToServer{On: true}, "VS_SPEAK#1#%"},
		{"VS_FRAME", &VS_FRAME{Payload: "b64"}, "VS_FRAME#b64#%"},
	} {
		got, err := EncodeCustom(c.header, c.val, aolib.WireFanta)
		if err != nil {
			t.Fatalf("EncodeCustom(%s, Fanta): %v", c.header, err)
		}
		if string(got) != c.want {
			t.Errorf("EncodeCustom(%s, Fanta) = %q, want %q", c.header, got, c.want)
		}
	}

	// Outbound JSON.
	raw, err := EncodeCustom("VS_SPEAK", &VS_SPEAKToServer{On: true}, aolib.WireJSON)
	if err != nil {
		t.Fatalf("EncodeCustom(VS_SPEAK, JSON): %v", err)
	}
	if !strings.HasPrefix(string(raw), "{") || !strings.Contains(string(raw), `"$header":"VS_SPEAK"`) || !strings.Contains(string(raw), `"on":true`) {
		t.Errorf("VS_SPEAK JSON = %q", raw)
	}

	// Unknown header has no codec.
	if _, err := EncodeCustom("NOPE", &VS_FRAME{}, aolib.WireFanta); err == nil {
		t.Error("EncodeCustom for an unregistered header should error")
	}
}

func TestVoiceCodecInbound(t *testing.T) {
	RegisterVoiceCodecs()

	// aolib's session decodes custom headers through the registered codec and
	// hands the typed value to OnCustom — the full inbound path the conn uses.
	var got aolib.Outgoing
	sess := aolib.NewServer(aolib.SessionConfig{
		Send: func([]byte) {},
	})
	_ = sess.OnCustom("VS_CAPS", func(p any) { got = p.(aolib.Outgoing) })
	sess.Receive([]byte("VS_CAPS#1#0#10#opus#48000#20#4096#%"))
	caps, ok := got.(*VS_CAPS)
	if !ok {
		t.Fatalf("decoded %T, want *VS_CAPS", got)
	}
	if !caps.Enabled || caps.PttOnly || caps.MaxPeers != 10 || caps.Codec != "opus" || caps.SampleRate != 48000 || caps.FrameMs != 20 || caps.MaxFrameBytes != 4096 {
		t.Errorf("VS_CAPS = %+v", caps)
	}

	_ = sess.OnCustom("VS_PEERS", func(p any) { got = p.(aolib.Outgoing) })
	sess.Receive([]byte(`{"$header":"VS_PEERS","uids":[1,2,3]}`))
	peers, ok := got.(*VS_PEERS)
	if !ok {
		t.Fatalf("decoded %T, want *VS_PEERS", got)
	}
	if strings.Join(intsToStrs(peers.Uids), ",") != "1,2,3" {
		t.Errorf("VS_PEERS = %+v", peers)
	}
}

func intsToStrs(ns []int) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = Itoa(n)
	}
	return out
}
