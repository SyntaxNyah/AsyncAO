//go:build novoice

package ui

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// Voice chat is compiled out in the -tags novoice build. These stubs stand in for the real surface (voicepanel.go,
// voiceaudio.go, settings_voice.go) so shared courtroom UI compiles; voiceOfferable() is always false, so no voice
// affordance appears and the Settings sidebar omits the Voice tab. Opus music still plays through SDL_mixer.

// voiceBuilt is false here; the Settings sidebar and search drop the Voice tab.
const voiceBuilt = false

// voiceExtraLabel keeps the Extras-list entry compiling; voiceOfferable (always false) means it is never shown.
const voiceExtraLabel = "Voice (Nyathena)"

// voiceEngine is the inert stand-in for the capture/playback engine; App.voiceAudio stays nil in this build.
type voiceEngine struct{}

// micTester is the inert stand-in for the Settings mic-test; App.micTest stays nil in this build.
type micTester struct{}

func (*App) voiceOfferable() bool                  { return false }
func (*App) voiceButtonState() (string, sdl.Color) { return "", sdl.Color{} }
func (*App) toggleVoice()                          {}
func (*App) voicePanelRect(_, _ int32) sdl.Rect    { return sdl.Rect{} }
func (*App) drawVoicePanel(_, _ int32, _ *bool)    {}
func (*App) voiceSetMic(bool)                      {}
func (*App) voicePump()                            {}
func (*App) voiceOnAudio(int, string)              {}
func (*App) voiceReconcilePeers()                  {}
func (*App) drawSettingsVoice(y, _ int32) int32    { return y }

// stopVoiceAudio has nothing to tear down here. It reads the inert App voice fields so they don't register as unused.
func (a *App) stopVoiceAudio() {
	_, _, _, _ = a.voiceAudio, a.voiceIconAsk, a.micTest, a.micTestSidetone
}
