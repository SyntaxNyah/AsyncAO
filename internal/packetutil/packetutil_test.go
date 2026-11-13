package packetutil

import (
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

func TestEncodeCustomUnknownHeader(t *testing.T) {
	if _, err := EncodeCustom("NOPE", struct{}{}, aolib.WireFanta); err == nil {
		t.Error("EncodeCustom for an unregistered header should error")
	}
}
