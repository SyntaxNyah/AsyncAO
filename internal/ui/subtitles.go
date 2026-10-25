package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Subtitle export (#69): an opt-in .srt + .vtt written beside every 🎥 Video
// export, cue-timed to THE VIDEO's frames — not the recording's wall-clock
// offsets, which the idle-paced export deliberately ignores (the same reason
// the timeline uses OffsetMs but exports don't). A cue opens on the first
// captured frame that actually shows the message's text (so preanim/shout lead
// time isn't subtitled) and closes when the next message replaces it, matching
// what the exported chatbox shows. Pure cue/format helpers, unit-tested.

// subCue is one subtitle: the video frame window + the speaker and line.
type subCue struct {
	startFrame, endFrame int // [start, end) in captured frames
	speaker, text        string
}

// subtitleLine extracts the speaker + plain display text for a recorded message:
// zero-width wire frames stripped, the "~~" centre prefix dropped, and the
// typewriter's markup ({}, \cN, \b, \i) removed — exactly what the export
// chatbox renders (export rooms don't expand inline emotes, so neither do we).
func subtitleLine(msg *protocol.ChatMessage) (speaker, text string) {
	if msg == nil {
		return "", ""
	}
	clean := courtroom.StripSpriteStyle(msg.Message)
	clean = strings.TrimPrefix(clean, "~~")
	text = strings.TrimSpace(courtroom.StripChatMarkup(clean))
	speaker = msg.Showname
	if speaker == "" {
		speaker = msg.CharName
	}
	return speaker, text
}

// subFeed is called when the export feeds a message event: the currently-open
// cue closes at frame (its text is being replaced on screen) and the new line
// becomes pending until a captured frame actually shows it. Blank posts pend
// with empty text and simply never anchor.
func (j *gifExportJob) subFeed(msg *protocol.ChatMessage, frame int) {
	if !j.subsOn {
		return
	}
	j.subClose(frame)
	speaker, text := subtitleLine(msg)
	j.subPend = subCue{startFrame: -1, speaker: speaker, text: text}
	j.subHasPend = text != ""
}

// subAnchor promotes the pending cue to open once the scene shows its text —
// called for each captured frame with that frame's index.
func (j *gifExportJob) subAnchor(visibleRunes int, frame int) {
	if !j.subsOn || !j.subHasPend || visibleRunes <= 0 {
		return
	}
	j.subPend.startFrame = frame
	j.subOpen, j.subHasOpen = j.subPend, true
	j.subHasPend = false
}

// subClose ends the open cue (if any) at frame, keeping it only if it was ever
// visible for at least one frame.
func (j *gifExportJob) subClose(frame int) {
	if !j.subHasOpen {
		return
	}
	j.subOpen.endFrame = frame
	if j.subOpen.endFrame <= j.subOpen.startFrame {
		j.subOpen.endFrame = j.subOpen.startFrame + 1 // a same-frame swap still gets one frame
	}
	j.subs = append(j.subs, j.subOpen)
	j.subHasOpen = false
}

// subTimestamp renders ms as an SRT/VTT clock; SRT wants a comma decimal
// separator, VTT a dot.
func subTimestamp(ms int, comma bool) string {
	if ms < 0 {
		ms = 0
	}
	sep := "."
	if comma {
		sep = ","
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", ms/3600000, ms/60000%60, ms/1000%60, sep, ms%1000)
}

// formatSubtitles renders the cues as one SRT or VTT document. fps converts a cue's
// frame window to milliseconds via frameToMs (the video's real cadence) — multiply-
// first, so an hours-long export's cues don't drift out of sync with the picture.
func formatSubtitles(cues []subCue, fps int, vtt bool) string {
	var b strings.Builder
	if vtt {
		b.WriteString("WEBVTT\n\n")
	}
	for i, cu := range cues {
		if !vtt {
			fmt.Fprintf(&b, "%d\n", i+1)
		}
		fmt.Fprintf(&b, "%s --> %s\n", subTimestamp(frameToMs(cu.startFrame, fps), !vtt), subTimestamp(frameToMs(cu.endFrame, fps), !vtt))
		if cu.speaker != "" {
			fmt.Fprintf(&b, "%s: %s\n\n", cu.speaker, cu.text)
		} else {
			fmt.Fprintf(&b, "%s\n\n", cu.text)
		}
	}
	return b.String()
}

// writeSubtitleFiles writes <video stem>.srt + .vtt beside the video (called
// off-thread by finishVideoExport). Returns whether both writes succeeded. fps drives
// the cue timing via formatSubtitles (multiply-first, drift-free over long exports).
func writeSubtitleFiles(vidPath string, cues []subCue, fps int) bool {
	if len(cues) == 0 {
		return false
	}
	stem := strings.TrimSuffix(vidPath, "."+extOf(vidPath))
	okSRT := os.WriteFile(stem+".srt", []byte(formatSubtitles(cues, fps, false)), 0o644) == nil
	okVTT := os.WriteFile(stem+".vtt", []byte(formatSubtitles(cues, fps, true)), 0o644) == nil
	return okSRT && okVTT
}

// extOf is the path's extension without the dot ("" when none).
func extOf(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 && i < len(path)-1 {
		return path[i+1:]
	}
	return ""
}
