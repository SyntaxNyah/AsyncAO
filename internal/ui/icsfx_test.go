package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The outgoing SFX_NAME rule (icsfx.go), against AO2-Client courtroom.cpp:2044-2116.
// The live report was "SFX: auto plays the emote sound on non-Pre messages"; the wire
// half of that is the first line AO2 writes and AsyncAO did not: f_sfx = "1".

func TestOutgoingSFXFollowsThePreCheckbox(t *testing.T) {
	emote := courtroom.Emote{Anim: "normal", Preanim: "point", SFXName: "deskslam"}
	picks := []string{sfxAutoLabel, "stab", "boom"}

	for _, tc := range []struct {
		name    string
		pre     bool
		pickIdx int
		want    string
		why     string
	}{
		{"auto + no pre", false, 0, icSilentSFX,
			"f_sfx stays at the silence sentinel when ui_pre is unchecked (courtroom.cpp:2045, :2095-2098)"},
		{"auto + pre", true, 0, "deskslam",
			"Pre ticked → f_sfx = get_char_sfx() → the emote's char.ini sound (courtroom.cpp:2078)"},
		{"explicit + pre", true, 1, "stab",
			"a dropdown row other than Default overrides (courtroom.cpp:2102-2104)"},
		{"explicit + no pre", false, 2, "boom",
			"the override is not gated on Pre — it is transmitted either way (courtroom.cpp:2102-2104)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outgoingSFXName(&emote, tc.pre, tc.pickIdx, picks); got != tc.want {
				t.Errorf("SFX_NAME = %q, want %q — %s", got, tc.want, tc.why)
			}
		})
	}

	// An emote with no sound of its own still ships the sentinel, never "" — an empty
	// SFX field is not the same wire value as "no sound" (get_sfx_name's empty default
	// is what :2045 already put there).
	silent := courtroom.Emote{Anim: "normal", Preanim: "point"}
	if got := outgoingSFXName(&silent, true, 0, picks); got != icSilentSFX {
		t.Errorf("soundless emote with Pre on = %q, want %q", got, icSilentSFX)
	}
	// An out-of-range pick index cannot smuggle a panic or an empty name.
	if got := outgoingSFXName(&emote, false, 99, picks); got != icSilentSFX {
		t.Errorf("out-of-range pick = %q, want %q", got, icSilentSFX)
	}
}

// TestSFXOnIdlePromotesTheEmoteModExtends the rule above for the preference the
// field-batch gate could only assume: AO2's sfx_on_idle / "Always Send SFX"
// (options.cpp:569-572, `config.value("sfx_on_idle", false)`).
//
// The gate above pins SFX_NAME, which sfx_on_idle does NOT touch. What the option
// changes is the emote MOD — courtroom.cpp:2107-2115 rewrites IDLE→PREANIM and
// ZOOM→PREANIM_ZOOM with an empty pre name and no delay, because AO plays a
// message's sound off the preanim stage and an idle-modded line is silent at every
// receiver whatever SFX_NAME it carries.
func TestSFXOnIdlePromotesTheEmoteMod(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mod, pickIdx int
		onIdle, zoom bool
		wantMod      int
		wantPromoted bool
		why          string
	}{
		{"off (the shipped default) changes nothing", protocol.EmoteModIdle, 1, false, true,
			protocol.EmoteModIdle, false, "AO2 ships sfx_on_idle false — a stock client transmits the pick and stays silent"},
		{"on + a picked sound promotes IDLE", protocol.EmoteModIdle, 1, true, true,
			protocol.EmoteModPreanim, true, "courtroom.cpp:2109-2110"},
		{"on but the Default row is picked", protocol.EmoteModIdle, 0, true, true,
			protocol.EmoteModIdle, false, ":2102 gates the whole rewrite on a dropdown index != 0"},
		{"on + ZOOM promotes to PREANIM_ZOOM", protocol.EmoteModZoom, 2, true, true,
			protocol.EmoteModPreanimZoom, true, "courtroom.cpp:2112-2113"},
		{"ZOOM without the prezoom feature is left alone", protocol.EmoteModZoom, 2, true, false,
			protocol.EmoteModZoom, false,
			"EmoteModPreanimZoom is 2.8; NormalizeOutgoingEmoteMod refuses to invent it, and routing around that guard makes a strict receiver drop the message"},
		{"a mod that already animates is untouched", protocol.EmoteModPreanim, 1, true, true,
			protocol.EmoteModPreanim, false, "only IDLE and ZOOM are rewritten (:2108)"},
	} {
		gotMod, gotPromoted := sfxIdlePromotion(tc.mod, tc.pickIdx, tc.onIdle, tc.zoom)
		if gotMod != tc.wantMod || gotPromoted != tc.wantPromoted {
			t.Errorf("%s: got (mod %d, promoted %v), want (mod %d, %v) — %s",
				tc.name, gotMod, gotPromoted, tc.wantMod, tc.wantPromoted, tc.why)
		}
	}
}

// TestTheICSendPathAppliesTheIdlePromotionWholesale is the encapsulation half of
// the option: the promotion moves THREE fields together (mod, pre name, delay) and
// a send path that took the mod without blanking the other two would transmit a
// preanim that does not exist — a missingno at every receiver.
func TestTheICSendPathAppliesTheIdlePromotionWholesale(t *testing.T) {
	body := funcBodySource(t, "screens.go", "sendIC")
	if !containsCall(body, "sfxIdlePromotion") {
		t.Fatal("the IC send path no longer calls sfxIdlePromotion — the sfx_on_idle preference does nothing")
	}
	for _, name := range []string{"preEmote", "sfxDelay"} {
		if !assignsIdent(body, name) {
			t.Errorf("sendIC never assigns %s — the promotion's three fields no longer move together", name)
		}
	}
}

// assignsIdent reports whether n contains an assignment to a plain identifier of
// this name.
func assignsIdent(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestTheICSendPathUsesTheSFXRule is the encapsulation half: the IC send builder must
// ASK outgoingSFXName rather than re-deriving the field. Re-deriving it inline is
// literally how the defect shipped, and a second copy would drift from the receive-side
// gate (courtroom.preanimSFXPlays) that now decides whether the value is ever heard.
func TestTheICSendPathUsesTheSFXRule(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "screens.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse screens.go: %v", err)
	}
	callsRule := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "outgoingSFXName" {
			callsRule = true
		}
		return true
	})
	if !callsRule {
		t.Error("the IC send path no longer calls outgoingSFXName — either it moved (point this test at it) " +
			"or the SFX_NAME decision was inlined again, which is exactly how the non-Pre sound shipped")
	}

	// And nothing in the package may spell the wire sentinel by hand: one constant, so
	// the send rule and any future reader agree on what "no sound" is.
	src, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go") && fi.Name() != "icsfx.go"
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for _, pkg := range src {
		for name, file := range pkg.Files {
			base := name
			if i := strings.LastIndexAny(base, `/\`); i >= 0 {
				base = base[i+1:]
			}
			ast.Inspect(file, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range assign.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || id.Name != "sfxName" || i >= len(assign.Rhs) {
						continue
					}
					if lit, ok := assign.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						t.Errorf("%s assigns sfxName from the literal %s — use icSilentSFX / outgoingSFXName", base, lit.Value)
					}
				}
				return true
			})
		}
	}
}
