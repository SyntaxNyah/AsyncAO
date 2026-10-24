package main

// Windows executable icon.
// Because Windows owns the executable icon, internal/ui.SetWindowIcon is a no-op
// there (mascot_icon_windows.go) — handing SDL one big surface would override this
// crisp multi-size resource with a poor downscale.
// Both mayo.ico and rsrc_windows.syso are committed so a normal build needs no
// extra tooling.
