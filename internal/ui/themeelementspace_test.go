package ui

// THE TWO MOST-USED GEOMETRY PATHS IN THE SHIPPED CORPUS (ledger L11).
//
// Both were completely uncovered, and both fail in the same invisible way — the
// element still bakes, still paints, and is simply in the wrong place (or nowhere):
//
//   - THE ANCHOR RE-BASING (absSlotRect). courtroom_design.ini declares four
//     coordinate systems and only the courtroom-relative ones are made
//     window-absolute at rebuild; showname/message stay chatbox-relative,
//     music_name stays plate-relative, the two evidence icons stay
//     viewport-relative. Delete the re-basing and 20+ `anchor = showname` elements
//     across 10 of the 14 shipped themes jump to the WINDOW origin. No fixture
//     anchored a chatbox/viewport/music_display-relative key, so nothing failed.
//
//   - THE SPACE ORIGIN (elementSpaceRect's non-courtroom arms). Neuter them and 79
//     shipped elements go inert. Every fixture element in those spaces also
//     declared an anchor, so it took elementAnchorRect instead and the arms never
//     ran.
//
// The two fixture rows these drive (shownamepin, chatbacking) are in
// testdata/sidecars/w3_render.ini beside the elements they close the gap for.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The two fixture fills, which are how a baked row is identified (identity is the
// INDEX, so the fixtures give every element a unique colour — bakedByFill).
var (
	shownamePinFill = sdl.Color{R: 0x3b, G: 0xa7, B: 0xd8, A: 255}
	chatBackingFill = sdl.Color{R: 0x0d, G: 0x4f, B: 0x2a, A: 255}
)

// TestAnchorOnAChatboxRelativeKeyIsRebasedOntoItsParent is the anchor half.
//
// It measures against the CHATBOX's box plus the stored showname rect plus the
// authored delta — i.e. it states the whole re-basing chain rather than asserting
// "somewhere sensible". With the re-basing deleted the badge lands at
// (showname.X + delta), which on the stock layout is a few pixels from the window
// corner: visibly wrong, and previously invisible to the suite.
func TestAnchorOnAChatboxRelativeKeyIsRebasedOntoItsParent(t *testing.T) {
	_, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	chat, ok := lay.rect("ao2_chatbox")
	if !ok {
		t.Fatal("the fixture layout has no ao2_chatbox — the re-basing has no parent to measure against")
	}
	stored, ok := lay.rect("showname")
	if !ok {
		t.Fatal("the fixture layout has no showname — nothing to anchor to")
	}
	// The premise of the whole path: showname is stored RELATIVE to the chatbox, so
	// a raw read is not a window position. If this ever stops being true the test
	// below is measuring nothing, so it is asserted rather than assumed.
	if themeSlotRel("showname") != relChatbox {
		t.Fatalf("showname is no longer chatbox-relative (rel=%v) — this gate exists for that "+
			"coordinate system", themeSlotRel("showname"))
	}
	if chat.X == 0 && chat.Y == 0 {
		t.Fatal("the chatbox sits at the window origin in this fixture, so a missing re-base would " +
			"be indistinguishable from a correct one")
	}

	pin, _ := bakedByFill(lay, shownamePinFill)
	if pin == nil {
		t.Fatal("the showname-anchored element did not bake — an anchor onto a chatbox-relative key " +
			"resolved to nothing")
	}
	// rect = -4, -2, 8, 4 design px on the anchor's ABSOLUTE box.
	base := sdl.Rect{X: chat.X + stored.X, Y: chat.Y + stored.Y, W: stored.W, H: stored.H}
	want := sdl.Rect{
		X: base.X + int32(-4*lay.scaleX),
		Y: base.Y + int32(-2*lay.scaleY),
		W: base.W + int32(8*lay.scaleX),
		H: base.H + int32(4*lay.scaleY),
	}
	if pin.r != want {
		t.Fatalf("a showname-anchored badge baked at %+v, want %+v — showname is stored relative to "+
			"ao2_chatbox %+v, so an anchor onto it must be RE-BASED onto the chatbox's own box. "+
			"Without that step every `anchor = showname` decoration in the shipped corpus lands at "+
			"the window origin.", pin.r, want, chat)
	}
}

// TestUnanchoredElementResolvesAgainstItsSpaceOrigin is the space half: an element
// that declares `space = chatbox` and NO anchor is absolute inside the chatbox, and
// its origin is the chatbox's own box.
//
// The mutation it answers to: make elementSpaceRect's SpaceChatbox / SpaceViewport /
// SpaceMusicDisplay arms return false. The element then bakes nothing at all, which
// is exactly what 79 shipped elements would do.
func TestUnanchoredElementResolvesAgainstItsSpaceOrigin(t *testing.T) {
	_, lay, cleanup := stageThemedWithSidecar(t, "w3_render.ini")
	defer cleanup()

	chat, ok := lay.rect("ao2_chatbox")
	if !ok {
		t.Fatal("the fixture layout has no ao2_chatbox")
	}
	backing, _ := bakedByFill(lay, chatBackingFill)
	if backing == nil {
		t.Fatal("a `space = chatbox` element with no anchor did not bake — elementSpaceRect's " +
			"non-courtroom arms answer nothing, so every element authored in a space other than " +
			"courtroom is silently inert")
	}
	// rect = 4, 4, 40, 12: an ABSOLUTE rect inside the space, so the SIZE is the
	// authored size (scaled) and only the ORIGIN comes from the space.
	want := sdl.Rect{
		X: chat.X + int32(4*lay.scaleX),
		Y: chat.Y + int32(4*lay.scaleY),
		W: int32(40 * lay.scaleX),
		H: int32(12 * lay.scaleY),
	}
	if backing.r != want {
		t.Fatalf("an unanchored chatbox-space element baked at %+v, want %+v (chatbox %+v) — with no "+
			"anchor the rect is ABSOLUTE in its declared space, re-based onto that space's origin; "+
			"a size taken from the space would be the anchor rule applied to the wrong path",
			backing.r, want, chat)
	}
	// And the space's origin really is the chatbox, not the canvas — otherwise the
	// assertion above would hold for a fixture whose chatbox happens to sit at the
	// canvas origin.
	if origin, ok2 := (&App{}).elementSpaceRect(lay, theme.SpaceChatbox); !ok2 || origin != chat {
		t.Errorf("elementSpaceRect(chatbox) = %+v (ok=%v), want the chatbox's own box %+v",
			origin, ok2, chat)
	}
}
