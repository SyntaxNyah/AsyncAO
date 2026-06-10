//go:build !cgo || nocgo_webp

package assets

// Pure-Go WebP fallback for CGO-less builds (PROMPT.md §2):
// golang.org/x/image/webp decodes static payloads only. Animated WebP needs
// libwebpdemux — surface a descriptive error so the warning UI can say why.

import (
	"bytes"
	"fmt"

	"golang.org/x/image/webp"
)

func decodeWebP(data []byte, playAnimations bool) (*Decoded, error) {
	if Sniff(data) == FormatWebPAnim {
		return nil, fmt.Errorf("assets: animated webp requires the cgo build (libwebp); rebuild with CGO_ENABLED=1 or ask for a static asset")
	}
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("assets: webp decode (pure-Go fallback): %w", err)
	}
	_ = playAnimations
	return staticDecoded(img), nil
}
