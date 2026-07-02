package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestStageFrameKindMaxMatchesUI pins the cross-package contract for #56: config
// hard-codes the highest valid stage-frame index (it can't import ui), so this
// proves it still equals len(stageFrameNames)-1 — the Settings dropdown can reach
// every style and a hand-edited pref can't select a non-existent one.
func TestStageFrameKindMaxMatchesUI(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	last := len(stageFrameNames) - 1 // Shadow
	prefs.SetStageFrame(last)
	if prefs.StageFrame() != last {
		t.Fatalf("the last stage frame (%d) didn't round-trip (got %d) — config's stageFrameKindMax is out of sync with stageFrameNames", last, prefs.StageFrame())
	}
	prefs.SetStageFrame(last + 1) // one past the last valid → must clamp to the last
	if prefs.StageFrame() != last {
		t.Errorf("an out-of-range stage frame didn't clamp to %d (got %d)", last, prefs.StageFrame())
	}
	prefs.SetStageFrame(0) // and Off round-trips
	if prefs.StageFrame() != stageFrameOff {
		t.Errorf("Off didn't round-trip (got %d)", prefs.StageFrame())
	}
}
