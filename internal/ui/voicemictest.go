//go:build !novoice

package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/voice"
)

// Mic test: the Voice-settings "Test microphone" tool. It opens the chosen capture device without joining a voice
// channel, drives a level meter, and optionally loops the mic back to the speakers. Main-thread only (SDL queue API),
// driven by pump() from the frame loop. It reuses voiceFrameBytes / voicePlayTargetFrames from voiceaudio.go so the
// framing matches the real engine.

const (
	// micMeterRelease is the per-frame decay of the meter: fast attack, slow release.
	micMeterRelease = 0.20
	// micMeterSidetoneCapFrames bounds the sidetone loopback buffer so it stays low-latency instead of echoing.
	micMeterSidetoneCapFrames = 2
)

// micTester owns the capture device (and a playback device while sidetone is on); nil when no test is running.
type micTester struct {
	capture  *sdl.AudioStream
	playback *sdl.AudioStream // nil unless sidetone is on
	buf      []byte           // reusable dequeue scratch (one frame)
	level    float64          // smoothed 0..1 input level for the meter
	sidetone bool
}

// startMicTest opens the chosen mic (and an output device if sidetone) and begins the test. An open failure leaves
// the tester nil and is never fatal. Re-opens cleanly if a test was already running.
func (a *App) startMicTest(sidetone bool) {
	a.stopMicTest() // never double-open the device
	spec := sdl.AudioSpec{
		Freq:     voice.SampleRate,
		Format:   sdl.AUDIO_S16SYS,
		Channels: voice.Channels,
	}
	cap, err := sdl.OpenAudioDeviceStream(a.d.Prefs.Load().VoiceInputDevice, true, &spec)
	if err != nil {
		a.pushDebug("mic test: no capture device available")
		return
	}
	mt := &micTester{capture: cap, buf: make([]byte, voiceFrameBytes), sidetone: sidetone}
	if sidetone {
		if play, err := sdl.OpenAudioDeviceStream("", false, &spec); err == nil {
			mt.playback = play
			play.Resume()
		}
	}
	cap.Resume() // start recording
	a.micTest = mt
}

// stopMicTest closes the test devices (idempotent) so the mic never stays open in the background.
func (a *App) stopMicTest() {
	if a.micTest == nil {
		return
	}
	a.micTest.close()
	a.micTest = nil
}

func (mt *micTester) close() {
	if mt == nil {
		return
	}
	if mt.capture != nil {
		mt.capture.Destroy()
	}
	if mt.playback != nil {
		mt.playback.Destroy()
	}
}

// micTestPump drains capture frames, updates the smoothed meter level and, with sidetone, loops them to the speakers.
func (a *App) micTestPump() {
	mt := a.micTest
	if mt == nil || mt.capture == nil {
		return
	}
	got := false
	for n := 0; n < voiceCaptureDrainCap && mt.capture.Available() >= voiceFrameBytes; n++ {
		read, err := mt.capture.Get(mt.buf)
		if err != nil || read < voiceFrameBytes {
			break
		}
		got = true
		lvl := micLevel(bytesAsI16(mt.buf))
		if lvl > mt.level { // fast attack
			mt.level = lvl
		} else { // slow release
			mt.level += (lvl - mt.level) * micMeterRelease
		}
		if mt.playback != nil && mt.playback.Queued() < micMeterSidetoneCapFrames*voiceFrameBytes {
			_ = mt.playback.Put(mt.buf)
		}
	}
	if !got { // no input this frame → let the meter fall toward zero
		mt.level *= micMeterRelease
	}
}

// micMeterLevel reports the smoothed input level (0..1), or 0 when no test is running.
func (a *App) micMeterLevel() float64 {
	if a.micTest == nil {
		return 0
	}
	return a.micTest.level
}

// micLevel returns a PCM frame's peak amplitude as 0..1.
func micLevel(frame []int16) float64 {
	peak := 0
	for _, s := range frame {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return float64(peak) / 32768
}

// drawMicMeter paints a left-to-right level bar (green to red near clipping) with a thin frame; level is 0..1.
func (a *App) drawMicMeter(x, y, w int32, level float64) {
	c := a.ctx
	const meterH = 14
	frame := sdl.Rect{X: x, Y: y, W: w, H: meterH}
	c.Fill(frame, ColPanel)
	c.Border(frame, ColPanelHi)
	if level <= 0 {
		return
	}
	if level > 1 {
		level = 1
	}
	fillW := int32(float64(w-2) * level)
	col := ColTierGreen
	switch {
	case level > 0.92:
		col = sdl.Color{R: 220, G: 70, B: 60, A: 255} // clipping
	case level > 0.75:
		col = sdl.Color{R: 220, G: 180, B: 60, A: 255} // hot
	}
	c.Fill(sdl.Rect{X: x + 1, Y: y + 1, W: fillW, H: meterH - 2}, col)
}
