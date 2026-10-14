package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// prefBinding binds a Settings field's get and set for a settings-panel row. The get
// accessor is a func(*config.Settings) T (a field selector, no closure per read); the set
// is the existing setter method. Build once at row construction so the per-frame read is
// just accessor(p.Load()) with no per-frame allocation.
type prefBinding[T any] struct {
	p   *config.AssetPreferences
	get func(*config.Settings) T
	set func(T)
}

func bindPref[T any](p *config.AssetPreferences, get func(*config.Settings) T, set func(T)) prefBinding[T] {
	return prefBinding[T]{p: p, get: get, set: set}
}

func (b prefBinding[T]) Get() T  { return b.get(b.p.Load()) }
func (b prefBinding[T]) Set(v T) { b.set(v) }

// prefBool / prefInt / prefString bind a scalar field by name-shape, so a row can spell
// just the field selector and the setter.
func prefBool(p *config.AssetPreferences, get func(*config.Settings) bool, set func(bool)) prefBinding[bool] {
	return bindPref(p, get, set)
}

func prefInt(p *config.AssetPreferences, get func(*config.Settings) int, set func(int)) prefBinding[int] {
	return bindPref(p, get, set)
}

func prefString(p *config.AssetPreferences, get func(*config.Settings) string, set func(string)) prefBinding[string] {
	return bindPref(p, get, set)
}
