// Package mixerx wraps SDL Mixer X (WohlSoft's extended SDL_mixer fork) for
// AsyncAO. It is a drop-in replacement for go-sdl2's `mix` package with the
// added runtime loop-point setters (Mix_SetMusicLoopStartTime/EndTime) that
// AsyncAO's custom music loop points (#48) depend on.
//
// The cgo preamble links SDL Mixer X (AsyncAO's fork of WohlSoft/SDL-Mixer-X),
// which provides the multi-music stream API and the runtime loop-point setters
// that AsyncAO's crossfade and gapless-loop support depend on. Only the library
// NAME is pinned here; the include and library search paths are supplied by the
// build environment (CGO_CFLAGS / CGO_LDFLAGS — see scripts/build.ps1,
// scripts/setup-deps.*, and the CI workflows, which build the fork and point
// cgo at its install prefix). See docs/SDL_MIXER_X-MIGRATION-PLAN.md.
package mixerx

/*
#cgo LDFLAGS: -lSDL2_mixer_ext
#include <stdlib.h>
#include <SDL2/SDL_mixer_ext.h>
*/
import "C"

import (
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// Chunk mirrors C's Mix_Chunk. Its memory layout must stay byte-identical to
// Mix_Chunk because it is handed to C as an unsafe.Pointer.
type Chunk struct {
	allocated int32
	buf       *uint8
	len_      uint32
	volume    uint8
}

// Fading is the music/channel fading state.
type Fading int

// The different supported fading types.
const (
	NO_FADING Fading = iota
	FADING_OUT
	FADING_IN
)

// Music is an opaque handle to a decoded music stream.
type Music C.Mix_Music

// MusicType is the file-format encoding of a music stream.
type MusicType int

// Init flags.
const (
	INIT_FLAC = C.MIX_INIT_FLAC
	INIT_MOD  = C.MIX_INIT_MOD
	INIT_MP3  = C.MIX_INIT_MP3
	INIT_OGG  = C.MIX_INIT_OGG
	INIT_OPUS = C.MIX_INIT_OPUS
)

// Good default values for a PC soundcard.
const (
	DEFAULT_FREQUENCY = C.MIX_DEFAULT_FREQUENCY
	DEFAULT_FORMAT    = C.MIX_DEFAULT_FORMAT
	DEFAULT_CHANNELS  = C.MIX_DEFAULT_CHANNELS
	MAX_VOLUME        = C.MIX_MAX_VOLUME
)

func cint(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// Init loads dynamic libraries and prepares them for use.
func Init(flags int) error {
	initted := int(C.Mix_Init(C.int(flags)))
	if initted&flags != flags {
		return sdl.GetError()
	}
	return nil
}

// Quit unloads libraries loaded with Init.
func Quit() {
	C.Mix_Quit()
}

// OpenAudio opens the mixer with a certain audio format.
func OpenAudio(frequency int, format uint16, channels, chunksize int) error {
	if C.Mix_OpenAudio(C.int(frequency), C.Uint16(format), C.int(channels), C.int(chunksize)) < 0 {
		return sdl.GetError()
	}
	return nil
}

// CloseAudio closes the mixer, halting all playback.
func CloseAudio() {
	C.Mix_CloseAudio()
}

// AllocateChannels dynamically changes the number of channels managed by the
// mixer. Returns the number of channels allocated.
func AllocateChannels(numchans int) int {
	return int(C.Mix_AllocateChannels(C.int(numchans)))
}

// ReserveChannels reserves num channels from being used when playing samples
// with channel == -1. Returns the number of channels reserved.
func ReserveChannels(num int) int {
	return int(C.Mix_ReserveChannels(C.int(num)))
}

// LoadWAVRW loads src for use as a sample.
func LoadWAVRW(src *sdl.RWops, freesrc bool) (*Chunk, error) {
	_src := (*C.SDL_RWops)(unsafe.Pointer(src))
	chunk := (*Chunk)(unsafe.Pointer(C.Mix_LoadWAV_RW(_src, cint(freesrc))))
	if chunk == nil {
		return nil, sdl.GetError()
	}
	return chunk, nil
}

// LoadWAV loads file for use as a sample.
func LoadWAV(file string) (*Chunk, error) {
	_file := C.CString(file)
	defer C.free(unsafe.Pointer(_file))
	_rb := C.CString("rb")
	defer C.free(unsafe.Pointer(_rb))
	chunk := (*Chunk)(unsafe.Pointer(C.Mix_LoadWAV_RW(C.SDL_RWFromFile(_file, _rb), 1)))
	if chunk == nil {
		return nil, sdl.GetError()
	}
	return chunk, nil
}

// LoadMUSRW loads a music file from an sdl.RWops object.
func LoadMUSRW(src *sdl.RWops, freesrc int) (*Music, error) {
	_src := (*C.SDL_RWops)(unsafe.Pointer(src))
	mus := (*Music)(unsafe.Pointer(C.Mix_LoadMUS_RW(_src, C.int(freesrc))))
	if mus == nil {
		return nil, sdl.GetError()
	}
	return mus, nil
}

// Free frees the memory used in chunk, and frees chunk itself as well.
func (chunk *Chunk) Free() {
	_chunk := (*C.Mix_Chunk)(unsafe.Pointer(chunk))
	C.Mix_FreeChunk(_chunk)
}

// Free frees the loaded music. If music is playing it will be halted.
func (music *Music) Free() {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	C.Mix_FreeMusic(_music)
}

// Play plays chunk on channel, or on the first free unreserved channel if
// channel == -1.
func (chunk *Chunk) Play(channel, loops int) (int, error) {
	return chunk.PlayTimed(channel, loops, -1)
}

// PlayTimed plays chunk on channel for at most ticks milliseconds.
func (chunk *Chunk) PlayTimed(channel, loops, ticks int) (int, error) {
	_chunk := (*C.Mix_Chunk)(unsafe.Pointer(chunk))
	channel_ := int(C.Mix_PlayChannelTimed(C.int(channel), _chunk, C.int(loops), C.int(ticks)))
	if channel_ == -1 {
		return channel_, sdl.GetError()
	}
	return channel_, nil
}

// Volume sets the volume for any allocated channel. If channel is -1 then all
// channels are set at once.
func Volume(channel, volume int) int {
	return int(C.Mix_Volume(C.int(channel), C.int(volume)))
}

// Volume sets the chunk's volume.
func (chunk *Chunk) Volume(volume int) int {
	_chunk := (*C.Mix_Chunk)(unsafe.Pointer(chunk))
	return int(C.Mix_VolumeChunk(_chunk, C.int(volume)))
}

// VolumeMusic sets the music volume.
func VolumeMusic(volume int) int {
	return int(C.Mix_VolumeMusic(C.int(volume)))
}

// HaltChannel halts playback on a specific channel.
func HaltChannel(channel int) {
	C.Mix_HaltChannel(C.int(channel))
}

// HaltMusic halts music playback.
func HaltMusic() {
	C.Mix_HaltMusic()
}

// FadeOutMusic fades out the current music over ms milliseconds.
func FadeOutMusic(ms int) bool {
	return int(C.Mix_FadeOutMusic(C.int(ms))) != 0
}

// FadingMusic returns the current fading state of the music stream.
func FadingMusic() Fading {
	return Fading(C.Mix_FadingMusic())
}

// PauseMusic pauses the music.
func PauseMusic() {
	C.Mix_PauseMusic()
}

// ResumeMusic resumes paused music.
func ResumeMusic() {
	C.Mix_ResumeMusic()
}

// PausedMusic reports whether the music is paused.
func PausedMusic() bool {
	return int(C.Mix_PausedMusic()) != 0
}

// Playing reports whether the given channel is playing.
func Playing(channel int) int {
	return int(C.Mix_Playing(C.int(channel)))
}

// PlayingMusic reports whether music is currently playing.
func PlayingMusic() bool {
	return int(C.Mix_PlayingMusic()) != 0
}

// GetChunk returns the chunk currently playing on channel, or nil.
func GetChunk(channel int) *Chunk {
	return (*Chunk)(unsafe.Pointer(C.Mix_GetChunk(C.int(channel))))
}

// SetMusicPosition seeks the currently playing music to position (seconds).
func SetMusicPosition(position int64) error {
	if C.Mix_SetMusicPosition(C.double(position)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// SetMusicPositionPrecise seeks the currently playing music to an exact
// sub-second position (seconds).
func SetMusicPositionPrecise(sec float64) error {
	if C.Mix_SetMusicPosition(C.double(sec)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// Duration returns the duration of this music stream in seconds, or -1 if
// unknown.
func (music *Music) Duration() float64 {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	return float64(C.Mix_MusicDuration(_music))
}

// Position returns the current playback position of this music stream in
// seconds, or -1 if unknown.
func (music *Music) Position() float64 {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	return float64(C.Mix_GetMusicPosition(_music))
}

// PlayingStream reports whether this specific music stream is currently
// playing. Unlike PlayingMusic (which checks the global Mix_Music slot), this
// checks the stream itself; required for multi-music (PlayStream) playback,
// which never populates the global slot.
func (music *Music) PlayingStream() bool {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	return int(C.Mix_PlayingMusicStream(_music)) != 0
}

// Play plays the loaded music loops times through from start to finish.
func (music *Music) Play(loops int) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_PlayMusic(_music, C.int(loops)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// FadeIn fades in the loaded music over ms milliseconds, playing it loops
// times through.
func (music *Music) FadeIn(loops, ms int) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_FadeInMusic(_music, C.int(loops), C.int(ms)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// SetLoopStartTime sets the loop start position (in seconds) for this music
// stream. Returns an error if loop points are not supported for this codec.
// This is an SDL Mixer X extension added by AsyncAO's fork.
func (music *Music) SetLoopStartTime(time float64) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_SetMusicLoopStartTime(_music, C.double(time)) < 0 {
		return sdl.GetError()
	}
	return nil
}

// SetLoopEndTime sets the loop end position (in seconds) for this music
// stream. Returns an error if loop points are not supported for this codec.
// This is an SDL Mixer X extension added by AsyncAO's fork.
func (music *Music) SetLoopEndTime(time float64) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_SetMusicLoopEndTime(_music, C.double(time)) < 0 {
		return sdl.GetError()
	}
	return nil
}

// PlayStream plays this music object as an independent stream (SDL Mixer X
// multi-music support). Unlike Play, it can coexist with other playing streams
// — this is what makes a true crossfade possible.
func (music *Music) PlayStream(loops int) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_PlayMusicStream(_music, C.int(loops)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// FadeInStream fades in this music object as an independent stream over ms
// milliseconds.
func (music *Music) FadeInStream(loops, ms int) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_FadeInMusicStream(_music, C.int(loops), C.int(ms)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// FadeOutStream fades out this music object over ms milliseconds, then halts it.
func (music *Music) FadeOutStream(ms int) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_FadeOutMusicStream(_music, C.int(ms)) == -1 {
		return sdl.GetError()
	}
	return nil
}

// HaltStream halts this music object's stream immediately.
func (music *Music) HaltStream() {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	C.Mix_HaltMusicStream(_music)
}

// SetVolumeStream sets the volume of this music object's stream.
func (music *Music) SetVolumeStream(volume int) int {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	return int(C.Mix_VolumeMusicStream(_music, C.int(volume)))
}

// FadingStream returns the current fading state of this music object's stream.
func (music *Music) FadingStream() Fading {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	return Fading(C.Mix_FadingMusicStream(_music))
}

// SeekStream seeks this music object's stream to position (seconds).
func (music *Music) SeekStream(position float64) error {
	_music := (*C.Mix_Music)(unsafe.Pointer(music))
	if C.Mix_SetMusicPositionStream(_music, C.double(position)) == -1 {
		return sdl.GetError()
	}
	return nil
}
