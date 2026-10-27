// Package mix is AsyncAO's binding to SDL3_mixer: a Mixer plays any number of
// Tracks at once, each fed one Audio. Loop points, fades and seeking are
// SDL3_mixer's own, so crossfades and gapless custom loops (#48) need no fork.
//
// Positions are converted between seconds and sample frames at the Audio's own
// sample rate, so a loop point lands on the exact frame the sidecar names.
// Hard rule 1 applies: everything here runs on the main thread.
package mix

/*
#cgo pkg-config: sdl3-mixer
#include <stdlib.h>
#include <SDL3_mixer/SDL_mixer.h>

static bool asyncaoPlay(MIX_Track *track, Sint64 loops, Sint64 startFrame, Sint64 loopStartFrame,
		Sint64 maxFrame, Sint64 fadeInMs) {
	SDL_PropertiesID props = SDL_CreateProperties();
	if (props == 0) {
		return false;
	}
	SDL_SetNumberProperty(props, MIX_PROP_PLAY_LOOPS_NUMBER, loops);
	if (startFrame > 0) {
		SDL_SetNumberProperty(props, MIX_PROP_PLAY_START_FRAME_NUMBER, startFrame);
	}
	if (loopStartFrame > 0) {
		SDL_SetNumberProperty(props, MIX_PROP_PLAY_LOOP_START_FRAME_NUMBER, loopStartFrame);
	}
	if (maxFrame > 0) {
		SDL_SetNumberProperty(props, MIX_PROP_PLAY_MAX_FRAME_NUMBER, maxFrame);
	}
	if (fadeInMs > 0) {
		SDL_SetNumberProperty(props, MIX_PROP_PLAY_FADE_IN_MILLISECONDS_NUMBER, fadeInMs);
	}
	bool ok = MIX_PlayTrack(track, props);
	SDL_DestroyProperties(props);
	return ok;
}

static int asyncaoAudioRate(MIX_Audio *audio) {
	SDL_AudioSpec spec;
	return MIX_GetAudioFormat(audio, &spec) ? spec.freq : 0;
}
*/
import "C"

import (
	"errors"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Mixer mixes tracks into one output.
type Mixer C.MIX_Mixer

// Audio is a loaded sound. It is reference-counted by the tracks that play it,
// so Destroy is safe while it is still playing.
type Audio C.MIX_Audio

// Track plays one Audio at a time.
type Track C.MIX_Track

// LoopForever is PlayOptions.Loops for a track that never ends by itself.
const LoopForever = -1

var errEmptyAudio = errors.New("mix: cannot load an empty buffer")

func failed() error {
	if err := sdl.GetError(); err != nil {
		return err
	}
	return errors.New("mix: call failed without an error message")
}

// OpenDevice opens the default playback device, mixing at freq Hz.
func OpenDevice(freq, channels int) (*Mixer, error) {
	if !C.MIX_Init() {
		return nil, failed()
	}
	spec := C.SDL_AudioSpec{format: C.SDL_AUDIO_F32, channels: C.int(channels), freq: C.int(freq)}
	m := C.MIX_CreateMixerDevice(C.SDL_AUDIO_DEVICE_DEFAULT_PLAYBACK, &spec)
	if m == nil {
		err := failed()
		C.MIX_Quit()
		return nil, err
	}
	return (*Mixer)(m), nil
}

// NewHeadless creates a mixer with no device: its output is pulled with
// Generate, as float32 samples. Tests use it to play real audio without a
// sound card.
func NewHeadless(freq, channels int) (*Mixer, error) {
	if !C.MIX_Init() {
		return nil, failed()
	}
	spec := C.SDL_AudioSpec{format: C.SDL_AUDIO_F32, channels: C.int(channels), freq: C.int(freq)}
	m := C.MIX_CreateMixer(&spec)
	if m == nil {
		err := failed()
		C.MIX_Quit()
		return nil, err
	}
	return (*Mixer)(m), nil
}

func (m *Mixer) c() *C.MIX_Mixer { return (*C.MIX_Mixer)(m) }

// Close stops every track, closes the device and frees the tracks. Audio
// objects are not freed: Destroy them first.
func (m *Mixer) Close() {
	C.MIX_DestroyMixer(m.c())
	C.MIX_Quit()
}

// Generate mixes len(out) samples of a headless mixer's output into out.
func (m *Mixer) Generate(out []float32) error {
	if len(out) == 0 {
		return nil
	}
	bytes := len(out) * int(unsafe.Sizeof(out[0]))
	if C.MIX_Generate(m.c(), unsafe.Pointer(&out[0]), C.int(bytes)) < 0 {
		return failed()
	}
	return nil
}

// Load decodes a sound held in memory. A predecoded sound is copied, so data
// may be dropped afterwards. A streamed one (predecode false, for music) reads
// data while it plays: the caller keeps data alive until the Audio is destroyed
// and no track plays it.
func (m *Mixer) Load(data []byte, predecode bool) (*Audio, error) {
	if len(data) == 0 {
		return nil, errEmptyAudio
	}
	io := C.SDL_IOFromConstMem(unsafe.Pointer(&data[0]), C.size_t(len(data)))
	if io == nil {
		return nil, failed()
	}
	a := C.MIX_LoadAudio_IO(m.c(), io, C.bool(predecode), true)
	if a == nil {
		return nil, failed()
	}
	return (*Audio)(a), nil
}

// LoadFile decodes a sound file completely.
func (m *Mixer) LoadFile(path string) (*Audio, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	a := C.MIX_LoadAudio(m.c(), cpath, true)
	if a == nil {
		return nil, failed()
	}
	return (*Audio)(a), nil
}

// NewTrack creates a track on the mixer.
func (m *Mixer) NewTrack() (*Track, error) {
	t := C.MIX_CreateTrack(m.c())
	if t == nil {
		return nil, failed()
	}
	return (*Track)(t), nil
}

func (a *Audio) c() *C.MIX_Audio { return (*C.MIX_Audio)(a) }

// Destroy drops the caller's reference.
func (a *Audio) Destroy() { C.MIX_DestroyAudio(a.c()) }

// rate is the sound's sample rate; frame positions count in it.
func (a *Audio) rate() float64 { return float64(C.asyncaoAudioRate(a.c())) }

func (a *Audio) frames(sec float64) C.Sint64 { return C.Sint64(sec * a.rate()) }

// Duration returns the sound's length in seconds; ok is false when the
// decoder cannot tell (some streamed formats) or the sound is endless.
func (a *Audio) Duration() (sec float64, ok bool) {
	frames := C.MIX_GetAudioDuration(a.c())
	rate := a.rate()
	if frames < 0 || rate == 0 {
		return 0, false
	}
	return float64(frames) / rate, true
}

// PlayOptions configures Track.Play. Zero values mean: play once, from the
// start, no fade, no loop points.
type PlayOptions struct {
	// Loops is how many times to loop after the first play; LoopForever for no end.
	Loops int
	// StartSec is where the first play starts.
	StartSec float64
	// LoopStartSec is where each loop restarts; LoopEndSec (when > 0) is where
	// each pass ends. Both are seconds into the sound.
	LoopStartSec, LoopEndSec float64
	// FadeInMs ramps the first play up from silence.
	FadeInMs int
}

func (t *Track) c() *C.MIX_Track { return (*C.MIX_Track)(t) }

// Destroy frees the track.
func (t *Track) Destroy() { C.MIX_DestroyTrack(t.c()) }

// audio is the sound currently on the track, or nil.
func (t *Track) audio() *Audio { return (*Audio)(C.MIX_GetTrackAudio(t.c())) }

// SetAudio puts a sound on the track. A playing track switches at once.
func (t *Track) SetAudio(a *Audio) error {
	if !C.MIX_SetTrackAudio(t.c(), a.c()) {
		return failed()
	}
	return nil
}

// Play starts the track's sound.
func (t *Track) Play(o PlayOptions) error {
	a := t.audio()
	if a == nil {
		return errors.New("mix: track has no audio")
	}
	var loopStart, maxFrame C.Sint64
	if o.LoopEndSec > 0 {
		loopStart, maxFrame = a.frames(o.LoopStartSec), a.frames(o.LoopEndSec)
	}
	if !C.asyncaoPlay(t.c(), C.Sint64(o.Loops), a.frames(o.StartSec), loopStart, maxFrame, C.Sint64(o.FadeInMs)) {
		return failed()
	}
	return nil
}

// Stop stops the track, fading out over fadeOutMs first when > 0.
func (t *Track) Stop(fadeOutMs int) {
	C.MIX_StopTrack(t.c(), C.MIX_TrackMSToFrames(t.c(), C.Sint64(fadeOutMs)))
}

// Playing reports whether the track is producing sound (not stopped or ended).
func (t *Track) Playing() bool { return bool(C.MIX_TrackPlaying(t.c())) }

// SetGain sets the track's volume: 0 silent, 1 unchanged.
func (t *Track) SetGain(gain float32) { C.MIX_SetTrackGain(t.c(), C.float(gain)) }

// Position returns the playback position in seconds of the track's sound.
func (t *Track) Position() (sec float64, ok bool) {
	a := t.audio()
	if a == nil {
		return 0, false
	}
	frames := C.MIX_GetTrackPlaybackPosition(t.c())
	rate := a.rate()
	if frames < 0 || rate == 0 {
		return 0, false
	}
	return float64(frames) / rate, true
}

// Seek moves playback to sec seconds into the track's sound.
func (t *Track) Seek(sec float64) error {
	a := t.audio()
	if a == nil {
		return errors.New("mix: track has no audio")
	}
	if !C.MIX_SetTrackPlaybackPosition(t.c(), a.frames(sec)) {
		return failed()
	}
	return nil
}
