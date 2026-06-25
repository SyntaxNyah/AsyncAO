//go:build windows

package ui

import "github.com/veandco/go-sdl2/sdl"

// SetWindowIcon is a no-op on Windows. The executable embeds a multi-resolution
// icon resource (cmd/asyncao/rsrc_windows.syso, built from mayo.ico), which
// Windows uses for the .exe in Explorer, the taskbar, and the title bar — picking
// the exact pixel size crisply at any DPI. Calling SDL_SetWindowIcon with one
// large surface would OVERRIDE that resource with a single badly downscaled icon
// (the pixelated-taskbar bug), so we deliberately leave the resource in charge.
func SetWindowIcon(win *sdl.Window) {}
