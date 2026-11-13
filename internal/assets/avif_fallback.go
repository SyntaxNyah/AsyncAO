//go:build !cgo || nocgo_avif

package assets

// No pure-Go AVIF decoder is worth shipping (WASM ones add a runtime), so CGO-less builds return an error.

import "fmt"

func decodeAVIF(data []byte, playAnimations bool, maxH int) (*Decoded, error) {
	_ = playAnimations
	_ = maxH
	return nil, fmt.Errorf("assets: avif (%d bytes) requires the cgo build (libavif); rebuild with CGO_ENABLED=1", len(data))
}

func peekAVIFAnimDims(data []byte) (width, height, frames int, ok bool) {
	return 0, 0, 0, false
}
