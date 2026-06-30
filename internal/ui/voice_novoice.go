//go:build novoice

package ui

import "github.com/veandco/go-sdl2/sdl"

// Voice chat is compiled out in the -tags novoice build. These stubs stand in
// for the real surface (voicepanel.go / voiceaudio.go / settings_voice.go, all
// !novoice) so the shared courtroom UI still compiles, while every voice
// affordance is inert: voiceOfferable() is always false, so the Voice button,
// the Extras "Voice" entry, and the floating voice panel never appear, and the
// Settings sidebar omits the Voice tab (voiceBuilt == false).
//
// Opus MUSIC is unaffected — it plays through SDL_mixer (internal/render),
// independent of voice chat. See internal/render/audio.go.

// voiceBuilt is false here (see the !novoice counterpart in voicepanel.go): the
// Settings sidebar + search drop the Voice tab when this is false.
const voiceBuilt = false

// voiceExtraLabel keeps the Extras-list entry and its skip guard compiling; the
// guard (voiceOfferable, always false) means the entry is never shown.
const voiceExtraLabel = "Voice (Nyathena)"

// voiceEngine is the inert stand-in for the live capture/playback engine; the
// App.voiceAudio field keeps a type but stays nil in this build.
type voiceEngine struct{}

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

// stopVoiceAudio has nothing to tear down here. It reads the inert App voice
// fields (and thus the voiceEngine stub type) so they don't register as unused
// in the voice-free build; they back the live engine in voiceaudio.go for the
// voice-enabled build.
func (a *App) stopVoiceAudio() { _, _ = a.voiceAudio, a.voiceIconAsk }
