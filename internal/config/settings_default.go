package config

// Item-2 default constants for the sentinel getters whose defaults were previously
// magic literals. Keeping them here (one place) so "unset → default" stays greppable.
const (
	defaultChromeTheme = "dark"
	defaultVoiceOutVol = 100
)

// orDefault resolves a zero-value sentinel to its default: 0 (or "" for string
// prefs) means "unset — follow the default". It is the single shared helper behind
// the sentinel getters (item 2), so the default constants stay in one place and the
// read path is uniform.
// The sentinel is deliberately NEVER resolved at load (strict or not): writing the
// resolved default into the file would pin today's default into every user's file on
// the next Save and stop a future default change from ever reaching existing users.
func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}
