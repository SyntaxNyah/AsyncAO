package ui

import "time"

// osToastMinInterval rate-limits only the OS popup, so a chatty friend can't storm the notification centre.
const osToastMinInterval = 4 * time.Second

// showOSToast is best-effort: the in-app toast is the fallback, and tests run without a notifier.
func (a *App) showOSToast(title, body string) {
	if a.d.Toasts != nil {
		a.d.Toasts.Show(title, body)
	}
}
