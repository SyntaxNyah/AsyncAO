package render

import (
	"log"
	"sync"
	"testing"
)

// TestDebugLogCaptureConcurrent hammers the init/drain pair: EnableDebugLogCapture
// (called once at Audio creation) runs alongside PollDebugLogs (the F8 panel's
// drain). Before the fix, PollDebugLogs read debugLogChan while EnableDebugLogCapture's
// sync.Once closure wrote it — a data race the Once could not cover, because the
// Once only serializes the writers, not the reader.
func TestDebugLogCaptureConcurrent(t *testing.T) {
	prev := log.Writer()
	t.Cleanup(func() { log.SetOutput(prev) })

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			EnableDebugLogCapture()
		}()
		go func() {
			defer wg.Done()
			_ = PollDebugLogs()
		}()
	}
	wg.Wait()
}
