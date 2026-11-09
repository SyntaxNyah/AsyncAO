package ui

// Rounded rectangles and line icons for chrome that needs more than axis-aligned fills. Each shape is rasterised once
// in Go (golang.org/x/image/vector) as a white, anti-aliased mask, uploaded as a texture, and tinted per draw with the
// texture's colour and alpha mods, so drawing one costs a few copies and never allocates.

import (
	"image"
	"math"
	"unsafe"

	"golang.org/x/image/vector"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// shapeDiscPx is the disc mask's size; corners are drawn from its quadrants, so it is the sharpest radius drawn.
	shapeDiscPx = 128
	// shapeIconPx is an icon mask's size; icons are drawn far smaller and scaled down with linear filtering.
	shapeIconPx = 96
	// shapeStroke is an icon's line width in its own viewBox units, the mockup's 1.5.
	shapeStroke = 1.5
	// shapeRoundSteps is how many segments flatten a circle or curve.
	shapeRoundSteps = 24
	// shapeDiscSteps flattens the disc mask, which is drawn larger than the icons.
	shapeDiscSteps     = 96
	shapeBytesPerPixel = 4
	shapeOpaque        = 255
)

// shapePart is one run of points in an icon's viewBox: a line, or a closed outline, stroked, or filled.
type shapePart struct {
	pts    []vec2
	closed bool
	fill   bool
}

type vec2 struct{ x, y float64 }

// shapeIcon is an icon drawn in a viewBox vb units square.
type shapeIcon struct {
	vb    float64
	parts []shapePart
}

// Icon names for Ctx.Icon.
const (
	iconShapeFolder = iota
	iconShapeStar
	iconShapeShield
	iconShapeScales
	iconShapePerson
	iconShapeHeart
	iconShapeGrid
	iconShapeList
	iconShapePencil
	iconShapeCross
	iconShapePlus
	iconShapeCheck
	iconShapeSwap
	iconShapeSearch
	iconShapeCount
)

// shapeIcons are the mockup's SVG icons as outlines. Curves are flattened when the mask is built.
var shapeIcons = [iconShapeCount]shapeIcon{
	iconShapeFolder: {16, []shapePart{{closed: true, pts: []vec2{{1.5, 4.5}, {2.5, 3.5}, {6.1, 3.5}, {7.5, 5.1}, {13.5, 5.1}, {14.5, 6.1}, {14.5, 12.5}, {13.5, 13.5}, {2.5, 13.5}, {1.5, 12.5}}}}},
	iconShapeStar:   {16, []shapePart{{closed: true, pts: []vec2{{8, 1.8}, {9.9, 5.8}, {14.2, 6.2}, {11, 9.1}, {11.9, 13.4}, {8, 11.2}, {4.1, 13.4}, {5, 9.1}, {1.8, 6.2}, {6.1, 5.8}}}}},
	iconShapeShield: {16, []shapePart{{closed: true, pts: concatPts(
		[]vec2{{8, 1.8}, {13, 3.6}, {13, 7.6}},
		cubicPts(vec2{13, 7.6}, vec2{13, 10.8}, vec2{10.8, 13}, vec2{8, 14.2}),
		cubicPts(vec2{8, 14.2}, vec2{5.2, 13}, vec2{3, 10.8}, vec2{3, 7.6}),
		[]vec2{{3, 3.6}},
	)}}},
	iconShapeScales: {16, []shapePart{
		{pts: []vec2{{8, 2.5}, {8, 13.5}}},
		{pts: []vec2{{5, 13.5}, {11, 13.5}}},
		{pts: []vec2{{3, 4.5}, {13, 4.5}}},
		{closed: true, pts: []vec2{{3, 4.5}, {1.3, 8.5}, {4.7, 8.5}}},
		{closed: true, pts: []vec2{{13, 4.5}, {11.3, 8.5}, {14.7, 8.5}}},
	}},
	iconShapePerson: {16, []shapePart{
		{closed: true, pts: circlePts(vec2{8, 5}, 2.6)},
		{pts: concatPts(cubicPts(vec2{3, 14}, vec2{3.6, 11.2}, vec2{5.6, 9.8}, vec2{8, 9.8}), cubicPts(vec2{8, 9.8}, vec2{10.4, 9.8}, vec2{12.4, 11.2}, vec2{13, 14}))},
	}},
	iconShapeHeart: {16, []shapePart{{closed: true, pts: concatPts(
		cubicPts(vec2{8, 13.6}, vec2{8, 13.6}, vec2{2, 10.2}, vec2{2, 6.1}),
		arcPts(vec2{5, 5.35}, 3.09, math.Pi+0.24, 2*math.Pi-0.24),
		arcPts(vec2{11, 5.35}, 3.09, math.Pi+0.24, 2*math.Pi-0.24),
		cubicPts(vec2{14, 6.1}, vec2{14, 10.2}, vec2{8, 13.6}, vec2{8, 13.6}),
	)}}},
	iconShapeGrid: {18, []shapePart{
		{closed: true, pts: []vec2{{2, 2}, {7.5, 2}, {7.5, 7.5}, {2, 7.5}}},
		{closed: true, pts: []vec2{{10.5, 2}, {16, 2}, {16, 7.5}, {10.5, 7.5}}},
		{closed: true, pts: []vec2{{2, 10.5}, {7.5, 10.5}, {7.5, 16}, {2, 16}}},
		{closed: true, pts: []vec2{{10.5, 10.5}, {16, 10.5}, {16, 16}, {10.5, 16}}},
	}},
	iconShapeList: {18, []shapePart{
		{pts: []vec2{{6, 4}, {16, 4}}}, {pts: []vec2{{6, 9}, {16, 9}}}, {pts: []vec2{{6, 14}, {16, 14}}},
		{fill: true, pts: circlePts(vec2{2.6, 4}, 1.1)}, {fill: true, pts: circlePts(vec2{2.6, 9}, 1.1)}, {fill: true, pts: circlePts(vec2{2.6, 14}, 1.1)},
	}},
	iconShapePencil: {12, []shapePart{{closed: true, pts: []vec2{{2, 10}, {2.6, 7.6}, {8, 2.2}, {9.8, 4}, {4.4, 9.4}}}}},
	iconShapeCross:  {12, []shapePart{{pts: []vec2{{2.5, 2.5}, {9.5, 9.5}}}, {pts: []vec2{{9.5, 2.5}, {2.5, 9.5}}}}},
	iconShapePlus:   {14, []shapePart{{pts: []vec2{{7, 2}, {7, 12}}}, {pts: []vec2{{2, 7}, {12, 7}}}}},
	iconShapeCheck:  {10, []shapePart{{pts: []vec2{{2, 5.2}, {4, 7.2}, {8, 2.8}}}}},
	iconShapeSwap:   {10, []shapePart{{pts: []vec2{{1.5, 3.5}, {8, 3.5}, {6.3, 1.8}}}, {pts: []vec2{{8.5, 6.5}, {2, 6.5}, {3.7, 8.2}}}}},
	iconShapeSearch: {14, []shapePart{{closed: true, pts: circlePts(vec2{6, 6}, 4.2)}, {pts: []vec2{{9.2, 9.2}, {12.5, 12.5}}}}},
}

func concatPts(runs ...[]vec2) []vec2 {
	var out []vec2
	for _, r := range runs {
		out = append(out, r...)
	}
	return out
}

func cubicPts(p0, p1, p2, p3 vec2) []vec2 {
	out := make([]vec2, 0, shapeRoundSteps+1)
	for i := 0; i <= shapeRoundSteps; i++ {
		t := float64(i) / shapeRoundSteps
		u := 1 - t
		a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		out = append(out, vec2{a*p0.x + b*p1.x + c*p2.x + d*p3.x, a*p0.y + b*p1.y + c*p2.y + d*p3.y})
	}
	return out
}

// arcPts walks a circle from angle from to angle to (radians, y down) in shapeRoundSteps steps.
func arcPts(center vec2, r, from, to float64) []vec2 {
	out := make([]vec2, 0, shapeRoundSteps+1)
	for i := 0; i <= shapeRoundSteps; i++ {
		t := from + (to-from)*float64(i)/shapeRoundSteps
		out = append(out, vec2{center.x + r*math.Cos(t), center.y + r*math.Sin(t)})
	}
	return out
}

func circlePts(center vec2, r float64) []vec2 {
	return arcPts(center, r, 0, 2*math.Pi)[:shapeRoundSteps]
}

// polygon adds a closed outline to z in a fixed winding, so overlapping pieces add up instead of cancelling.
func polygon(z *vector.Rasterizer, pts []vec2) {
	if len(pts) < 3 {
		return
	}
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i].x*pts[j].y - pts[j].x*pts[i].y
	}
	order := func(i int) vec2 { return pts[i] }
	if area < 0 {
		order = func(i int) vec2 { return pts[len(pts)-1-i] }
	}
	p := order(0)
	z.MoveTo(float32(p.x), float32(p.y))
	for i := 1; i < len(pts); i++ {
		p = order(i)
		z.LineTo(float32(p.x), float32(p.y))
	}
	z.ClosePath()
}

// stroke adds a line of width w through pts: a quad per segment and a round joint at every point.
func stroke(z *vector.Rasterizer, pts []vec2, closed bool, w float64) {
	half := w / 2
	n := len(pts)
	segs := n - 1
	if closed {
		segs = n
	}
	for i := 0; i < segs; i++ {
		a, b := pts[i], pts[(i+1)%n]
		dx, dy := b.x-a.x, b.y-a.y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*half, dx/l*half
		polygon(z, []vec2{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}})
	}
	for _, p := range pts {
		polygon(z, circlePts(p, half))
	}
}

// maskTexture uploads z's coverage as a white texture whose alpha is the mask, with linear filtering.
func (c *Ctx) maskTexture(z *vector.Rasterizer, size int) *sdl.Texture {
	alpha := image.NewAlpha(image.Rect(0, 0, size, size))
	z.Draw(alpha, alpha.Bounds(), image.Opaque, image.Point{})
	pix := make([]byte, size*size*shapeBytesPerPixel)
	for i, a := range alpha.Pix {
		// ABGR8888 is R, G, B, A in memory: white, with the mask as alpha.
		o := i * shapeBytesPerPixel
		pix[o], pix[o+1], pix[o+2], pix[o+3] = shapeOpaque, shapeOpaque, shapeOpaque, a
	}
	tex, err := c.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, int32(size), int32(size))
	if err != nil {
		return nil
	}
	if tex.Update(nil, unsafe.Pointer(&pix[0]), size*shapeBytesPerPixel) != nil {
		_ = tex.Destroy()
		return nil
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	_ = tex.SetScaleMode(sdl.ScaleModeLinear)
	return tex
}

// shapeDisc is the disc mask, built on first use.
func (c *Ctx) shapeDisc() *sdl.Texture {
	if c.discTex == nil && c.Ren != nil {
		z := vector.NewRasterizer(shapeDiscPx, shapeDiscPx)
		pts := make([]vec2, shapeDiscSteps)
		for i := range pts {
			t := 2 * math.Pi * float64(i) / shapeDiscSteps
			pts[i] = vec2{shapeDiscPx/2 + shapeDiscPx/2*math.Cos(t), shapeDiscPx/2 + shapeDiscPx/2*math.Sin(t)}
		}
		polygon(z, pts)
		c.discTex = c.maskTexture(z, shapeDiscPx)
	}
	return c.discTex
}

// shapeIconTex is icon k's mask, built on first use.
func (c *Ctx) shapeIconTex(k int) *sdl.Texture {
	if k < 0 || k >= iconShapeCount || c.Ren == nil {
		return nil
	}
	if c.iconTex[k] == nil {
		ic := shapeIcons[k]
		scale := shapeIconPx / ic.vb
		z := vector.NewRasterizer(shapeIconPx, shapeIconPx)
		for _, part := range ic.parts {
			pts := make([]vec2, len(part.pts))
			for i, p := range part.pts {
				pts[i] = vec2{p.x * scale, p.y * scale}
			}
			if part.fill {
				polygon(z, pts)
			} else {
				stroke(z, pts, part.closed, shapeStroke*scale)
			}
		}
		c.iconTex[k] = c.maskTexture(z, shapeIconPx)
	}
	return c.iconTex[k]
}

// tinted copies tex (or its src part, when part) into dst, coloured col. The scratch rects keep the cgo pointers off
// the heap.
func (c *Ctx) tinted(tex *sdl.Texture, src sdl.Rect, part bool, dst sdl.Rect, col sdl.Color) {
	if tex == nil || dst.W <= 0 || dst.H <= 0 {
		return
	}
	_ = tex.SetColorMod(col.R, col.G, col.B)
	_ = tex.SetAlphaMod(col.A)
	c.cgoRect = dst
	if !part {
		_ = c.Ren.Copy(tex, nil, &c.cgoRect)
		return
	}
	c.cgoSrc = src
	_ = c.Ren.Copy(tex, &c.cgoSrc, &c.cgoRect)
}

// Icon draws line icon k in r, coloured col.
func (c *Ctx) Icon(k int, r sdl.Rect, col sdl.Color) {
	c.tinted(c.shapeIconTex(k), sdl.Rect{}, false, r, col)
}

// FillRound fills r with corners of radius rad; a radius over half the short side makes a pill.
func (c *Ctx) FillRound(r sdl.Rect, rad int32, col sdl.Color) {
	rad = min(rad, r.W/2, r.H/2)
	disc := c.shapeDisc()
	if rad <= 0 || disc == nil {
		c.Fill(r, col)
		return
	}
	const q = shapeDiscPx / 2
	c.tinted(disc, sdl.Rect{X: 0, Y: 0, W: q, H: q}, true, sdl.Rect{X: r.X, Y: r.Y, W: rad, H: rad}, col)
	c.tinted(disc, sdl.Rect{X: q, Y: 0, W: q, H: q}, true, sdl.Rect{X: r.X + r.W - rad, Y: r.Y, W: rad, H: rad}, col)
	c.tinted(disc, sdl.Rect{X: 0, Y: q, W: q, H: q}, true, sdl.Rect{X: r.X, Y: r.Y + r.H - rad, W: rad, H: rad}, col)
	c.tinted(disc, sdl.Rect{X: q, Y: q, W: q, H: q}, true, sdl.Rect{X: r.X + r.W - rad, Y: r.Y + r.H - rad, W: rad, H: rad}, col)
	c.Fill(sdl.Rect{X: r.X + rad, Y: r.Y, W: r.W - 2*rad, H: rad}, col)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y + rad, W: r.W, H: r.H - 2*rad}, col)
	c.Fill(sdl.Rect{X: r.X + rad, Y: r.Y + r.H - rad, W: r.W - 2*rad, H: rad}, col)
}

// RoundRect fills r rounded with an outline thick wide in border; a zero-alpha border draws none. The fill is drawn
// over the outline, so it must be opaque.
func (c *Ctx) RoundRect(r sdl.Rect, rad, thick int32, fill, border sdl.Color) {
	if border.A == 0 || thick <= 0 {
		c.FillRound(r, rad, fill)
		return
	}
	c.FillRound(r, rad, border)
	c.FillRound(sdl.Rect{X: r.X + thick, Y: r.Y + thick, W: r.W - 2*thick, H: r.H - 2*thick}, max(rad-thick, 0), fill)
}
