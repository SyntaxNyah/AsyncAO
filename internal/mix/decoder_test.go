package mix

import (
	"bytes"
	"io"
	"testing"
)

const (
	decoderTestSec = 0.5
	// decoderTestChunk is how many samples one Decode call asks for.
	decoderTestChunk = 256
	// streamTestDrainChunks bounds the mixing a drained stream track gets to stop in.
	streamTestDrainChunks = 40
)

// byteReader is a Reader over a whole file.
type byteReader struct{ *bytes.Reader }

func (r byteReader) Size() int64 { return r.Reader.Size() }

func decodeAll(t *testing.T, d *Decoder) []float32 {
	t.Helper()
	var all []float32
	buf := make([]float32, decoderTestChunk)
	for {
		n, err := d.Decode(buf)
		all = append(all, buf[:n]...)
		if err == io.EOF {
			return all
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecoderReadsAWholeFileThroughAReader(t *testing.T) {
	headless(t)
	d, err := NewDecoder(byteReader{bytes.NewReader(silentWAV(t, decoderTestSec))})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Destroy()
	if d.Rate() != testRate || d.Channels() != testChannels {
		t.Fatalf("format %d Hz x%d, want %d Hz x%d", d.Rate(), d.Channels(), testRate, testChannels)
	}
	if got, want := len(decodeAll(t, d)), int(decoderTestSec*testRate)*testChannels; got != want {
		t.Fatalf("decoded %d samples, want %d", got, want)
	}
}

func TestDecoderRefusesWhatIsNotAudio(t *testing.T) {
	headless(t)
	if _, err := NewDecoder(byteReader{bytes.NewReader([]byte("not a sound file at all"))}); err == nil {
		t.Fatal("a text file opened as audio")
	}
}

func TestAStreamTrackPlaysWhatIsPutAndStopsWhenDrained(t *testing.T) {
	m := headless(t)
	s, err := m.NewStream(testRate, testChannels)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := m.NewTrack()
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Destroy()
	defer s.Destroy()
	if err := tr.SetStream(s); err != nil {
		t.Fatal(err)
	}
	samples := make([]float32, testChunkFrames*testChannels)
	if err := s.Put(samples); err != nil {
		t.Fatal(err)
	}
	if s.Queued() != len(samples) {
		t.Fatalf("queued %d, want %d", s.Queued(), len(samples))
	}
	if err := tr.PlayStream(0); err != nil {
		t.Fatal(err)
	}
	out := make([]float32, testChunkFrames*testChannels)
	if err := m.Generate(out); err != nil {
		t.Fatal(err)
	}
	if s.Queued() >= len(samples) {
		t.Fatalf("still %d queued after mixing", s.Queued())
	}
	s.Flush()
	for range streamTestDrainChunks {
		if !tr.Playing() {
			return
		}
		if err := m.Generate(out); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("a flushed, drained stream track kept playing")
}

// SDL3_mixer ends a stream track whose stream runs dry, flushed or not; the music player restarts it once more arrives.
func TestAStreamTrackStopsOnAnUnderrunAndResumes(t *testing.T) {
	m := headless(t)
	s, err := m.NewStream(testRate, testChannels)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := m.NewTrack()
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Destroy()
	defer s.Destroy()
	if err := tr.SetStream(s); err != nil {
		t.Fatal(err)
	}
	samples := make([]float32, testChunkFrames*testChannels)
	if err := s.Put(samples); err != nil {
		t.Fatal(err)
	}
	if err := tr.PlayStream(0); err != nil {
		t.Fatal(err)
	}
	out := make([]float32, testChunkFrames*testChannels)
	for range streamTestDrainChunks {
		if err := m.Generate(out); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Playing() {
		t.Fatal("a dry stream kept its track playing; the restart is no longer needed")
	}
	if err := s.Put(samples); err != nil {
		t.Fatal(err)
	}
	if err := tr.PlayStream(0); err != nil {
		t.Fatal(err)
	}
	if err := m.Generate(out); err != nil {
		t.Fatal(err)
	}
	if !tr.Playing() || s.Queued() >= len(samples) {
		t.Fatalf("restart did not resume: playing %v, queued %d", tr.Playing(), s.Queued())
	}
}
