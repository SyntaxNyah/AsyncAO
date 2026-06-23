package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Player-list presence badges (#M1): a short label + colour per Status, shown as a chip
// on the row and cycled by the header "Status:" button. StatusNone has no badge.

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

// statusColor is the chip background for a status — dark enough that the chip's light
// text stays legible.
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
