//go:build !cgo || nocgo_avif

package assets

// No pure-Go AVIF decoder exists worth shipping (the WASM-based ones drag
// a runtime in); CGO-less builds surface a descriptive error so the
// warning UI can say why instead of showing a silent black box.

import "fmt"

func decodeAVIF(data []byte, playAnimations bool) (*Decoded, error) {
	_ = playAnimations
	return nil, fmt.Errorf("assets: avif (%d bytes) requires the cgo build (libavif); rebuild with CGO_ENABLED=1", len(data))
}
