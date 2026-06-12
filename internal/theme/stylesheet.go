package theme

// AO2 ≥ 2.10 themes ship courtroom_stylesheets.css — a Qt stylesheet
// (QSS) skinning every widget. Full QSS is a Qt rendering feature, but
// the properties theme authors actually use for the LOOK are a handful
// of colors. This parses that subset into a Palette the UI applies over
// its own color scheme, so "the css stuff" visibly works: panels, text,
// buttons, and accents take the theme's colors.

import (
	"strconv"
	"strings"
)

// Palette is the color scheme extracted from a theme stylesheet. Each
// pointer is nil when the stylesheet didn't define it — the UI keeps its
// default for those slots.
type Palette struct {
	Text    *RGB // base text (QWidget/body color)
	Panel   *RGB // window/panel background
	PanelHi *RGB // button/raised background
	Accent  *RGB // borders / highlights
	Danger  *RGB // error text, when themes define one
}

// Empty reports whether nothing usable was parsed.
func (p Palette) Empty() bool {
	return p.Text == nil && p.Panel == nil && p.PanelHi == nil &&
		p.Accent == nil && p.Danger == nil
}

// paletteSlot identifies where a parsed declaration lands.
type paletteSlot int

const (
	slotNone paletteSlot = iota
	slotText
	slotPanel
	slotPanelHi
	slotAccent
	slotDanger
)

// selectorSlots maps the QSS selectors theme authors use onto palette
// slots, per property. Lowercased selector → property → slot.
var selectorSlots = map[string]map[string]paletteSlot{
	// Base widget look: the whole-window colors.
	"*":           {"color": slotText, "background-color": slotPanel, "background": slotPanel},
	"qwidget":     {"color": slotText, "background-color": slotPanel, "background": slotPanel},
	"qmainwindow": {"color": slotText, "background-color": slotPanel, "background": slotPanel},
	// Buttons: raised surfaces.
	"qpushbutton": {"background-color": slotPanelHi, "background": slotPanelHi, "color": slotText, "border-color": slotAccent},
	"aobutton":    {"background-color": slotPanelHi, "background": slotPanelHi, "border-color": slotAccent},
	// Inputs give the accent via their borders. List/edit widget
	// backgrounds are REFINEMENTS of the window look, not the window look
	// — only QWidget-level selectors define the panel slot (a theme that
	// darkens its lists must not recolor every panel).
	"qlineedit":      {"background-color": slotPanelHi, "background": slotPanelHi, "border-color": slotAccent},
	"qplaintextedit": {"color": slotText},
	"qtextedit":      {"color": slotText},
	"qlistwidget":    {"color": slotText},
	"qtreewidget":    {"color": slotText},
	"qcheckbox":      {"color": slotText},
	"qlabel":         {"color": slotText},
	// Hover/checked states are the closest thing QSS has to an accent.
	"qpushbutton:hover":   {"background-color": slotAccent, "background": slotAccent},
	"qpushbutton:pressed": {"background-color": slotAccent, "background": slotAccent},
}

// ParseStylesheet extracts the supported palette from QSS content.
// Later declarations win, like CSS. Unknown selectors/properties are
// ignored wholesale — this is a palette extractor, not a CSS engine.
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

// parseDeclarations splits "prop: value; prop: value" into a map.
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

// stripCSSComments removes /* ... */ runs.
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

// parseCSSColor handles the forms theme authors use: #rgb, #rrggbb,
// #aarrggbb (Qt's ARGB — alpha ignored), rgb(...)/rgba(...), and a few
// named colors.
func parseCSSColor(v string) (RGB, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "#"):
		hex := v[1:]
		switch len(hex) {
		case 3: // #rgb
			r, okR := hexNibble(hex[0])
			g, okG := hexNibble(hex[1])
			b, okB := hexNibble(hex[2])
			if okR && okG && okB {
				return RGB{R: r * 17, G: g * 17, B: b * 17}, true
			}
		case 6: // #rrggbb
			if n, err := strconv.ParseUint(hex, 16, 32); err == nil {
				return RGB{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n)}, true
			}
		case 8: // #aarrggbb (Qt order) — alpha dropped
			if n, err := strconv.ParseUint(hex, 16, 64); err == nil {
				return RGB{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n)}, true
			}
		}
	case strings.HasPrefix(v, "rgb(") || strings.HasPrefix(v, "rgba("):
		inner := v[strings.IndexByte(v, '(')+1:]
		if end := strings.IndexByte(inner, ')'); end >= 0 {
			inner = inner[:end]
		}
		parts := strings.Split(inner, ",")
		if len(parts) >= 3 {
			r := atoiTrim(parts[0])
			g := atoiTrim(parts[1])
			b := atoiTrim(parts[2])
			return RGB{R: uint8(r), G: uint8(g), B: uint8(b)}, true
		}
	}
	if named, ok := cssNamedColors[v]; ok {
		return named, true
	}
	return RGB{}, false
}

func hexNibble(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// cssNamedColors: the handful that appear in real AO themes.
var cssNamedColors = map[string]RGB{
	"white":       {R: 255, G: 255, B: 255},
	"black":       {},
	"red":         {R: 255},
	"green":       {G: 128},
	"blue":        {B: 255},
	"gray":        {R: 128, G: 128, B: 128},
	"grey":        {R: 128, G: 128, B: 128},
	"transparent": {},
}
