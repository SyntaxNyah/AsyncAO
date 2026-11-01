package host

import "github.com/SyntaxNyah/AsyncAO/internal/mix"

const (
	mixerFrequency = 48000
	mixerChannels  = 2
)

// AudioDevices opens the sound hardware; the caller owns and closes what it gets.
type AudioDevices struct{}

// OpenMixer opens the default playback device for music and sound effects.
func (AudioDevices) OpenMixer() (*mix.Mixer, error) {
	return mix.OpenDevice(mixerFrequency, mixerChannels)
}
