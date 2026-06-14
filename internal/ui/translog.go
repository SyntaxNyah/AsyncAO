package ui

// Detailed transcript logging (opt-in, OFF by default): when enabled, IC
// messages are appended to logs/transcript.log beside the exe with a timestamp,
// the server, the area, and "CharName (Showname)" — a full chat record for
// casing. All disk I/O runs on ONE background goroutine fed by a bounded channel
// (rule §2: no synchronous disk I/O on the message path; §17.4: bounded queue),
// so the message seam on the render thread never blocks on the disk.

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

// detailedLogLine formats one IC message for the transcript: timestamp, server,
// area, "CharName (Showname)" (showname omitted when blank or same as the
// character), then the message text. Pure — unit-tested directly.
func detailedLogLine(now time.Time, server, area string, m *protocol.ChatMessage) string {
	who := m.CharName
	if show := strings.TrimSpace(m.Showname); show != "" && !strings.EqualFold(show, m.CharName) {
		who = m.CharName + " (" + show + ")"
	}
	if area == "" {
		area = "-"
	}
	var b strings.Builder
	b.WriteString(now.Format("2006-01-02 15:04:05"))
	b.WriteString(" | ")
	b.WriteString(server)
	b.WriteString(" | ")
	b.WriteString(area)
	b.WriteString(" | ")
	b.WriteString(who)
	b.WriteString(" | ")
	b.WriteString(m.Message)
	return b.String()
}

// transcriptPath is logs/transcript.log beside the exe (the dir is created).
func transcriptPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(exe), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "transcript.log"), nil
}
