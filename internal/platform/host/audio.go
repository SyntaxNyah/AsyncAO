package host

import (
	"errors"
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/mix"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	mixerFrequency = 48000
	mixerChannels  = 2
)

// ErrNoCaptureDevice means the machine has no microphone to open.
var ErrNoCaptureDevice = errors.New("no capture device")

// AudioDevices opens the sound hardware; the caller owns and closes what it gets.
type AudioDevices struct{}

// OpenMixer opens the default playback device for music and sound effects.
func (AudioDevices) OpenMixer() (*mix.Mixer, error) {
	return mix.OpenDevice(mixerFrequency, mixerChannels)
}

// OpenPlayback opens the default playback device as a paused stream in spec's format.
func (AudioDevices) OpenPlayback(spec *sdl.AudioSpec) (*sdl.AudioStream, error) {
	return sdl.OpenAudioDeviceStream("", false, spec)
}

// OpenCapture opens the named microphone ("" = the system default) as a paused stream; call it only on a user's action,
// since opening is what asks the OS for microphone permission.
func (d AudioDevices) OpenCapture(device string, spec *sdl.AudioSpec) (*sdl.AudioStream, error) {
	stream, err := sdl.OpenAudioDeviceStream(device, true, spec)
	if err == nil {
		return stream, nil
	}
	if len(d.CaptureDeviceNames()) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrNoCaptureDevice, err)
	}
	return nil, err
}

// CaptureDeviceNames lists the microphones by name.
func (AudioDevices) CaptureDeviceNames() []string { return sdl.GetAudioDeviceNames(true) }
