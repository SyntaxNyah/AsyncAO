package mix

import (
	"encoding/binary"
	"testing"
)

const (
	testRate     = 8000
	testChannels = 1
	// testChunkFrames is how much output one Generate call mixes (50 ms).
	testChunkFrames = 400
)

// silentWAV is a mono 16-bit WAV of the given length.
func silentWAV(t *testing.T, sec float64) []byte {
	t.Helper()
	const (
		bitsPerSample = 16
		bytesPerFrame = bitsPerSample / 8
		pcmFormat     = 1
		fmtChunkSize  = 16
		headerSize    = 44
		riffPreamble  = 8
	)
	dataLen := int(sec*testRate) * bytesPerFrame
	b := make([]byte, 0, headerSize+dataLen)
	b = append(b, "RIFF"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(headerSize-riffPreamble+dataLen))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, fmtChunkSize)
	b = binary.LittleEndian.AppendUint16(b, pcmFormat)
	b = binary.LittleEndian.AppendUint16(b, testChannels)
	b = binary.LittleEndian.AppendUint32(b, testRate)
	b = binary.LittleEndian.AppendUint32(b, testRate*bytesPerFrame)
	b = binary.LittleEndian.AppendUint16(b, bytesPerFrame)
	b = binary.LittleEndian.AppendUint16(b, bitsPerSample)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(dataLen))
	return append(b, make([]byte, dataLen)...)
}

func headless(t *testing.T) *Mixer {
	t.Helper()
	m, err := NewHeadless(testRate, testChannels)
	if err != nil {
		t.Skipf("SDL3_mixer unavailable: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

func playing(t *testing.T, m *Mixer, sec float64, o PlayOptions) *Track {
	t.Helper()
	a, err := m.Load(silentWAV(t, sec), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Destroy)
	tr, err := m.NewTrack()
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.SetAudio(a); err != nil {
		t.Fatal(err)
	}
	if err := tr.Play(o); err != nil {
		t.Fatal(err)
	}
	return tr
}

// A custom loop (#48) must wrap at its end on every pass, not only the first:
// the loop end is SDL3_mixer's "max frame", and the header does not say it
// applies again after a loop. This pins that it does.
func TestLoopEndHoldsOnEveryPass(t *testing.T) {
	const (
		trackSec     = 2.0
		loopStartSec = 0.5
		loopEndSec   = 1.0
		// mixSec covers several passes of the half-second loop.
		mixSec = 4.0
	)
	m := headless(t)
	tr := playing(t, m, trackSec, PlayOptions{Loops: LoopForever, LoopStartSec: loopStartSec, LoopEndSec: loopEndSec})
	out := make([]float32, testChunkFrames*testChannels)
	chunkSec := float64(testChunkFrames) / testRate
	for mixed := 0.0; mixed < mixSec; mixed += chunkSec {
		if err := m.Generate(out); err != nil {
			t.Fatal(err)
		}
		pos, ok := tr.Position()
		if !ok {
			t.Fatalf("position unknown after %.2fs", mixed)
		}
		if pos > loopEndSec {
			t.Fatalf("after %.2fs of mixing the track is at %.3fs, past the loop end %.1fs", mixed+chunkSec, pos, loopEndSec)
		}
		if mixed > loopEndSec && pos < loopStartSec {
			t.Fatalf("after %.2fs of mixing the track is at %.3fs, before the loop start %.1fs", mixed+chunkSec, pos, loopStartSec)
		}
	}
	if !tr.Playing() {
		t.Fatal("a looping track stopped by itself")
	}
}

// A play-once track ends by itself, which is how the client notices a one-shot
// song (2.9 NO_REPEAT, #15) has finished.
func TestPlayOnceEnds(t *testing.T) {
	const (
		trackSec = 0.2
		mixSec   = 0.5
	)
	m := headless(t)
	tr := playing(t, m, trackSec, PlayOptions{})
	out := make([]float32, testChunkFrames*testChannels)
	for mixed := 0.0; mixed < mixSec; mixed += float64(testChunkFrames) / testRate {
		if err := m.Generate(out); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Playing() {
		t.Fatal("a play-once track is still playing after its end")
	}
}

// A sound stays playable after Destroy while a track still uses it: SDL3_mixer
// counts references, so evicting a cached sound mid-play is safe.
func TestDestroyWhilePlaying(t *testing.T) {
	const (
		trackSec = 1.0
		mixSec   = 0.2
	)
	m := headless(t)
	a, err := m.Load(silentWAV(t, trackSec), true)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := m.NewTrack()
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.SetAudio(a); err != nil {
		t.Fatal(err)
	}
	if err := tr.Play(PlayOptions{}); err != nil {
		t.Fatal(err)
	}
	a.Destroy()
	out := make([]float32, testChunkFrames*testChannels)
	for mixed := 0.0; mixed < mixSec; mixed += float64(testChunkFrames) / testRate {
		if err := m.Generate(out); err != nil {
			t.Fatal(err)
		}
	}
	if !tr.Playing() {
		t.Fatal("destroying the caller's reference stopped the track")
	}
}

// Seek and Position agree, in seconds of the sound.
func TestSeekPosition(t *testing.T) {
	const (
		trackSec  = 2.0
		targetSec = 1.25
		tolerance = 0.01
	)
	m := headless(t)
	tr := playing(t, m, trackSec, PlayOptions{})
	if err := tr.Seek(targetSec); err != nil {
		t.Fatal(err)
	}
	pos, ok := tr.Position()
	if !ok || pos < targetSec-tolerance || pos > targetSec+tolerance {
		t.Fatalf("position after Seek(%v) = %v (ok=%v)", targetSec, pos, ok)
	}
}
