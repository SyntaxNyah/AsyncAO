package ui

// Detailed transcript logging (opt-in, OFF by default): when enabled, IC
// messages are appended to a per-server log file — logs/<server>/<date_time>.log
// beside the exe — AO-style: "[timestamp] showname (char): message" (showname
// first, the server is the folder so it isn't repeated per line, no area/pipe
// columns). One file per server per session. All disk I/O runs on a background
// goroutine per server fed by a bounded channel (rule §2: no synchronous disk I/O
// on the message path; §17.4: bounded queue), so the message seam never blocks.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// transcriptBufSize bounds the detailed-log line queue; a full buffer sheds the
// line rather than blocking the seam (the on-screen IC log keeps everything).
const transcriptBufSize = 256

// transcriptWriter appends lines to a file on a single background goroutine fed
// by a bounded channel.
//
// ended is the one-shot latch that makes retire/close idempotent and write inert
// afterwards. Both a mid-run retirement (the session that owned this file ended)
// and the shutdown drain can reach the same writer, and closing an already-closed
// channel — or sending on one — panics. Plain bool, no mutex: every one of these
// calls is on the render thread (the message seams, the tab lifecycle, the exit
// path), which is the same single-goroutine discipline the App state itself has.
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
//
// It exists because a session can end mid-run — a tab closes, a reconnect mints a
// new session — and the file + goroutine that session opened must go with it, or
// every reconnect leaks one of each until transcriptWriterCap is reached and
// transcription silently stops for the rest of the run. Deliberately NOT close():
// waiting means blocking the render thread on a disk flush, which hard rule §2
// forbids. Nothing is lost — the queued lines are written by the goroutine that
// outlives this call, and the process-exit drain (App.CloseTranscript) is the one
// place that legitimately waits.
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
	b.WriteString(m.Message)
	return b.String()
}

// transcriptWriterCap bounds how many live transcript writers ONE RUN may hold open (hard rule
// #4: no unbounded goroutines / open files). One writer = one open file + one goroutine.
//
// It counts LIVE (server, tab session) pairs, not servers: a tab is a session, and two tabs on
// one server are two logs. A pair is retired the moment nothing owns it any more (App.reapTranscripts
// on every tab close / session reset), so the number here is bounded by the TAB CAP, not by how
// many times you reconnect — 64 is far above any reachable tab count and exists purely as the
// named ceiling rule §4 requires.
//
// The reap is what makes that true. Without it every reconnect minted a session, opened a file and
// a goroutine, and abandoned both still-open; the 64th cycle hit this cap and transcription stopped
// for the rest of the run with nothing on screen to say so. Past the cap a new session still simply
// isn't transcribed (the on-screen logs are unaffected) — the same shedding rule the per-writer line
// queue uses — but reaching it now means 64 tabs open at once.
const transcriptWriterCap = 64

// translogKey identifies ONE transcript writer: a server AND the tab session writing to it.
// The session half is what keeps two tabs on the same server in two files — keyed by server
// alone they shared a writer and interleaved into one. Comparable (both value fields), so it
// is a map key directly.
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
//
// seq is that server's 1-based file ordinal within this run and appears only from the SECOND file
// on, so a single-tab session keeps the exact historical bare-timestamp name and every already-
// written log still parses. It exists because the timestamp only resolves to the second: two tabs
// joining one server in the same second would otherwise be handed the identical path and append
// into each other, defeating the split. See transcriptFilename for the name rule (pure, tested).
func transcriptPathFor(server string, now time.Time, seq int) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(exe), "logs", sanitizeLogFolder(server))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, transcriptFilename(now, seq)), nil
}

// transcriptFilename is the name rule alone: "<stamp>.log" for the first file of a run and
// "<stamp>-<seq>.log" after it. Pure, so the naming and the log browser's sessionLabel can be
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
