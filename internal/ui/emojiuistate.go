package ui

import "github.com/veandco/go-sdl2/sdl"

// emojiUIState owns emoji state.
type emojiUIState struct {
	// #M2 S1: the IC-bar emoji picker (local; insert emoji into your message).
	showEmojiPicker bool
	emojiFenceOn    bool
	emojiBtnRect    sdl.Rect
}
