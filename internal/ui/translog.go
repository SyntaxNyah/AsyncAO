package ui

// Detailed transcript logging (opt-in, on by default): IC and OOC lines are interleaved into one per-server log file,
// logs/<server>/<date_time>.log beside the exe, AO2 style: IC as "[timestamp] showname (char): message" (AO2-Client
// chatlogpiece.cpp ChatLogPiece::toString), OOC as "[OOC][timestamp] name: text" (courtroom.cpp:1932,
// append_server_chatmessage). Showname first; the server is the folder, so there are no per-line server, area or pipe
// columns. One file per server per tab session (translogKey). Disk I/O runs on a background goroutine per writer fed by
// a bounded channel (hard rules 2 and 4), so the message path never blocks.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// transcriptBufSize bounds the detailed-log line queue; a full buffer sheds the line rather than blocking (the
// on-screen IC log keeps everything).
const transcriptBufSize = 256

// transcriptWriter appends lines to a file on a single background goroutine fed by a bounded channel.
//
// ended makes retire/close idempotent and write inert afterwards: a mid-run retirement and the shutdown drain can reach
// the same writer, and closing or sending on a closed channel panics. Plain bool, no mutex: every call is on the render
// thread.
type transcriptWriter struct {
	lines chan string
	wg    sync.WaitGroup
	ended bool
}

func newTranscriptWriter(path string) (*transcriptWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	w := &transcriptWriter{lines: make(chan string, transcriptBufSize)}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer f.Close()
		bw := bufio.NewWriter(f)
		for line := range w.lines {
			_, _ = bw.WriteString(line)
			_ = bw.WriteByte('\n')
			if len(w.lines) == 0 { // caught up: flush so the file is durable
				_ = bw.Flush()
			}
		}
		_ = bw.Flush()
	}()
	return w, nil
}

// write queues a line; a full buffer drops it (never blocks the render thread). Inert once retired, so a line racing a
// session teardown is shed rather than sent on a closed channel.
func (w *transcriptWriter) write(line string) {
	if w == nil || w.ended {
		return
	}
	select {
	case w.lines <- line:
	default:
	}
}

// retire ends the writer without waiting: closing the channel tells the goroutine to drain what is queued, flush, close
// the file and exit. Idempotent and nil-safe.
//
// A session can end mid-run (a tab closes, a reconnect mints a new session) and its file and goroutine must go with it,
// or each reconnect leaks one until transcriptWriterCap is reached and transcription silently stops. Not close():
// waiting blocks the render thread on a disk flush (hard rule 2). Queued lines are still written; the process-exit
// drain (App.CloseTranscript) is the one place that waits.
func (w *transcriptWriter) retire() {
	if w == nil || w.ended {
		return
	}
	w.ended = true
	close(w.lines)
}

// close retires the writer and waits for the file to be flushed and closed. Nil-safe and idempotent; only the shutdown
// path (App.CloseTranscript) calls it, the one moment a synchronous disk wait is allowed.
func (w *transcriptWriter) close() {
	if w == nil {
		return
	}
	w.retire()
	w.wg.Wait()
}

// detailedLogLine formats one IC message for the transcript, AO-style: a bracketed timestamp, "showname (char)"
// (showname first, just the character when there is no distinct showname), then the message. Pure.
//
// The body is stripped of the zero-width sidechannel (courtroom/spritestyle.go), as icMessageBody does: this file is
// read by the user and the in-app log browser and nothing re-decodes it, so the marker would only add invisible runes
// that break search and copy-paste.
func detailedLogLine(now time.Time, m *protocol.ChatMessage) string {
	char := strings.TrimSpace(m.CharName)
	show := strings.TrimSpace(m.Showname)
	who := show
	switch {
	case who == "":
		who = char
	case char != "" && !strings.EqualFold(show, char):
		who = show + " (" + char + ")" // showname (character)
	}
	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(now.Format("2006-01-02 15:04:05"))
	b.WriteString("] ")
	b.WriteString(who)
	b.WriteString(": ")
	b.WriteString(courtroom.StripSpriteStyle(m.Message))
	return b.String()
}

// oocLogLine formats one OOC transcript line like AO2-Client's own OOC log (Courtroom::append_server_chatmessage,
// courtroom.cpp:1932: `"[OOC][" + timestamp + "] " + name + ": " + message`): a bracketed "OOC" tag beside the
// timestamp, then line verbatim, as composed for the on-screen OOC panel, so the file matches the screen. Pure.
//
// Still readable by logbrowser.ParseWho: the "]" of the literal "[OOC]" is followed by "[", never a space, so the first
// "] " is still the one after the timestamp.
func oocLogLine(now time.Time, line string) string {
	var b strings.Builder
	b.WriteString("[OOC][")
	b.WriteString(now.Format("2006-01-02 15:04:05"))
	b.WriteString("] ")
	b.WriteString(line)
	return b.String()
}

// transcriptWriterCap bounds how many transcript writers one run holds open (hard rule 4); each is an open file plus a
// goroutine. It counts live (server, tab session) pairs: two tabs on one server are two logs. A pair is retired when
// nothing owns it (App.reapTranscripts on tab close or session reset), so the bound is the tab cap, not the number of
// reconnects. Past the cap a new session simply isn't transcribed (the on-screen logs are unaffected), like the
// per-writer line queue.
const transcriptWriterCap = 64

// translogKey identifies one transcript writer: a server and the tab session writing to it, so two tabs on one server
// get two files. Comparable, so a direct map key.
type translogKey struct {
	server  string
	session int
}

// sanitizeLogFolder makes a server name safe as a folder name: path separators and Windows-reserved characters become
// "_", control chars drop, surrounding spaces/dots trim (Windows rejects trailing ones), empty becomes "server". A name
// never escapes logs/.
func sanitizeLogFolder(server string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(server) {
		switch {
		case r < 0x20: // control characters
			continue
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	if out := strings.Trim(b.String(), " ."); out != "" {
		return out
	}
	return "server"
}

// transcriptPathFor is logs/<server>/<YYYY-MM-DD_HH-MM-SS>[-<seq>].log beside the exe, one file per tab session (the
// dirs are created).
//
// seq is that server's 1-based file ordinal within this run, shown only from the second file on so a single-tab session
// keeps the bare-timestamp name. It exists because the timestamp resolves to the second: two tabs joining one server in
// the same second would otherwise append into each other's file. See transcriptFilename.
func transcriptPathFor(server string, now time.Time, seq int) (string, error) {
	root, err := exeDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "logs", sanitizeLogFolder(server))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, transcriptFilename(now, seq)), nil
}

// transcriptFilename is the name rule alone: "<stamp>.log" for the first file of a run and "<stamp>-<seq>.log" after.
// Pure.
func transcriptFilename(now time.Time, seq int) string {
	stamp := now.Format(transcriptStampLayout)
	if seq <= 1 {
		return stamp + ".log"
	}
	return stamp + "-" + strconv.Itoa(seq) + ".log"
}

// transcriptStampLayout is the file-name timestamp, shared by the writer and the log browser's label parser so they
// can't drift.
const transcriptStampLayout = "2006-01-02_15-04-05"
