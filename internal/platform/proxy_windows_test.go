package platform

import "testing"

// blobWithFlags builds a minimal DefaultConnectionSettings blob with the given flags DWORD at
// pacFlagsOffset, so pacURLEnabled is exercised the way the registry value would drive it.
func blobWithFlags(flags byte) []byte {
	b := make([]byte, pacFlagsOffset+pacFlagBytes)
	b[pacFlagsOffset] = flags // little-endian low byte carries the proxy-type bits
	return b
}

func TestPACURLEnabled(t *testing.T) {
	cases := []struct {
		name string
		blob []byte
		want bool
	}{
		{"pac on", blobWithFlags(pacEnabledFlag), true},
		{"pac on with manual proxy", blobWithFlags(pacEnabledFlag | 0x02), true},
		{"pac off, direct", blobWithFlags(0x01), false},
		{"pac off, manual proxy only", blobWithFlags(0x02), false},
		{"empty", nil, false},
		{"short", make([]byte, pacFlagsOffset), false},
	}
	for _, tc := range cases {
		if got := pacURLEnabled(tc.blob); got != tc.want {
			t.Errorf("%s: pacURLEnabled = %v, want %v", tc.name, got, tc.want)
		}
	}
}
