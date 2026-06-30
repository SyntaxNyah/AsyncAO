//go:build novoice

// The -tags novoice build compiles out AsyncAO's voice chat entirely (the
// LemmyAO/Nyathena VS_* relay + this Opus codec). Nothing imports this package
// in that build — the UI's voice surface is stubbed in internal/ui and the
// session reducer's VS_* handling is stubbed in internal/courtroom — so this
// file exists only to keep the package non-empty for `go test ./...`.
//
// Opus MUSIC playback is unaffected: that decodes through SDL_mixer
// (internal/render), which links libopusfile/libopus independently of this
// package's libopus binding. See internal/render/audio.go.
package voice
