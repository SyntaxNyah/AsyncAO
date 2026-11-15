package render

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/mix"
)

const (
	// musicAheadSec is how far decoding runs ahead of playback; it also covers a download's short stalls.
	musicAheadSec = 2
	// musicResumeSec is what must be queued before a track starts, or restarts after the download fell behind.
	musicResumeSec = 0.5
	// musicChunkSamples is how much one decode call produces.
	musicChunkSamples = 8192
	// musicWaitStep is how long the decoder sleeps while far enough ahead.
	musicWaitStep = 20 * time.Millisecond
	// musicPassesCap bounds the loop passes the clock remembers: only those still queued matter.
	musicPassesCap = 8
)

var errMusicFormatChanged = errors.New("render: a looped track came back in another format")

// musicPass is where one pass through the file starts: at sample at of the stream, from second from of the file.
type musicPass struct {
	at   int64
	from float64
}

// musicStream plays a track from a media.Stream that may still be downloading: one goroutine decodes it ahead of
// playback into an SDL audio stream, and reopens the decoder at each loop, so loops stay gapless. The goroutine owns
// the SDL stream and frees it once close is called; the render thread attaches it to a track and reads the clock.
type musicStream struct {
	src  media.Stream
	stop chan struct{}
	done chan struct{}

	mu       sync.Mutex
	out      *mix.Stream
	rate     int
	channels int
	loop     bool
	// loopStart and loopEnd are seconds into the file; loopEnd 0 loops at the file's end.
	loopStart, loopEnd float64
	pushed             int64
	passes             []musicPass
	durSec             float64
	ended              bool
	err                error
}

// newMusicStream starts decoding src from startSec; loop repeats it from loopStart, cut at loopEnd when set.
func newMusicStream(m *mix.Mixer, src media.Stream, startSec float64, loop bool, loopStart, loopEnd float64) *musicStream {
	s := &musicStream{src: src, stop: make(chan struct{}), done: make(chan struct{}), loop: loop,
		loopStart: loopStart, loopEnd: loopEnd}
	go s.run(m, startSec)
	return s
}

// close stops the decoder; the caller has taken the stream off its track first and closes src after.
func (s *musicStream) close() { close(s.stop) }

// output is the SDL stream to attach, nil until the file's header is read.
func (s *musicStream) output() *mix.Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out
}

// failure is why decoding stopped early, or nil.
func (s *musicStream) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// finished reports a one-shot track decoded to its end, so an empty stream means it has played out.
func (s *musicStream) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// queuedSec is how much is decoded but not yet played.
func (s *musicStream) queuedSec() float64 {
	s.mu.Lock()
	out, rate, ch := s.out, s.rate, s.channels
	s.mu.Unlock()
	if out == nil || rate == 0 || ch == 0 {
		return 0
	}
	return float64(out.Queued()) / float64(rate*ch)
}

// setLoop changes the loop points; the decoder reads them at each chunk, so they apply from the current pass.
func (s *musicStream) setLoop(start, end float64) {
	s.mu.Lock()
	s.loopStart, s.loopEnd = start, end
	s.mu.Unlock()
}

// clock is the position played in the file, and its length once a pass from the start has reached the end.
func (s *musicStream) clock() (posSec, durSec float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil || s.rate == 0 || len(s.passes) == 0 {
		return 0, 0, false
	}
	played := s.pushed - int64(s.out.Queued())
	p := s.passes[0]
	for _, q := range s.passes {
		if q.at <= played {
			p = q
		}
	}
	posSec = p.from + float64(played-p.at)/float64(s.rate*s.channels)
	return posSec, s.durSec, s.durSec > 0
}

func (s *musicStream) run(m *mix.Mixer, startSec float64) {
	defer close(s.done)
	from := startSec
	for first := true; ; first = false {
		dec, err := mix.NewDecoder(s.src)
		if err == nil && first {
			err = s.open(m, dec)
		} else if err == nil && (dec.Rate() != s.rate || dec.Channels() != s.channels) {
			err = errMusicFormatChanged
		}
		if err != nil {
			if dec != nil {
				dec.Destroy()
			}
			s.finish(err)
			return
		}
		natural, err := s.pass(dec, from)
		dec.Destroy()
		if err != nil {
			s.finish(err)
			return
		}
		s.mu.Lock()
		loop, start, end := s.loop, s.loopStart, s.loopEnd
		if natural && from == 0 && end == 0 && s.durSec == 0 {
			s.durSec = float64(s.pushed-s.passes[len(s.passes)-1].at) / float64(s.rate*s.channels)
		}
		s.mu.Unlock()
		if !loop {
			s.out.Flush()
			s.finish(nil)
			return
		}
		from = start
	}
}

// open creates the SDL stream once the first decoder knows the file's format.
func (s *musicStream) open(m *mix.Mixer, dec *mix.Decoder) error {
	out, err := m.NewStream(dec.Rate(), dec.Channels())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.out, s.rate, s.channels = out, dec.Rate(), dec.Channels()
	s.mu.Unlock()
	return nil
}

// finish records why decoding ended and waits for close, so the SDL stream outlives every queued sample.
func (s *musicStream) finish(err error) {
	s.mu.Lock()
	s.ended, s.err = true, err
	out := s.out
	s.mu.Unlock()
	<-s.stop
	if out != nil {
		out.Destroy()
	}
}

// pass decodes one run through the file from second from, skipping what comes before it, until the end or the loop
// end; natural reports that it reached the file's end.
func (s *musicStream) pass(dec *mix.Decoder, from float64) (natural bool, err error) {
	ch := int64(s.channels)
	skip := int64(from*float64(s.rate)) * ch
	ahead := int(musicAheadSec * float64(s.rate) * float64(ch))
	buf := make([]float32, musicChunkSamples*ch)
	var at int64 // samples decoded from the file's start
	marked := false
	for {
		select {
		case <-s.stop:
			return false, errMusicStopped
		default:
		}
		n, derr := dec.Decode(buf)
		if derr != nil && !errors.Is(derr, io.EOF) {
			return false, derr
		}
		chunk := buf[:n]
		if at < skip {
			drop := min(int64(n), skip-at)
			chunk = chunk[drop:]
		}
		s.mu.Lock()
		end := int64(0)
		if s.loop && s.loopEnd > 0 {
			end = int64(s.loopEnd*float64(s.rate)) * ch
		}
		s.mu.Unlock()
		cut := end > 0 && at+int64(n) >= end
		if cut {
			chunk = chunk[:max(0, len(chunk)-int(at+int64(n)-end))]
		}
		if len(chunk) > 0 {
			if !s.waitRoom(ahead) {
				return false, errMusicStopped
			}
			s.mu.Lock()
			if !marked {
				if len(s.passes) == musicPassesCap {
					s.passes = append(s.passes[:0], s.passes[1:]...)
				}
				s.passes = append(s.passes, musicPass{at: s.pushed, from: from})
				marked = true
			}
			s.pushed += int64(len(chunk))
			s.mu.Unlock()
			if err := s.out.Put(chunk); err != nil {
				return false, err
			}
		}
		at += int64(n)
		if cut {
			return false, nil
		}
		if errors.Is(derr, io.EOF) {
			return true, nil
		}
	}
}

var errMusicStopped = errors.New("render: music stopped")

// waitRoom sleeps while ahead samples are already queued; false means the track was stopped.
func (s *musicStream) waitRoom(ahead int) bool {
	for s.out.Queued() > ahead {
		select {
		case <-s.stop:
			return false
		case <-time.After(musicWaitStep):
		}
	}
	return true
}
