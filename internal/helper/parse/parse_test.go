package parse_test

import (
	"image/color"
	"math"
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

func TestUint8(t *testing.T) {
	const (
		lo, hi    = 10, 200
		inRange   = 42
		wraps     = "300" // narrowed without a check, 300 would read as 44
		overflows = "99999999999999999999999"
	)
	cases := []struct {
		in     string
		want   uint8
		wantOK bool
	}{
		{" 42 ", inRange, true},
		{"5", lo, true},
		{wraps, hi, true},
		{"-1", lo, true},
		{"x", lo, false},
		{overflows, lo, false},
	}
	for _, tc := range cases {
		if got, ok := parse.Uint8(tc.in, lo, hi); got != tc.want || ok != tc.wantOK {
			t.Errorf("Uint8(%q) = %d, %v, want %d, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestInt16(t *testing.T) {
	const (
		lo, hi  = -100, 100
		inRange = -42
	)
	cases := []struct {
		in     string
		want   int16
		wantOK bool
	}{
		{"-42", inRange, true},
		{"40000", hi, true},
		{"-40000", lo, true},
		{"", 0, false},
	}
	for _, tc := range cases {
		if got, ok := parse.Int16(tc.in, lo, hi); got != tc.want || ok != tc.wantOK {
			t.Errorf("Int16(%q) = %d, %v, want %d, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestInt32(t *testing.T) {
	const (
		fullLo, fullHi = math.MinInt32, math.MaxInt32
		inRange        = 123456
	)
	cases := []struct {
		in     string
		want   int32
		wantOK bool
	}{
		{"123456", inRange, true},
		{"3000000000", fullHi, true},
		{"-3000000000", fullLo, true},
		{"1e3", 0, false},
	}
	for _, tc := range cases {
		if got, ok := parse.Int32(tc.in, fullLo, fullHi); got != tc.want || ok != tc.wantOK {
			t.Errorf("Int32(%q) = %d, %v, want %d, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
