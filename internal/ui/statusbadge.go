package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Player-list presence badges: a label and colour per Status; StatusNone has no badge.

func statusLabel(s courtroom.Status) string {
	switch s {
	case courtroom.StatusAFK:
		return "AFK"
	case courtroom.StatusBusy:
		return "Busy"
	case courtroom.StatusWriting:
		return "Writing"
	case courtroom.StatusLFRP:
		return "LFRP"
	}
	return ""
}

// statusColor is the chip background, dark enough to keep the chip's light text legible.
func statusColor(s courtroom.Status) sdl.Color {
	switch s {
	case courtroom.StatusAFK:
		return sdl.Color{R: 150, G: 120, B: 40, A: 255} // amber
	case courtroom.StatusBusy:
		return sdl.Color{R: 175, G: 55, B: 55, A: 255} // red
	case courtroom.StatusWriting:
		return sdl.Color{R: 55, G: 105, B: 180, A: 255} // blue
	case courtroom.StatusLFRP:
		return sdl.Color{R: 55, G: 145, B: 70, A: 255} // green
	}
	return sdl.Color{}
}
