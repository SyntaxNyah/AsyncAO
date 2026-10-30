package theme

// AO2 2.10+ courtroom_stylesheets.css is QSS; this parses the colour subset into a Palette.

import (
	"math"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
)

// Palette is the extracted colour scheme; a nil pointer keeps the UI's default.
type Palette struct {
	Text    *RGB // base text (QWidget/body color)
	Panel   *RGB // window/panel background
	PanelHi *RGB // button/raised background
	Accent  *RGB // borders / highlights
	Danger  *RGB // error text, when themes define one
}

func (p Palette) Empty() bool {
	return p.Text == nil && p.Panel == nil && p.PanelHi == nil &&
		p.Accent == nil && p.Danger == nil
}

type paletteSlot int

const (
	slotNone paletteSlot = iota
	slotText
	slotPanel
	slotPanelHi
	slotAccent
	slotDanger
)

// selectorSlots maps lowercased QSS selector, then property, to palette role.
var selectorSlots = map[string]map[string]paletteSlot{
	"*":           {"color": slotText, "background-color": slotPanel, "background": slotPanel},
	"qwidget":     {"color": slotText, "background-color": slotPanel, "background": slotPanel},
	"qmainwindow": {"color": slotText, "background-color": slotPanel, "background": slotPanel},
	"qpushbutton": {"background-color": slotPanelHi, "background": slotPanelHi, "color": slotText, "border-color": slotAccent},
	"aobutton":    {"background-color": slotPanelHi, "background": slotPanelHi, "border-color": slotAccent},
	// Inputs give the accent via borders; only QWidget-level selectors define the panel.
	"qlineedit":      {"background-color": slotPanelHi, "background": slotPanelHi, "border-color": slotAccent},
	"qplaintextedit": {"color": slotText},
	"qtextedit":      {"color": slotText},
	"qlistwidget":    {"color": slotText},
	"qtreewidget":    {"color": slotText},
	"qcheckbox":      {"color": slotText},
	"qlabel":         {"color": slotText},
	// Hover and checked states are the closest QSS has to an accent.
	"qpushbutton:hover":   {"background-color": slotAccent, "background": slotAccent},
	"qpushbutton:pressed": {"background-color": slotAccent, "background": slotAccent},
}

// ParseStylesheet extracts the palette; later declarations win, and unknown selectors and properties are ignored.
func ParseStylesheet(data []byte) Palette {
	var pal Palette
	assign := func(slot paletteSlot, c RGB) {
		v := c
		switch slot {
		case slotText:
			pal.Text = &v
		case slotPanel:
			pal.Panel = &v
		case slotPanelHi:
			pal.PanelHi = &v
		case slotAccent:
			pal.Accent = &v
		case slotDanger:
			pal.Danger = &v
		}
	}

	src := stripCSSComments(string(data))
	for {
		open := strings.IndexByte(src, '{')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(src[open:], '}')
		if closeIdx < 0 {
			break
		}
		selectors := src[:open]
		body := src[open+1 : open+closeIdx]
		src = src[open+closeIdx+1:]

		decls := parseDeclarations(body)
		if len(decls) == 0 {
			continue
		}
		for _, rawSel := range strings.Split(selectors, ",") {
			sel := strings.ToLower(strings.TrimSpace(rawSel))
			props, known := selectorSlots[sel]
			if !known {
				continue
			}
			for prop, val := range decls {
				slot, ok := props[prop]
				if !ok {
					continue
				}
				if c, ok := parseCSSColor(val); ok {
					assign(slot, c)
				}
			}
		}
	}
	return pal
}

func parseDeclarations(body string) map[string]string {
	out := map[string]string{}
	for _, decl := range strings.Split(body, ";") {
		prop, val, found := strings.Cut(decl, ":")
		if !found {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(prop))] = strings.TrimSpace(val)
	}
	return out
}

func stripCSSComments(s string) string {
	var b strings.Builder
	for {
		start := strings.Index(s, "/*")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		end := strings.Index(s[start+2:], "*/")
		if end < 0 {
			return b.String()
		}
		s = s[start+2+end+2:]
	}
}

const (
	cssShortHexLen = 3
	cssHexLen      = 6
	cssQtHexLen    = 8
)

// parseCSSColor handles #rgb, #rrggbb, #aarrggbb (Qt's ARGB, alpha ignored), rgb()/rgba() and a few named colours.
func parseCSSColor(v string) (RGB, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "#"):
		hex := v[1:]
		switch len(hex) {
		case cssShortHexLen: // #rgb: each digit doubled, so #abc is #aabbcc
			if c, ok := parse.HexColor(string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})); ok {
				return RGB{R: c.R, G: c.G, B: c.B}, true
			}
		case cssHexLen: // #rrggbb
			if c, ok := parse.HexColor(hex); ok {
				return RGB{R: c.R, G: c.G, B: c.B}, true
			}
		case cssQtHexLen: // #aarrggbb (Qt order) — alpha dropped
			if c, ok := parse.HexColor(hex[2:] + hex[:2]); ok { // aarrggbb → rrggbbaa
				return RGB{R: c.R, G: c.G, B: c.B}, true
			}
		}
	case strings.HasPrefix(v, "rgb(") || strings.HasPrefix(v, "rgba("):
		inner := v[strings.IndexByte(v, '(')+1:]
		if end := strings.IndexByte(inner, ')'); end >= 0 {
			inner = inner[:end]
		}
		parts := strings.Split(inner, ",")
		if len(parts) >= rgbFields {
			r := parse.AtoiOr(parts[0], 0)
			g := parse.AtoiOr(parts[1], 0)
			b := parse.AtoiOr(parts[2], 0)
			return RGB{R: uint8(r), G: uint8(g), B: uint8(b)}, true
		}
	}
	if named, ok := cssNamedColors[v]; ok {
		return named, true
	}
	return RGB{}, false
}

// cssNamedColors are those that appear in real AO themes.
var cssNamedColors = map[string]RGB{
	"white":       {R: math.MaxUint8, G: math.MaxUint8, B: math.MaxUint8},
	"black":       {},
	"red":         {R: math.MaxUint8},
	"green":       {G: cssHalfChannel},
	"blue":        {B: math.MaxUint8},
	"gray":        {R: cssHalfChannel, G: cssHalfChannel, B: cssHalfChannel},
	"grey":        {R: cssHalfChannel, G: cssHalfChannel, B: cssHalfChannel},
	"transparent": {},
}

const cssHalfChannel = 128
