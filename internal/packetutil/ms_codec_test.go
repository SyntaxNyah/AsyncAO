package packetutil

import (
	"strings"
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// msTestFields is a full-featured outgoing MS (28 fields, all features on).
var msTestFields = []string{
	"1", "pre", "Phoenix", "normal", "Hello world", "def", "", "0", "4", "0",
	"0", "0", "0", "0", "0", "Phoenix", "-1", "0&0", "0", "0", "0", "", "", "", "0", "", "", "0",
}

func TestMSCodec(t *testing.T) {
	RegisterMSCodec()

	// Fanta stays positional and lossless (preserves the Fanta-only extensions).
	raw, err := BuildWire("MS", msTestFields, aolib.WireFanta)
	if err != nil {
		t.Fatalf("BuildWire(MS, Fanta): %v", err)
	}
	if string(raw) != "MS#"+strings.Join(EscapeAll(msTestFields), "#")+"#%" {
		t.Errorf("MS Fanta = %q", raw)
	}

	// JSON encodes through aolib's typed MSToServer (26 canonical fields).
	raw, err = BuildWire("MS", msTestFields, aolib.WireJSON)
	if err != nil {
		t.Fatalf("BuildWire(MS, JSON): %v", err)
	}
	v, err := aolib.Decode(raw, aolib.WireJSON)
	if err != nil {
		t.Fatalf("decode MS JSON %q: %v", raw, err)
	}
	ms, ok := v.(*aolib.MSToServer)
	if !ok {
		t.Fatalf("decoded %T, want *aolib.MSToServer", v)
	}
	if ms.CharID != 4 || ms.Character != "Phoenix" || ms.Message != "Hello world" {
		t.Errorf("MSToServer = %+v", ms)
	}

	// JSON inbound folds back to the positional form the switch consumes.
	d := NewDecoder()
	hdr, args, err := d.DecodeBody(raw)
	if err != nil {
		t.Fatalf("DecodeBody(MS JSON): %v", err)
	}
	if hdr != "MS" {
		t.Fatalf("header = %q", hdr)
	}
	if GetStr(args, 2) != "Phoenix" || GetStr(args, 8) != "4" || GetStr(args, 4) != "Hello world" {
		t.Errorf("folded MS = %v", args)
	}
}
