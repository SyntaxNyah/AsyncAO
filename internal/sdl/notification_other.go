//go:build !windows

package sdl

import "errors"

// ShowNotification stands in for SDL 3.6's call of the same name, which has no implementation here until then.
func ShowNotification(_, _ string) error {
	return errors.New("sdl: notifications need SDL 3.6 on this platform")
}
