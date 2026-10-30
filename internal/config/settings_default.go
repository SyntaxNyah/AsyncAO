package config

// Defaults for the sentinel getters, in one place.
const (
	defaultChromeTheme = "dark"
	defaultVoiceOutVol = 100
)

// orDefault resolves a zero sentinel to def; never at load, or Save pins today's default into the file.
func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}
