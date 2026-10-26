package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// packScene builds a one-line scene at origin whose speaker is char.
func packScene(origin, char string) *sceneRecording {
	return &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: "courtroom",
		Events: []recEvent{{
			Kind: int(courtroom.EventMessage),
			Message: &protocol.ChatMessage{
				CharName: char, Emote: "normal", Message: "hi",
				Side: "wit", EmoteMod: protocol.EmoteModIdle,
			},
		}},
	}
}

// TestExportPackConfirmFiresOnlyWhenThePackContributes pins the gate: exporting a
// bundle that carries the user's own art must ask first, and exporting one that
// does not must never interrupt them.
func TestExportPackConfirmFiresOnlyWhenThePackContributes(t *testing.T) {
	const origin = "http://h/base/"
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)

	// No mounts at all: nothing to warn about.
	if n := a.packContribution(packScene(origin, "witch")); n != 0 {
		t.Errorf("packContribution = %d with no mounts, want 0", n)
	}

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	a.d.Prefs.SetLocalAssets(false, []string{dir})
	a.setAssetSourceMode(assetSrcLayered, []string{dir})
	a.urls = courtroom.NewURLBuilder(origin)
	waitForIndex(t, a)

	// A scene using the packed character must be flagged...
	if n := a.packContribution(packScene(origin, "witch")); n == 0 {
		t.Error("packContribution = 0 for a scene whose sprite only exists in the pack")
	}
	// ...and one that doesn't touch it must not be.
	if n := a.packContribution(packScene(origin, "someone-else")); n != 0 {
		t.Errorf("packContribution = %d for a scene the pack does not cover, want 0", n)
	}
}

// TestExportPackConfirmBlocksUntilAnswered pins that the export does not start
// behind the user's back: asking must HOLD the export, and cancelling must not
// silently produce a server-only bundle (which would play wrong for a recipient).
func TestExportPackConfirmBlocksUntilAnswered(t *testing.T) {
	const origin = "http://h/base/"
	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")

	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	a.d.Prefs.SetLocalAssets(false, []string{dir})
	a.setAssetSourceMode(assetSrcLayered, []string{dir})
	a.urls = courtroom.NewURLBuilder(origin)
	waitForIndex(t, a)

	scene := packScene(origin, "witch")
	a.exportSceneArchive(scene, "scene")

	if a.makerExportPack == 0 {
		t.Fatal("the export did not ask before bundling the user's own art")
	}
	if a.makerExporting {
		t.Error("the export started while the confirmation was still open")
	}
	if a.makerExportScene == nil {
		t.Error("the pending scene was not held for the confirmation")
	}

	// Cancelling clears everything and grants no standing consent.
	a.makerExportPack, a.makerExportScene = 0, nil
	a.makerExportPackOK = false
	a.exportSceneArchive(scene, "scene")
	if a.makerExportPack == 0 {
		t.Error("a second export after cancelling did not ask again — consent must not persist")
	}
}
