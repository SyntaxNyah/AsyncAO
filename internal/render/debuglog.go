package render

import (
	"bytes"
	"io"
	"log"
	"os"
	"sync"
)

// debugLogSink forwards render package log.Printf output to a channel the debug panel drains.
type debugLogSink struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	sink chan<- string
}

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

const debugLogBuffer = 64

var (
	// debugLogChan is eager: a lazy write under sync.Once races PollDebugLogs, as Once serializes only writers.
	debugLogChan   = make(chan string, debugLogBuffer)
	debugLogOnce   sync.Once
	originalStderr *os.File
)

// EnableDebugLogCapture redirects render package log.Printf to a channel; called once at Audio creation.
func EnableDebugLogCapture() {
	debugLogOnce.Do(func() {
		sink := &debugLogSink{sink: debugLogChan}
		originalStderr = os.Stderr
		log.SetOutput(io.MultiWriter(sink, originalStderr))
	})
}

// PollDebugLogs drains pending lines without blocking.
func PollDebugLogs() []string {
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
