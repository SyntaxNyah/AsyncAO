package courtroom

import "testing"

// TestParseScalingModeMatchesAO2 pins the char.ini spellings AO2 accepts,
// including "fast" — which looks like a typo for pixel but is AO2's own Qt-side
// alias (Qt::FastTransformation) and appears in shipped char.inis. Dropping it
// would silently smooth exactly the characters that asked hardest not to be.
func TestParseScalingModeMatchesAO2(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want ScalingMode
	}{
		{"pixel", ScalingPixel},
		{"fast", ScalingPixel}, // get_scaling treats it as pixel
		{"smooth", ScalingSmooth},
		{"Pixel", ScalingPixel}, // char.inis are hand-edited
		{" smooth ", ScalingSmooth},
		{"", ScalingAuto},        // absent key
		{"nearest", ScalingAuto}, // NOT a spelling AO2 knows: fall through, don't guess
		{"linear", ScalingAuto},
	} {
		if got := ParseScalingMode(tc.raw); got != tc.want {
			t.Errorf("ParseScalingMode(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestUserModeOverridesCharINI is the precedence half, and it is the easy one to
// get backwards. In AO2 the char.ini key is consulted ONLY while the user sits on
// Auto (get_scaling reads Options::resizeMode first and returns it untouched when
// it is not AUTO). Inverting this would let any downloaded character force a
// filter on a user who explicitly picked the other one.
func TestUserModeOverridesCharINI(t *testing.T) {
	for _, tc := range []struct {
		name       string
		user, char ScalingMode
		want       ScalingMode
	}{
		{"user smooth beats char pixel", ScalingSmooth, ScalingPixel, ScalingSmooth},
		{"user pixel beats char smooth", ScalingPixel, ScalingSmooth, ScalingPixel},
		{"auto user defers to char", ScalingAuto, ScalingPixel, ScalingPixel},
		{"auto user defers to char (smooth)", ScalingAuto, ScalingSmooth, ScalingSmooth},
		{"neither decides", ScalingAuto, ScalingAuto, ScalingAuto},
	} {
		if got := ResolveScalingMode(tc.user, tc.char); got != tc.want {
			t.Errorf("%s: ResolveScalingMode(%d, %d) = %d, want %d", tc.name, tc.user, tc.char, got, tc.want)
		}
	}
}

// TestCharINIScalingRoundTrip proves the key is actually read out of [Options] —
// the struct field and the parser can both be right while nothing wires them.
func TestCharINIScalingRoundTrip(t *testing.T) {
	ini, err := ParseCharINI([]byte("[Options]\nshowname = Furio\nscaling = pixel\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ini.Scaling != "pixel" {
		t.Errorf("raw [Options] scaling not captured: got %q", ini.Scaling)
	}
	if got := ini.ScalingMode(); got != ScalingPixel {
		t.Errorf("ScalingMode() = %d, want ScalingPixel", got)
	}
	// A char.ini with no scaling key is the overwhelmingly common case.
	plain, err := ParseCharINI([]byte("[Options]\nshowname = Phoenix\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := plain.ScalingMode(); got != ScalingAuto {
		t.Errorf("a char.ini with no scaling key must be Auto, got %d", got)
	}
	if got := (*CharINI)(nil).ScalingMode(); got != ScalingAuto {
		t.Errorf("nil char.ini must be Auto, got %d", got) // the pre-char.ini frames
	}
}
