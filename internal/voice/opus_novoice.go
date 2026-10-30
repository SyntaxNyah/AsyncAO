//go:build novoice

// The novoice build compiles voice chat out; this file only keeps the package non-empty for `go test ./...`.
// Opus music is unaffected, since it decodes through SDL_mixer with its own libopus.
package voice
