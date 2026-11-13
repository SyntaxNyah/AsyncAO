package main

import _ "embed"

// Windows executable icon. internal/ui.SetWindowIcon is a no-op on Windows, as one big SDL surface would
// override this multi-size resource with a poor downscale.

// appIcon is the same icon as a file, for desktop notifications.
//
//go:embed mayo.ico
var appIcon []byte
