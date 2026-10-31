//go:build windows

package ui

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// SetWindowIcon is a no-op on Windows: the exe embeds a multi-resolution icon (rsrc_windows.syso),
// and SDL_SetWindowIcon's single surface would override it with one badly downscaled icon.
func SetWindowIcon(win *sdl.Window) {}
