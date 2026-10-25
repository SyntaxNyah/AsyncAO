package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// config.AssetCharCaseMax has to be spelled out as a literal, because
// internal/config sits BELOW internal/courtroom and cannot import the enum it
// bounds. internal/ui imports both, so the pin lives here.
//
// It matters because both ends CLAMP against it: the preference setter and the
// load overlay reject anything above it, so a max that lagged behind a new casing
// mode would make that mode unselectable and silently unloadable — while the
// Settings cycle button, which counts with courtroom.CharCaseCount, would happily
// offer it.
func TestAssetCharCaseMaxMatchesTheEnum(t *testing.T) {
	if want := uint8(courtroom.CharCaseCount - 1); config.AssetCharCaseMax != want {
		t.Errorf("config.AssetCharCaseMax = %d, want %d (courtroom.CharCaseCount-1) — "+
			"a new casing mode was added without widening the preference clamp",
			config.AssetCharCaseMax, want)
	}
}
