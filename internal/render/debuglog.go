package render

import (
	"bytes"
	"io"
	"log"
	"os"
	"sync"
)

// debugLogSink captures log.Printf output from the render package and forwards
// it to a channel that the UI's debug panel can drain. Used for diagnosing
// issues like loop-point sample rate sniffing without needing a terminal.
type debugLogSink struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	sink chan<- string
}

// Write implements io.Writer. Called by log.Logger on every log.Printf.
func (d *debugLogSink) Write(p []byte) (n int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.buf.Write(p)
	for {
		line, err := d.buf.ReadBytes('\n')
		if err == io.EOF {
			// Incomplete line: put it back and wait for more
			d.buf.Write(line)
			break
		}
		if err != nil {
			return 0, err
		}
		// Trim trailing newline and send to UI
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			select {
			case d.sink <- string(line):
			default:
				// UI not draining fast enough; drop this line to avoid blocking
			}
		}
	}
	return len(p), nil
}

var (
	debugLogChan   chan string
	debugLogOnce   sync.Once
	originalStderr *os.File
)

// EnableDebugLogCapture redirects render package log.Printf to a channel that
// the UI can drain via PollDebugLogs. Called once at Audio creation time.
func EnableDebugLogCapture() {
	debugLogOnce.Do(func() {
		debugLogChan = make(chan string, 64) // buffer 64 lines
		sink := &debugLogSink{sink: debugLogChan}
		originalStderr = os.Stderr
		log.SetOutput(io.MultiWriter(sink, originalStderr))
	})
}

// PollDebugLogs drains any pending log lines captured from the render package.
// Returns all available lines (may be empty). Non-blocking.
func PollDebugLogs() []string {
	if debugLogChan == nil {
		return nil
	}
	var lines []string
	for {
		select {
		case line := <-debugLogChan:
			lines = append(lines, line)
		default:
			return lines
		}
	}
}
