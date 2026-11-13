package toast

import "testing"

// fakeSender records notifications; while hold is open, send waits for it to close.
type fakeSender struct {
	sent   chan note
	hold   chan struct{}
	closed chan struct{}
}

func newFakeSender() *fakeSender {
	return &fakeSender{sent: make(chan note, queueCap), hold: make(chan struct{}), closed: make(chan struct{})}
}

func (f *fakeSender) send(title, body string) error {
	<-f.hold
	f.sent <- note{title, body}
	return nil
}

func (f *fakeSender) close() { close(f.closed) }

func TestShowReachesTheSenderAndCloseStopsIt(t *testing.T) {
	f := newFakeSender()
	close(f.hold)
	n := start(f)
	n.Show("Friend online", "Phoenix")
	if got := <-f.sent; got != (note{"Friend online", "Phoenix"}) {
		t.Errorf("sent %+v", got)
	}
	n.Close()
	select {
	case <-f.closed:
	default:
		t.Error("Close did not close the sender")
	}
}

func TestShowNeverBlocksWhileTheSenderIsBusy(t *testing.T) {
	f := newFakeSender()
	n := start(f)
	for range queueCap * 2 {
		n.Show("title", "body") // a full queue drops instead of waiting for the stuck send
	}
	close(f.hold)
	n.Close()
}

func TestShowAfterCloseIsDropped(t *testing.T) {
	f := newFakeSender()
	close(f.hold)
	n := start(f)
	n.Close()
	n.Close()
	for range queueCap * 2 {
		n.Show("title", "body")
	}
	if len(f.sent) != 0 {
		t.Errorf("%d notifications sent after Close", len(f.sent))
	}
}
