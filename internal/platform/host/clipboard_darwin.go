package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// CopyFileToClipboard does nothing here: SDL maps MIME types to pasteboard types itself, and whether Finder accepts the
// result as a file is unconfirmed. The CopyFilesToClipboard capability is off.
func (Desktop) CopyFileToClipboard(*sdl.Window, string) bool { return false }
