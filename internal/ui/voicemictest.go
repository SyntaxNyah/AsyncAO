//go:build !novoice

package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/voice"
)

// Mic test (#84): the Voice-settings "Test microphone" tool. It opens the chosen
// capture device INDEPENDENTLY of being in a voice channel — so you can check your
// mic and levels before you ever Join — measures the live input level for a meter,
// and optionally loops the mic back to the speakers so you can HEAR yourself
// (sidetone). Main-thread only (SDL queue API), driven by pump() from the frame loop
// exactly like the live engine; nil/inactive ⇒ ~free.
//
// It deliberately reuses voiceFrameBytes / voiceDeviceSamples / voicePlayTargetFrames
// from voiceaudio.go so the test path matches the real engine's framing.

const (
	// micMeterRelease is the per-frame decay the meter eases toward a lower level with,
	// so the bar jumps to your voice (fast attack) then falls smoothly (slow release).
	micMeterRelease = 0.20
	// micMeterSidetoneCapFrames bounds the sidetone loopback buffer (~2 frames ≈ 40 ms)
	// so "hear yourself" stays low-latency instead of building an echo.
	micMeterSidetoneCapFrames = 2
)

// micTester owns the capture device (and a playback device while sidetone is on) for
// the settings mic test. nil when no test is running.
type micTester struct {
	capture  sdl.AudioDeviceID
	playback sdl.AudioDeviceID // 0 unless sidetone is on
	buf      []byte            // reusable dequeue scratch (one frame)
	level    float64           // smoothed 0..1 input level for the meter
	sidetone bool
}

// startMicTest opens the chosen mic (and, if sidetone, an output device for loopback)
// and begins the test. Fail-safe: any open failure leaves the tester nil (inactive)
// and is never fatal. Re-opens cleanly if a test was already running.
func (a *App) startMicTest(sidetone bool) {
	a.stopMicTest() // never double-open the device
	spec := sdl.AudioSpec{
		Freq:     voice.SampleRate,
		Format:   sdl.AUDIO_S16SYS,
		Channels: voice.Channels,
		Samples:  voiceDeviceSamples,
	}
	cap, err := sdl.OpenAudioDevice(a.d.Prefs.VoiceInput(), true, &spec, nil, 0)
	if err != nil {
		a.pushDebug("mic test: no capture device available")
		return
	}
	mt := &micTester{capture: cap, buf: make([]byte, voiceFrameBytes), sidetone: sidetone}
	if sidetone {
		if play, err := sdl.OpenAudioDevice("", false, &spec, nil, 0); err == nil {
			mt.playback = play
			sdl.PauseAudioDevice(play, false)
		}
	}
	sdl.PauseAudioDevice(cap, false) // start recording
	a.micTest = mt
}

// stopMicTest closes the test devices (idempotent). Called when the user stops the
// test, leaves the Voice settings tab, or exits Settings — so the mic never stays
// open in the background.
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
	if mt.capture != 0 {
		sdl.PauseAudioDevice(mt.capture, true)
		sdl.CloseAudioDevice(mt.capture)
	}
	if mt.playback != 0 {
		sdl.PauseAudioDevice(mt.playback, true)
		sdl.CloseAudioDevice(mt.playback)
	}
}

// micTestPump drains available capture frames, updates the smoothed meter level, and
// (if sidetone) loops them to the speakers. Bounded work per frame; no-op when no test
// is running. Mirrors voiceEngine.pump's framing so the test reflects the real path.
func (a *App) micTestPump() {
	mt := a.micTest
	if mt == nil || mt.capture == 0 {
		return
	}
	got := false
	for n := 0; n < voiceCaptureDrainCap && sdl.GetQueuedAudioSize(mt.capture) >= voiceFrameBytes; n++ {
		read, err := sdl.DequeueAudio(mt.capture, mt.buf)
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
		if mt.playback != 0 && sdl.GetQueuedAudioSize(mt.playback) < micMeterSidetoneCapFrames*voiceFrameBytes {
			_ = sdl.QueueAudio(mt.playback, mt.buf)
		}
	}
	if !got { // no input this frame → let the meter fall toward zero
		mt.level *= micMeterRelease
	}
}

// micMeterLevel reports the current smoothed input level (0..1), or 0 when no test is
// running — read by the settings meter draw.
func (a *App) micMeterLevel() float64 {
	if a.micTest == nil {
		return 0
	}
	return a.micTest.level
}

// micLevel returns a PCM frame's peak amplitude as 0..1. Pure (no SDL) — unit-tested.
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

// drawMicMeter paints a left-to-right level bar (green → yellow → red as it nears
// clipping) with a thin frame. level is 0..1.
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
