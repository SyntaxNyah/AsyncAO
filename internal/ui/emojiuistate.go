package ui

import "github.com/veandco/go-sdl2/sdl"

// emojiUIState owns emoji state.
type emojiUIState struct {
	// #M2 S1: the IC-bar emoji picker (local; insert emoji into your message).
	// showEmojiPicker toggles the grid overlay; emojiBtnRect anchors it above the button.
	// emojiFenceOn tracks that WE set the modal fence so it's RELEASED when the picker
	// closes — modalOn persists across frames, so an un-released fence freezes the UI.
	showEmojiPicker bool
	emojiFenceOn    bool
	emojiBtnRect    sdl.Rect
}
