package ui

import (
	"testing"
	"time"
)

// TestDownloadDoneNotDropped pins the bug fix: with a full progress buffer (a
// fast grab's last intermediate snapshot still unread), the terminal done
// snapshot must STILL be delivered — otherwise pollDownload never flips
// a.dl.active off and the on-screen download chip sticks forever.
func TestDownloadDoneNotDropped(t *testing.T) {
	ch := make(chan dlProgress, 1)
	j := &dlJob{label: "character Phoenix", progress: ch, files: 12}

	j.publish(false) // fills the cap-1 buffer with an intermediate snapshot

	done := make(chan struct{})
	go func() { j.publish(true); close(done) }() // blocks until we make room

	first := <-ch // the buffered intermediate
	second := <-ch
	if !(first.done || second.done) {
		t.Fatal("done snapshot was dropped — the download indicator would never clear")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish(done) did not return after the snapshot was drained")
	}
}
