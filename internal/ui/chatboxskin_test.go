package ui

import (
	"go/ast"
	"testing"
)

// mentionsField reports whether n reads a selector `.field` anywhere inside it —
// the same shape srcgate_test.go's readsFieldOf checks for, but without requiring
// the receiver to be a helper call (here it is a plain `sc`).
func mentionsField(n ast.Node, field string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
			found = true
		}
		return !found
	})
	return found
}

// mentionsIdentNode is the ast.Node form of srcgate_test.go's mentionsIdent (which
// is typed ast.Expr): it reports whether an identifier named name appears anywhere
// inside n, whatever node shape n is.
func mentionsIdentNode(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// TestClassicChatboxThemeInkFollowsAnySkin pins the per-character-chatbox colour
// fix at the CALL SITE. drawChatOverlay used to gate theme message/showname ink on
// a theme-skin-only flag, so a speaker's per-character chatbox (char.ini chat=)
// drew the default white text instead of the theme's declared ink — white-on-white
// on a light skin like the "Endless Monday" box. The fix makes the ink follow
// `skinned`, which is true whenever ANY skin (theme OR per-character) drew; only
// the flat fallback panel keeps the client default. This is a source gate because
// the bug lives in which local the draw site passes, not in the pure colour
// resolver (chatBaseColor) it calls.
func TestClassicChatboxThemeInkFollowsAnySkin(t *testing.T) {
	body := funcBodySource(t, "screens.go", "drawChatOverlay")

	// Any theme-skin-only flag surviving here is the bug: per-character skins would
	// skip the theme ink and fall back to the palette default.
	if mentionsIdentNode(body, "themeSkinned") {
		t.Error("drawChatOverlay still computes a theme-skin-only flag — theme ink would be skipped for per-character skins")
	}

	for _, call := range callsNamed(body, "ensureChatRaster") {
		if len(call.Args) < 2 || !mentionsIdent(call.Args[1], "skinned") {
			t.Errorf("drawChatOverlay feeds ensureChatRaster a skin flag that is not the any-skin `skinned` local — a per-character skin would keep the default white text")
		}
	}

	// The raster-failed fallback label must agree, or a message that failed to
	// rasterize would still ink white on a per-character skin.
	for _, call := range callsNamed(body, "chatBaseColor") {
		if len(call.Args) < 3 || !mentionsIdent(call.Args[2], "skinned") {
			t.Errorf("drawChatOverlay's chatBaseColor fallback must take the any-skin `skinned` flag")
		}
	}

	// The showname ink must follow the same rule: gated on skinned && themeHasName.
	nameGated := false
	ast.Inspect(body, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || be.Op.String() != "&&" {
			return true
		}
		if mentionsIdent(be, "skinned") && mentionsField(be, "themeHasName") {
			nameGated = true
		}
		return true
	})
	if !nameGated {
		t.Error("drawChatOverlay must gate the showname ink on the any-skin `skinned` local (skinned && a.themeHasName)")
	}
}

// TestThemedChatboxDrawsPerCharacterSkins pins AO2's get_chat priority in the
// themed layout: a speaker's char.ini chat= box must win over the theme's own skin
// in BOTH the live themed chatbox and its video-export mirror. The classic overlay
// already drew per-character skins; the themed path used to ignore them entirely.
func TestThemedChatboxDrawsPerCharacterSkins(t *testing.T) {
	for _, site := range []struct{ file, fn string }{
		{"theme_layout.go", "drawThemedChatBox"},
		{"gifexport.go", "drawGifThemedChatbox"},
	} {
		body := funcBodySource(t, site.file, site.fn)
		if !mentionsField(body, "ChatSkinBase") {
			t.Errorf("%s must read sc.ChatSkinBase so a speaker's own chatbox draws (AO2 get_chat priority)", site.fn)
		}
	}
}
