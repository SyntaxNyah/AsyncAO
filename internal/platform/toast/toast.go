// Package toast shows desktop notifications under the app's own name.
package toast

import (
	"log"
	"runtime"
	"sync"
)

// queueCap bounds the notifications waiting to show; callers already space them out, so a full queue drops the rest.
const queueCap = 4

// App names the program the notifications come from.
type App struct {
	// ID is the AppUserModelID Windows files the notifications under.
	ID string
	// Name is the sender the notification shows.
	Name string
	// Icon is the .ico shown beside the name; nil shows none.
	Icon []byte
}

// sender shows notifications for one Notifier; every call runs on that Notifier's goroutine.
type sender interface {
	send(title, body string) error
	close()
}

type note struct{ title, body string }

// Notifier shows notifications one at a time on its own goroutine.
type Notifier struct {
	queue    chan note
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// New starts a Notifier for app; Close stops it.
func New(app App) *Notifier { return start(newSender(app)) }

func start(s sender) *Notifier {
	n := &Notifier{queue: make(chan note, queueCap), stop: make(chan struct{}), done: make(chan struct{})}
	go n.run(s)
	return n
}

// Show queues a notification without blocking, and drops it when the queue is full or the Notifier is closed.
func (n *Notifier) Show(title, body string) {
	select {
	case n.queue <- note{title, body}:
	default:
	}
}

// Close stops the Notifier and waits for its goroutine to finish; notifications still queued are dropped.
func (n *Notifier) Close() {
	n.stopOnce.Do(func() { close(n.stop) })
	<-n.done
}

func (n *Notifier) run(s sender) {
	// WinRT is initialized per OS thread, so the goroutine keeps one thread for its whole life.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(n.done)
	defer s.close()
	for {
		select {
		case <-n.stop:
			return
		case m := <-n.queue:
			if err := s.send(m.title, m.body); err != nil {
				log.Printf("toast: %v", err)
			}
		}
	}
}
