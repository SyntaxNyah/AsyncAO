package ui

// AsyncAO fork addition: Teleport (TP) re-staging.
//
// TP never re-implements rendering. The snapshot is fed back through the paths
// that already exist: the room's own background/music event handlers, and
// Courtroom.RestoreMessage for each IC line. RestoreMessage is already a
// SETTLED replay with sound and flashes suppressed, which is exactly what a
// rewind needs.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// applyTeleport re-stages the active room from the latest snapshot.
//
// TP has two modes. "authoritative" means the server rolled the room back, so
// this tab matches what every peer sees. "overlay" means the snapshot went only
// to TP-capable peers, so this tab deliberately shows an older scene than a
// non-TP peer would — the divergence is the feature, not a bug.
//
// Deferred for the POC: evidence and testimony are not re-applied. The snapshot
// carries them (TPArea.Evidence/Testimony), so wiring them is a follow-up, not a
// protocol change.
func (a *App) applyTeleport() {
	tp := a.sess.Teleport
	if tp == nil || a.room == nil {
		return
	}

	// Background first: a wipe re-stages the viewport, so everything after this
	// lands on a clean stage. The side travels with it, mirroring the server's
	// BN ordering (set_side runs before set_background upstream).
	a.room.HandleEvent(courtroom.Event{
		Kind: courtroom.EventBackground,
		Text: tp.Area.Background,
		Name: tp.Area.Side,
		Wipe: tp.Area.Background != "",
	})

	if tp.Area.MusicName != "" {
		a.room.HandleEvent(courtroom.Event{
			Kind: courtroom.EventMusic,
			Text: tp.Area.MusicName,
			Int:  tp.Area.MusicCharID,
			Name: tp.Area.MusicShowname,
			Loop: tp.Area.MusicLooping,
		})
	}

	// Transcript, oldest first. Empty text is skipped: RestoreMessage settles a
	// message, and an empty one would clear the stage the log just built.
	for _, line := range tp.Log {
		if line.Text == "" {
			continue
		}
		msg := protocol.ChatMessage{
			DeskMod:   protocol.DeskShow,
			CharName:  line.Name,
			Emote:     line.Emote,
			Message:   line.Text,
			Side:      line.Side,
			EmoteMod:  protocol.EmoteModIdle,
			CharID:    line.CharID,
			TextColor: line.Color,
			Showname:  line.Showname,
			Pair:      protocol.PairInfo{CharID: protocol.UnpairedCharID},
		}
		a.room.RestoreMessage(&msg)
	}

	a.uiDirty = true
}
