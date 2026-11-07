//go:build !windows

package toast

// noSender drops every notification: only Windows has an implementation until SDL 3.6's SDL_ShowNotification.
type noSender struct{}

func newSender(App) sender { return noSender{} }

func (noSender) send(_, _ string) error { return nil }
func (noSender) close()                 {}
