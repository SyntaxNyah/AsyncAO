//go:build !novoice

package ui

import (
	"strconv"

	"github.com/veandco/go-sdl2/sdl"
)

// The Voice settings tab lives here (moved out of settings.go) so the entire
// voice-settings surface compiles out of the -tags novoice build along with the
// rest of voice chat. The Settings sidebar omits the Voice tab there
// (voiceBuilt == false) and drawSettingsVoice is a no-op stub.

// drawSettingsVoice is the Voice tab: pick the microphone (system default unless
// you choose another) and set the output volume. Voice chat itself only appears
// on servers that support it (Nyathena) — the Voice button shows in a VC area.
func (a *App) drawSettingsVoice(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	y = a.settingsSection(y, w, "Microphone")
	cur := a.d.Prefs.VoiceInput()
	curLabel := cur
	if curLabel == "" {
		curLabel = "System default"
	}
	c.Label(pad, y+4, "Input device:", ColText)
	c.LabelClipped(pad+110, y+4, w-pad-110, curLabel, ColAccent)
	y += 26
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "Next device") {
		a.cycleVoiceInput()
	}
	if c.Button(sdl.Rect{X: pad + 160, Y: y, W: 150, H: btnH}, "System default") {
		a.d.Prefs.SetVoiceInput("")
	}
	y += 30
	c.Label(pad, y, "Uses your system default mic unless you pick another. Takes effect next time you Join voice.", ColTextDim)
	y += 24

	y = a.settingsSection(y, w, "Output")
	c.Label(pad, y+4, "Volume:", ColText)
	vol := int32(a.d.Prefs.VoiceOutVol())
	if nv := c.Slider("voiceOutVolSet", sdl.Rect{X: pad + 80, Y: y, W: 220, H: btnH}, vol, 100); nv != vol {
		a.d.Prefs.SetVoiceOutVol(int(nv))
		if a.voiceAudio != nil {
			a.voiceAudio.setOutVol(int(nv)) // apply live if we're in voice
		}
	}
	c.Label(pad+310, y+4, strconv.Itoa(a.d.Prefs.VoiceOutVol())+"%", ColTextDim)
	y += 30

	y = a.settingsSection(y, w, "Push-to-talk")
	c.Label(pad, y+4, "Mic toggle key:", ColText)
	keyLabel := a.d.Prefs.VoicePTT()
	if a.voicePTTBindArmed {
		keyLabel = "press a key…  (Esc cancels)"
	} else if keyLabel == "" {
		keyLabel = "(unbound)"
	}
	c.Label(pad+120, y+4, keyLabel, ColAccent)
	y += 26
	if c.Button(sdl.Rect{X: pad, Y: y, W: 130, H: btnH}, "Bind key") {
		a.voicePTTBindArmed = !a.voicePTTBindArmed
	}
	if c.Button(sdl.Rect{X: pad + 140, Y: y, W: 90, H: btnH}, "Clear") {
		a.d.Prefs.SetVoicePTT("")
		a.voicePTTBindArmed = false
	}
	y += 30
	c.Label(pad, y, "Press the bound key while in voice to toggle your mic on/off (same as the Talk button).", ColTextDim)
	y += 24
	c.Label(pad, y, "Voice chat appears on servers that support it (Nyathena) — the Voice button shows when you enter a voice-enabled area.", ColTextDim)
	y += 24
	return y
}

// cycleVoiceInput advances the chosen mic to the next capture device (wrapping
// through "System default"). Enumerated on click, so there's no per-frame SDL
// device scan.
func (a *App) cycleVoiceInput() {
	n := sdl.GetNumAudioDevices(true)
	devs := make([]string, 0, n+1)
	devs = append(devs, "") // system default first
	for i := 0; i < n; i++ {
		if name := sdl.GetAudioDeviceName(i, true); name != "" {
			devs = append(devs, name)
		}
	}
	cur := a.d.Prefs.VoiceInput()
	idx := 0
	for i, d := range devs {
		if d == cur {
			idx = i
			break
		}
	}
	a.d.Prefs.SetVoiceInput(devs[(idx+1)%len(devs)])
}
