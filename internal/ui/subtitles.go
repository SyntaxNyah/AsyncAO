package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Subtitle export: an opt-in .srt + .vtt beside every video export, cue-timed to the video's frames, not the
// recording's wall-clock offsets (the idle-paced export ignores them). A cue opens on the first captured frame
// that shows the message's text and closes when the next message replaces it.

// subCue is one subtitle: the video frame window plus the speaker and line.
type subCue struct {
	startFrame, endFrame int // [start, end) in captured frames
	speaker, text        string
}

// subtitleLine extracts the speaker and plain text of a recorded message as the export chatbox renders it:
// zero-width wire frames stripped, "~~" centre prefix dropped, typewriter markup removed.
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

// subFeed closes the open cue at frame (its text is being replaced) and makes the new line pending until shown.
// Blank posts pend with empty text and never anchor.
func (j *gifExportJob) subFeed(msg *protocol.ChatMessage, frame int) {
	if !j.subsOn {
		return
	}
	j.subClose(frame)
	speaker, text := subtitleLine(msg)
	j.subPend = subCue{startFrame: -1, speaker: speaker, text: text}
	j.subHasPend = text != ""
}

// subAnchor promotes the pending cue to open once a captured frame shows its text.
func (j *gifExportJob) subAnchor(visibleRunes int, frame int) {
	if !j.subsOn || !j.subHasPend || visibleRunes <= 0 {
		return
	}
	j.subPend.startFrame = frame
	j.subOpen, j.subHasOpen = j.subPend, true
	j.subHasPend = false
}

// subClose ends the open cue at frame, keeping it only if it was visible for at least one frame.
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

// subTimestamp renders ms as an SRT/VTT clock; SRT wants a comma decimal separator, VTT a dot.
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

// formatSubtitles renders the cues as one SRT or VTT document, converting frames to ms via frameToMs.
// Multiply first so an hours-long export does not drift out of sync with the picture.
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

// writeSubtitleFiles writes <video stem>.srt and .vtt beside the video, off-thread; it reports whether both succeeded.
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
