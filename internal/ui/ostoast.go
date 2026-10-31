package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// osToastMinInterval rate-limits only the OS popup, so a chatty friend can't storm the notification centre.
const osToastMinInterval = 4 * time.Second

// showOSToast is best-effort: a failure stays silent, and the in-app toast is the fallback.
func showOSToast(title, body string) {
	_ = sdl.ShowNotification(title, body)
}
