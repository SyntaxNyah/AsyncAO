package ui

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// elemBakeState is one of App's banner groups lifted into a named component. The fields
// used to sit flat on App under a banner comment, where every ui file could read
// them at depth 1; they are reached as a.elemBake.<field> now.
type elemBakeState struct {
	//
	// themeSidecar is the applied theme's parsed asyncao_theme.ini, or nil for a
	// stock AO2 theme — which is every theme that has never been opened in the
	// editor, so nil is the ordinary case and every read of it is nil-safe. Loaded
	// on the theme-apply goroutine (theme.Load), landed here by pollThemeApply, and
	// read ONLY by the cold bake in themeLayoutIn.
	themeSidecar *theme.Sidecar
	themeElemGen uint64
	elemBakeBand [theme.ElementCap]theme.ElementBand
	elemBakeZ    [theme.ElementCap]int16
	condSrc      [theme.ConditionAxisCount]string
	condLive     [theme.ConditionAxisCount]int16
	condGen      uint64
	condSpeaking bool
}

// themeFXState is one of App's banner groups lifted into a named component. The fields
// used to sit flat on App under a banner comment, where every ui file could read
// them at depth 1; they are reached as a.fxPool.<field> now.
type themeFXState struct {
	//
	// themeClocks is the FIXED authoring-clock pool. Clock 0 is the shared themeAt
	// anchor, so the ZERO VALUE of this array is "every element runs exactly as
	// themeFrame always has" — which is why nothing has to initialise it and a stock
	// AO2 theme costs nothing for it. Re-loaded beside a.themeAt in pollThemeApply.
	themeClocks        [theme.ClockCap]themeClock
	themeFX            [themeFXCap]fxState
	themeFXFrame       uint64
	themeFXOver        bool
	themeFXFrozen      bool
	themeSidecarErr    string
	themeSidecarTip    string
	themeSidecarErrAck bool
}
