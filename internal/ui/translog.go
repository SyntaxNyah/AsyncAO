package ui

// Detailed transcript logging (opt-in, ON by default since the 2026-08-09
// flip): when enabled, IC and OOC lines are INTERLEAVED into the same
// per-server log file — logs/<server>/<date_time>.log beside the exe — AO2
// style: IC as "[timestamp] showname (char): message"
// (../AO2-Client/src/chatlogpiece.cpp ChatLogPiece::toString), OOC as
// "[OOC][timestamp] name: text" (../AO2-Client/src/courtroom.cpp:1932,
// Courtroom::append_server_chatmessage) — showname first, the server is the
// folder so it isn't repeated per line, no area/pipe columns. One file per
// server per TAB SESSION (translogKey). All disk I/O runs on a background
// goroutine per writer fed by a bounded channel (rule §2: no synchronous disk
// I/O on the message path; §17.4: bounded queue), so the message seam never
// blocks.

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

// transcriptBufSize bounds the detailed-log line queue; a full buffer sheds the
// line rather than blocking the seam (the on-screen IC log keeps everything).
const transcriptBufSize = 256

// transcriptWriter appends lines to a file on a single background goroutine fed
// by a bounded channel.
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

// write queues a line; a full buffer drops it (never blocks the render thread).
// Inert once retired, so a line that raced a session teardown is shed rather than
// sent on a closed channel.
func (w *transcriptWriter) write(line string) {
	if w == nil || w.ended {
		return
	}
	select {
	case w.lines <- line:
	default:
	}
}

// retire ends the writer WITHOUT waiting for it: closing the channel is the signal
// for the goroutine to drain what is already queued, flush, close the file and
// exit on its own. Idempotent and nil-safe.
// It exists because a session can end mid-run — a tab closes, a reconnect mints a
// new session — and the file + goroutine that session opened must go with it, or
// every reconnect leaks one of each until transcriptWriterCap is reached and
func (w *transcriptWriter) retire() {
	if w == nil || w.ended {
		return
	}
	w.ended = true
	close(w.lines)
}

// close retires the writer and WAITS for the file to be flushed and closed.
// Nil-safe and idempotent; the shutdown path (App.CloseTranscript) is its only
// caller, because that is the only moment a synchronous disk wait is allowed.
func (w *transcriptWriter) close() {
	if w == nil {
		return
	}
	w.retire()
	w.wg.Wait()
}

// detailedLogLine formats one IC message for the transcript, AO-style: a bracketed timestamp, then
// "showname (char)" — showname FIRST, falling back to just the character when there's no distinct
// showname — then the message. No server (it's the folder) or area/pipe columns. Pure — unit-tested.
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

// oocLogLine formats one OOC transcript line, matching AO2-Client's own OOC
// log shape (Courtroom::append_server_chatmessage,
// ../AO2-Client/src/courtroom.cpp:1932: `"[OOC][" + timestamp + "] " + name +
// ": " + message`): a bracketed "OOC" tag beside the bracketed timestamp,
// then LINE verbatim — line is whatever pushOOC / the parked-tab seam already
// composed for the on-screen OOC panel ("name: text" for a real message, a
// bare "[MOD CALL] …" / "SERVER: …" / "CLIENT: …" line for a system one), so
// the file always matches what the screen showed rather than a second,
// independently-derived rendering of it. Pure — unit-tested.
func oocLogLine(now time.Time, line string) string {
	var b strings.Builder
	b.WriteString("[OOC][")
	b.WriteString(now.Format("2006-01-02 15:04:05"))
	b.WriteString("] ")
	b.WriteString(line)
	return b.String()
}

// transcriptWriterCap bounds how many live transcript writers ONE RUN may hold open (hard rule
// #4: no unbounded goroutines / open files). One writer = one open file + one goroutine.
const transcriptWriterCap = 64

// translogKey identifies ONE transcript writer: a server AND the tab session writing to it.
type translogKey struct {
	server  string
	session int
}

// sanitizeLogFolder makes a server name safe as a folder name: path separators and the
// Windows-reserved characters become "_", control chars drop, surrounding spaces/dots trim (Windows
// rejects trailing dots/spaces), empty → "server". Never lets a name escape logs/.
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

// transcriptPathFor is logs/<server>/<YYYY-MM-DD_HH-MM-SS>[-<seq>].log beside the exe — one file
// per TAB SESSION (the dirs are created). The timestamp names the file so each run is its own log.
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

// transcriptFilename is the name rule alone: "<stamp>.log" for the first file of a run and
// "<stamp>-<seq>.log" after it. Pure, so the naming and logbrowser.SessionLabel can be
// pinned against each other without touching disk.
func transcriptFilename(now time.Time, seq int) string {
	stamp := now.Format(transcriptStampLayout)
	if seq <= 1 {
		return stamp + ".log"
	}
	return stamp + "-" + strconv.Itoa(seq) + ".log"
}

// transcriptStampLayout is the file-name timestamp, shared by the writer and the log browser's
// label parser so the two can never drift (a browser that can't parse the writer's name shows a
// raw file name instead of a date).
const transcriptStampLayout = "2006-01-02_15-04-05"
