package mix

/*
#include <stdint.h>
#include <SDL3_mixer/SDL_mixer.h>

SDL_IOStream *asyncaoOpenIO(uintptr_t handle);
*/
import "C"

import (
	"errors"
	"io"
	"runtime/cgo"
	"unsafe"
)

const bytesPerSample = 4 // float32

// Reader is what a Decoder reads: a file read at offsets, which may still be arriving, so ReadAt may wait.
type Reader interface {
	ReadAt(p []byte, off int64) (int, error)
	// Size is the file's length, or -1 while it isn't known.
	Size() int64
}

// reading is a Reader with the position SDL's stream keeps.
type reading struct {
	r   Reader
	pos int64
}

//export asyncaoIOSize
func asyncaoIOSize(h C.uintptr_t) C.Sint64 {
	return C.Sint64(cgo.Handle(h).Value().(*reading).r.Size())
}

//export asyncaoIOSeek
func asyncaoIOSeek(h C.uintptr_t, offset C.Sint64, whence C.int) C.Sint64 {
	s := cgo.Handle(h).Value().(*reading)
	to := int64(offset)
	switch whence {
	case C.SDL_IO_SEEK_CUR:
		to += s.pos
	case C.SDL_IO_SEEK_END:
		size := s.r.Size()
		if size < 0 {
			return -1
		}
		to += size
	}
	if to < 0 {
		return -1
	}
	s.pos = to
	return C.Sint64(to)
}

//export asyncaoIORead
func asyncaoIORead(h C.uintptr_t, ptr unsafe.Pointer, size C.size_t, status *C.SDL_IOStatus) C.size_t {
	s := cgo.Handle(h).Value().(*reading)
	p := unsafe.Slice((*byte)(ptr), int(size))
	n, err := s.r.ReadAt(p, s.pos)
	s.pos += int64(n)
	switch {
	case errors.Is(err, io.EOF):
		*status = C.SDL_IO_STATUS_EOF
	case err != nil:
		*status = C.SDL_IO_STATUS_ERROR
	}
	return C.size_t(n)
}

//export asyncaoIOClose
func asyncaoIOClose(h C.uintptr_t) { cgo.Handle(h).Delete() }

// Decoder turns a file into float samples on whatever thread calls it; SDL documents its calls as thread-safe.
type Decoder struct {
	c        *C.MIX_AudioDecoder
	rate     int
	channels int
}

// NewDecoder opens r, reading what the format's header needs; the Decoder reads r until Destroy.
func NewDecoder(r Reader) (*Decoder, error) {
	h := cgo.NewHandle(&reading{r: r})
	stream := C.asyncaoOpenIO(C.uintptr_t(h))
	if stream == nil {
		h.Delete()
		return nil, failed()
	}
	dec := C.MIX_CreateAudioDecoder_IO(stream, true, 0) // closes stream, and so the handle, on failure too
	if dec == nil {
		return nil, failed()
	}
	var spec C.SDL_AudioSpec
	if !C.MIX_GetAudioDecoderFormat(dec, &spec) {
		C.MIX_DestroyAudioDecoder(dec)
		return nil, failed()
	}
	return &Decoder{c: dec, rate: int(spec.freq), channels: int(spec.channels)}, nil
}

// Rate is the file's sample rate.
func (d *Decoder) Rate() int { return d.rate }

// Channels is the file's channel count; samples interleave them.
func (d *Decoder) Channels() int { return d.channels }

// Decode fills out with interleaved samples at the file's rate and returns how many it wrote; io.EOF ends the file.
func (d *Decoder) Decode(out []float32) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	spec := C.SDL_AudioSpec{format: C.SDL_AUDIO_F32, channels: C.int(d.channels), freq: C.int(d.rate)}
	n := C.MIX_DecodeAudio(d.c, unsafe.Pointer(&out[0]), C.int(len(out)*bytesPerSample), &spec)
	switch {
	case n < 0:
		return 0, failed()
	case n == 0:
		return 0, io.EOF
	}
	return int(n) / bytesPerSample, nil
}

// Destroy frees the decoder and stops it reading.
func (d *Decoder) Destroy() { C.MIX_DestroyAudioDecoder(d.c) }

// Stream is an SDL audio stream a track plays from: samples put into it are mixed as the track asks for them.
type Stream C.SDL_AudioStream

func (s *Stream) c() *C.SDL_AudioStream { return (*C.SDL_AudioStream)(s) }

// NewStream takes interleaved float samples at rate and converts them to the mixer's format.
func (m *Mixer) NewStream(rate, channels int) (*Stream, error) {
	var out C.SDL_AudioSpec
	if !C.MIX_GetMixerFormat(m.c(), &out) {
		return nil, failed()
	}
	in := C.SDL_AudioSpec{format: C.SDL_AUDIO_F32, channels: C.int(channels), freq: C.int(rate)}
	s := C.SDL_CreateAudioStream(&in, &out)
	if s == nil {
		return nil, failed()
	}
	return (*Stream)(s), nil
}

// Put queues samples; SDL copies them, from any thread.
func (s *Stream) Put(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}
	if !C.SDL_PutAudioStreamData(s.c(), unsafe.Pointer(&samples[0]), C.int(len(samples)*bytesPerSample)) {
		return failed()
	}
	return nil
}

// Queued is how many samples are put but not yet played.
func (s *Stream) Queued() int { return int(C.SDL_GetAudioStreamQueued(s.c())) / bytesPerSample }

// Flush marks the end of what will be put, so the last samples play out.
func (s *Stream) Flush() { C.SDL_FlushAudioStream(s.c()) }

// Destroy frees the stream; take it off its track first.
func (s *Stream) Destroy() { C.SDL_DestroyAudioStream(s.c()) }

// SetStream puts a stream on the track in place of a sound; nil takes it off.
func (t *Track) SetStream(s *Stream) error {
	if !C.MIX_SetTrackAudioStream(t.c(), s.c()) {
		return failed()
	}
	return nil
}
