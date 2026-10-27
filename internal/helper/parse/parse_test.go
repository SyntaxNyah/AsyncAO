package parse_test

import (
	"image/color"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
)

const (
	fallback = -7
	parsed   = 42
	negative = -3
)

func TestAtoiOr(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"42", parsed},
		{"  42\t", parsed},
		{"-3", negative},
		{"", fallback},
		{"   ", fallback},
		{"4 2", fallback},
		{"42px", fallback},
		{"8SS", fallback}, // Qt 6.5.3: QString("8SS").toInt() fails, AO2 reads 0
		{"0x2a", fallback},
		{"99999999999999999999999", fallback},
	}
	for _, tc := range cases {
		if got := parse.AtoiOr(tc.in, fallback); got != tc.want {
			t.Errorf("AtoiOr(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestHexColor(t *testing.T) {
	opaque := color.NRGBA{R: 0x1c, G: 0x2b, B: 0x3a, A: 0xff}
	cases := []struct {
		in   string
		want color.NRGBA
		ok   bool
	}{
		{"1c2b3a", opaque, true},
		{"#1C2B3A", opaque, true},
		{"  #1c2B3a ", opaque, true},
		{"1c2b3a80", color.NRGBA{R: 0x1c, G: 0x2b, B: 0x3a, A: 0x80}, true},
		{"#FFFFFF00", color.NRGBA{R: 0xff, G: 0xff, B: 0xff}, true},
		{"", color.NRGBA{}, false},
		{"#", color.NRGBA{}, false},
		{"fff", color.NRGBA{}, false},
		{"1c2b3", color.NRGBA{}, false},
		{"1c2b3a8", color.NRGBA{}, false},
		{"1c2b3a800", color.NRGBA{}, false},
		{"1c2g3a", color.NRGBA{}, false},
		{"##1c2b3a", color.NRGBA{}, false},
		{"+1c2b3a", color.NRGBA{}, false},
	}
	for _, tc := range cases {
		got, ok := parse.HexColor(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("HexColor(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
