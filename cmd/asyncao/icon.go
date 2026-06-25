package main

// Windows executable icon.
//
// The .exe icon (shown in Explorer, the taskbar and the title bar) comes from
// rsrc_windows.syso in this directory — the Go toolchain automatically links any
// *.syso it finds in the main package on Windows, so no build flag is needed.
// rsrc_windows.syso embeds mayo.ico, a multi-resolution icon (16–256 px) built
// from the Mayo mascot art (internal/ui/assets/mayo.png — see internal/ui/mascot.go).
//
// Because Windows owns the executable icon, internal/ui.SetWindowIcon is a no-op
// there (mascot_icon_windows.go) — handing SDL one big surface would override this
// crisp multi-size resource with a poor downscale.
//
// To regenerate after changing the mascot art (needs ImageMagick + Go):
//
//	magick internal/ui/assets/mayo.png -define icon:auto-resize=256,128,96,64,48,40,32,24,20,16 cmd/asyncao/mayo.ico
//	go run github.com/akavel/rsrc@latest -ico cmd/asyncao/mayo.ico -o cmd/asyncao/rsrc_windows.syso
//
// Both mayo.ico and rsrc_windows.syso are committed so a normal build needs no
// extra tooling.
