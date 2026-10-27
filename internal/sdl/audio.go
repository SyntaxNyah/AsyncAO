package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

type (
	AudioDeviceID = sdl2.AudioDeviceID
	AudioSpec     = sdl2.AudioSpec
)

const (
	AUDIO_S16SYS = sdl2.AUDIO_S16SYS
)

func ClearQueuedAudio(dev AudioDeviceID) {
	sdl2.ClearQueuedAudio(dev)
}

func CloseAudioDevice(dev AudioDeviceID) {
	sdl2.CloseAudioDevice(dev)
}

func DequeueAudio(dev AudioDeviceID, data []byte) (n int, err error) {
	return sdl2.DequeueAudio(dev, data)
}

func GetAudioDeviceName(index int, isCapture bool) string {
	return sdl2.GetAudioDeviceName(index, isCapture)
}

func GetNumAudioDevices(isCapture bool) int {
	return sdl2.GetNumAudioDevices(isCapture)
}

func GetQueuedAudioSize(dev AudioDeviceID) uint32 {
	return sdl2.GetQueuedAudioSize(dev)
}

func OpenAudioDevice(device string, isCapture bool, desired, obtained *AudioSpec, allowedChanges int) (AudioDeviceID, error) {
	return sdl2.OpenAudioDevice(device, isCapture, desired, obtained, allowedChanges)
}

func PauseAudioDevice(dev AudioDeviceID, pauseOn bool) {
	sdl2.PauseAudioDevice(dev, pauseOn)
}

func QueueAudio(dev AudioDeviceID, data []byte) error {
	return sdl2.QueueAudio(dev, data)
}
