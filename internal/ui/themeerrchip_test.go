package ui

// Gates for the theme-sidecar refusal surface (v1.90.0 W4).
//
// The defect these exist against is not a crash or a wrong pixel — it is SILENCE.
// theme.Theme.SidecarErr() shipped in W1 with no consumers, so a theme whose
// asyncao_theme.ini the reader refused applied anyway, rendered its AO2 half, and
// dropped every element, override, palette role and pane in the file with nothing
// anywhere to say so. A test that only checked "the theme still loads" would have
// passed throughout. These check that somebody is TOLD.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// overCapBase is a [theme] base value one rune past theme.NameRuneCap. It is the
// SHAPE of the real defect: the reader refuses the whole file on a named cap
// (theme.ErrSidecarCap, never a truncation — a truncated theme that then got saved
// would be data loss), so one long line costs a theme its entire AsyncAO tier.
//
// CHANGED FIXTURE (v1.90.0 W8, the metadata-cap ruling). It used to be `credit`,
// and `credit` no longer refuses: metadata degrades with a note, precisely so that
// a stranger's attribution line cannot cost them their whole theme (see
// internal/theme/metatext.go and THEME-FORMAT §7). The PROPERTY these gates pin —
// "a refused sidecar reaches a surface the user can see" — is unchanged, so the
// fixture moves to a [theme] key that is still structural: `base` names the
// terminal AO2 fallback FOLDER, and a shortened one resolves a different theme or
// none at all. Everything else about the file, and every assertion below, is
// untouched.
func overCapBase() string { return strings.Repeat("x", theme.NameRuneCap+1) }

// refusedSidecarSource is a minimal but otherwise perfectly good sidecar whose
// [theme] base trips the cap. Everything else in it is legal and would have
// rendered, which is exactly what makes the failure invisible.
func refusedSidecarSource() []byte {
	return []byte(fmt.Sprintf(`[theme]
name   = Refused
base   = %s

[element.plate]
kind = gradient
band = mid
rect = 10, 10, 40, 40
fill = #ff0000
`, overCapBase()))
}

// cleanSidecarSource is the same file with a base that fits.
func cleanSidecarSource() []byte {
	return []byte(`[theme]
name   = Clean
base   = default

[element.plate]
kind = gradient
band = mid
rect = 10, 10, 40, 40
fill = #ff0000
`)
}

// TestSidecarRefusalIsVisibleOnScreen is the ship-blocker gate: a refused sidecar
// must reach a surface the user can see, and a clean one must reach none.
//
// It drives the two halves the chip is built from — the strings the apply goroutine
// renders, and the chip's own active/rect decision — rather than a screenshot,
// because the property is "somebody is told", not "the pixels are here".
func TestSidecarRefusalIsVisibleOnScreen(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	// A clean apply says nothing at all.
	if a.themeErrChipActive() {
		t.Fatal("the chip is armed on a fixture with no theme error — it would cry wolf on every install")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, theme.SidecarFileName), refusedSidecarSource(), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, _, err := theme.LoadSidecarIn([]string{dir})
	if err == nil {
		t.Fatalf("the fixture sidecar parsed (sidecar %v) — a %d-rune free-text value must trip "+
			"theme.NameRuneCap, or this gate is measuring nothing", sc != nil, theme.NameRuneCap+1)
	}

	line := themeSidecarRefusalLine("refused", dir, err)
	tip := themeSidecarRefusalTip("refused", err)
	for what, s := range map[string]string{"debug line": line, "tooltip": tip} {
		if s == "" {
			t.Fatalf("the %s for a refused sidecar is empty — the refusal is invisible", what)
		}
		if !strings.Contains(s, theme.SidecarFileName) {
			t.Errorf("the %s never names %s, so the reader cannot tell WHICH file to fix: %q",
				what, theme.SidecarFileName, s)
		}
		if !strings.Contains(s, "refused") {
			t.Errorf("the %s never names the theme: %q", what, s)
		}
	}
	// A refusal must say what was LOST, not just that something failed: "the theme
	// applied but is not the theme" is the whole user-visible symptom.
	if !strings.Contains(strings.ToLower(line), "element") {
		t.Errorf("the debug line does not say what was skipped: %q", line)
	}
	// And it never fires on success.
	if got := themeSidecarRefusalLine("fine", dir, nil); got != "" {
		t.Errorf("a nil error produced a refusal line: %q", got)
	}
	if got := themeSidecarRefusalTip("fine", nil); got != "" {
		t.Errorf("a nil error produced a refusal tooltip: %q", got)
	}

	// The chip arms on the landed state, and a click acknowledges it.
	a.themeSidecarErr, a.themeSidecarTip = line, tip
	if !a.themeErrChipActive() {
		t.Fatal("with a refusal landed the chip is not active — nothing on screen reports it")
	}
	a.themeSidecarErrAck = true
	if a.themeErrChipActive() {
		t.Error("an acknowledged refusal keeps drawing — the chip cannot be dismissed")
	}
	// Fixing the file clears it outright, which is what makes the fix visible.
	a.themeSidecarErr, a.themeSidecarErrAck = "", false
	if a.themeErrChipActive() {
		t.Error("the chip stayed armed after the refusal cleared")
	}
}

// TestSidecarRefusalChipDoesNotOverlapItsNeighbours pins the band's geometry with
// three chips in it.
//
// Two right-anchored chips both measured from the window edge would have drawn on
// top of each other — the exact failure the band's "drop rather than overlap" rule
// exists to prevent, and one a single anchor cannot express. It also pins the
// no-error case as byte-identical: menuBarRightContentLeft must be the window width
// when nothing is right-anchored, or every install without a theme error pays a
// layout change for a feature it is not using.
func TestSidecarRefusalChipDoesNotOverlapItsNeighbours(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()
	a.drawCourtroom(1280, 720) // one frame so the menu bar's own latch is live

	const w = int32(1280)
	if got := a.menuBarRightContentLeft(w); got != w {
		t.Fatalf("menuBarRightContentLeft = %d with no theme error, want the window width %d — "+
			"the other two chips' rects must be unchanged for every install without one", got, w)
	}

	a.themeSidecarErr = "theme \"x\": refused"
	a.themeSidecarTip = "refused"
	errRect, ok := a.themeErrChipRect(w)
	if !ok {
		t.Skip("the menu bar is not painting in this fixture — the geometry cannot be measured here")
	}
	if right := errRect.X + errRect.W; right > w {
		t.Errorf("the refusal chip runs off the right edge: %+v in a %d-wide window", errRect, w)
	}
	if got := a.menuBarRightContentLeft(w); got != errRect.X {
		t.Errorf("menuBarRightContentLeft = %d, want the refusal chip's left edge %d", got, errRect.X)
	}
	// The missing-asset chip now sits INSIDE that, not on top of it.
	a.assetMissCount = 3
	a.assetMissAt = a.now()
	a.d.Prefs.SetAssetWarnings(true)
	if missRect, missOK := a.assetMissChipRect(w); missOK {
		if missRect.X+missRect.W > errRect.X {
			t.Errorf("the missing-asset chip %+v overlaps the refusal chip %+v — two right-anchored "+
				"chips measured from the same edge", missRect, errRect)
		}
	}
	// And the left-anchored Iniswap chip keeps clear of both, or drops.
	if iniRect, iniOK := a.iniswapChipRect(w); iniOK {
		if iniRect.X+iniRect.W > errRect.X {
			t.Errorf("the Iniswap chip %+v overlaps the refusal chip %+v — the reserve must come off "+
				"menuBarRightContentLeft, not off the window width", iniRect, errRect)
		}
	}
}

// TestThemeBadgeReflectsASidecarRefusal is the catalog half.
//
// A badge is a promise about what will happen when you pick the theme. "AO2 +
// native" on a folder whose native file the loader is going to refuse is that
// promise being broken silently — and a sidecar-ONLY folder in that state loads
// nothing whatsoever, which is precisely what the zero-value badge means.
func TestThemeBadgeReflectsASidecarRefusal(t *testing.T) {
	write := func(name string, sidecar []byte, ao2 bool) string {
		dir := filepath.Join(t.TempDir(), theme.ThemesDirName, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if ao2 {
			if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName), []byte("chatbox = 1, 2, 3, 4\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if sidecar != nil {
			if err := os.WriteFile(filepath.Join(dir, theme.SidecarFileName), sidecar, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	for _, tc := range []struct {
		name    string
		sidecar []byte
		ao2     bool
		want    themeKind
		wantErr bool
	}{
		{"clean_both", cleanSidecarSource(), true, themeKindBoth, false},
		{"clean_native", cleanSidecarSource(), false, themeKindNative, false},
		{"refused_both", refusedSidecarSource(), true, themeKindAO2, true},
		{"refused_native", refusedSidecarSource(), false, themeKindBroken, true},
		{"ao2_only", nil, true, themeKindAO2, false},
	} {
		dir := write(tc.name, tc.sidecar, tc.ao2)
		got, why := themeEntryKind(dir)
		if got != tc.want {
			t.Errorf("%s badges %q, want %q — the badge is a promise about what picking the theme does",
				tc.name, themeKindLabel(got), themeKindLabel(tc.want))
		}
		if (why != "") != tc.wantErr {
			t.Errorf("%s carries SidecarErr %q, wantErr=%v — the reason must travel with the badge or "+
				"the browser can only say that something is wrong", tc.name, why, tc.wantErr)
		}
		// themeKindOf itself is UNCHANGED: it is the subtheme filter, called up to
		// themeSubthemeScanCap times per theme, and folding a file read into it would
		// turn a 128-theme scan into thousands of parses.
		fileKind := themeKindOf(dir)
		if tc.sidecar != nil && fileKind != themeKindNative && fileKind != themeKindBoth {
			t.Errorf("%s: themeKindOf lost its file census (%q) — it must stay stat-only",
				tc.name, themeKindLabel(fileKind))
		}
	}

	// End to end through the scan, so the entry really carries it.
	root := filepath.Dir(filepath.Dir(write("scanned", refusedSidecarSource(), true)))
	cat := scanThemeCatalog(root, nil, false)
	if cat == nil {
		t.Fatal("scan returned no catalog")
	}
	e, ok := cat.Entry("scanned")
	if !ok {
		t.Fatal("the scanned theme is not in the catalog")
	}
	if e.SidecarErr == "" {
		t.Error("the catalog entry for a refused sidecar carries no reason")
	}
	if e.Kind != themeKindAO2 {
		t.Errorf("the scanned entry badges %q, want %q", themeKindLabel(e.Kind), themeKindLabel(themeKindAO2))
	}
}
