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
type transcriptWriter struct {
	lines chan string
	wg    sync.WaitGroup
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
func (w *transcriptWriter) write(line string) {
	select {
	case w.lines <- line:
	default:
	}
}

// close drains the queue and closes the file. Nil-safe (called at shutdown).
func (w *transcriptWriter) close() {
	if w == nil {
		return
	}
	close(w.lines)
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

// transcriptServerCap bounds how many distinct servers get a live transcript writer in one run
// (hard rule #4: no unbounded goroutines / open files). A generous ceiling — nobody logs to dozens
// of servers in one session; past it a new server simply isn't transcribed.
const transcriptServerCap = 64

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

// transcriptPathFor is logs/<server>/<YYYY-MM-DD_HH-MM-SS>.log beside the exe — one session file per
// server (the dirs are created). The timestamp names the file so each run is its own log.
func transcriptPathFor(server string, now time.Time) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(exe), "logs", sanitizeLogFolder(server))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, now.Format("2006-01-02_15-04-05")+".log"), nil
}
