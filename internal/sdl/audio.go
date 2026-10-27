package sdl

/*
#include <SDL3/SDL.h>
*/
import "C"

import "unsafe"

// AudioFormat is a sample format.
type AudioFormat uint32

// AUDIO_S16SYS is signed 16-bit samples in native byte order.
const AUDIO_S16SYS = C.SDL_AUDIO_S16

// AudioSpec is a stream's sample format, channel count and sample rate.
type AudioSpec struct {
	Freq     int32
	Format   AudioFormat
	Channels uint8
}

// AudioStream is a queue of samples bound to one playback or recording device.
// It opens paused; Resume starts the device.
type AudioStream C.SDL_AudioStream

func (s *AudioSpec) c() C.SDL_AudioSpec {
	return C.SDL_AudioSpec{format: C.SDL_AudioFormat(s.Format), channels: C.int(s.Channels), freq: C.int(s.Freq)}
}

func audioDevices(recording bool) []C.SDL_AudioDeviceID {
	var n C.int
	var ids *C.SDL_AudioDeviceID
	if recording {
		ids = C.SDL_GetAudioRecordingDevices(&n)
	} else {
		ids = C.SDL_GetAudioPlaybackDevices(&n)
	}
	if ids == nil {
		return nil
	}
	defer C.SDL_free(unsafe.Pointer(ids))
	return append([]C.SDL_AudioDeviceID(nil), unsafe.Slice(ids, int(n))...)
}

// GetAudioDeviceNames lists the playback or recording devices by name.
func GetAudioDeviceNames(recording bool) []string {
	var names []string
	for _, id := range audioDevices(recording) {
		if name := C.SDL_GetAudioDeviceName(id); name != nil {
			names = append(names, C.GoString(name))
		}
	}
	return names
}

// OpenAudioDeviceStream opens the named device ("" = the system default) as a
// stream in spec's format; SDL converts to the device's own format.
func OpenAudioDeviceStream(device string, recording bool, spec *AudioSpec) (*AudioStream, error) {
	id := C.SDL_AudioDeviceID(C.SDL_AUDIO_DEVICE_DEFAULT_PLAYBACK)
	if recording {
		id = C.SDL_AUDIO_DEVICE_DEFAULT_RECORDING
	}
	if device != "" {
		for _, d := range audioDevices(recording) {
			if C.GoString(C.SDL_GetAudioDeviceName(d)) == device {
				id = d
				break
			}
		}
	}
	cspec := spec.c()
	s := C.SDL_OpenAudioDeviceStream(id, &cspec, nil, nil)
	if s == nil {
		return nil, errorOr(false)
	}
	return (*AudioStream)(s), nil
}

func (s *AudioStream) c() *C.SDL_AudioStream { return (*C.SDL_AudioStream)(s) }

// Put queues samples for playback.
func (s *AudioStream) Put(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return errorOr(C.SDL_PutAudioStreamData(s.c(), unsafe.Pointer(&data[0]), C.int(len(data))))
}

// Get reads recorded samples into data and returns how many bytes it read.
func (s *AudioStream) Get(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	n := C.SDL_GetAudioStreamData(s.c(), unsafe.Pointer(&data[0]), C.int(len(data)))
	if n < 0 {
		return 0, errorOr(false)
	}
	return int(n), nil
}

// Queued returns the bytes put but not yet played.
func (s *AudioStream) Queued() int { return int(C.SDL_GetAudioStreamQueued(s.c())) }

// Available returns the recorded bytes ready for Get.
func (s *AudioStream) Available() int { return int(C.SDL_GetAudioStreamAvailable(s.c())) }

// Clear drops every queued byte.
func (s *AudioStream) Clear() { C.SDL_ClearAudioStream(s.c()) }

// Resume starts the device.
func (s *AudioStream) Resume() { C.SDL_ResumeAudioStreamDevice(s.c()) }

// Pause stops the device; queued data is kept.
func (s *AudioStream) Pause() { C.SDL_PauseAudioStreamDevice(s.c()) }

// Destroy closes the device and frees the stream.
func (s *AudioStream) Destroy() { C.SDL_DestroyAudioStream(s.c()) }
