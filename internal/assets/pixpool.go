package assets

import "sync"

// Pixel-buffer pooling: decoded frame composition draws into pooled RGBA
// backing arrays sized by class, released by the render thread after texture
// upload (Decoded.Release). This keeps the decode pool's steady-state
// allocation churn near zero for the common sprite sizes.

// pixClassSizes are the pooled buffer capacities. AO sprites are typically
// 256×192×4 ≈ 200 KiB; backgrounds up to 1920×1080×4 ≈ 8 MiB.
var pixClassSizes = [...]int{256 << 10, 1 << 20, 4 << 20, 16 << 20}

var pixPools = func() [len(pixClassSizes)]*sync.Pool {
	var pools [len(pixClassSizes)]*sync.Pool
	for i, size := range pixClassSizes {
		size := size
		pools[i] = &sync.Pool{New: func() any {
			buf := make([]byte, size)
			return &buf
		}}
	}
	return pools
}()

// getPixBuf returns a zeroed pixel buffer with len ≥ n from the smallest
// fitting class, or a plain allocation when n exceeds every class. The
// second return is the pool token to hand to putPixBuf (nil when unpooled).
func getPixBuf(n int) ([]byte, *[]byte) {
	for i, size := range pixClassSizes {
		if n <= size {
			token := pixPools[i].Get().(*[]byte)
			buf := (*token)[:n]
			clearPix(buf)
			return buf, token
		}
	}
	return make([]byte, n), nil
}

// putPixBuf returns a pooled buffer to its class. nil tokens (unpooled
// oversize buffers) are ignored.
func putPixBuf(token *[]byte) {
	if token == nil {
		return
	}
	n := cap(*token)
	for i, size := range pixClassSizes {
		if n == size {
			pixPools[i].Put(token)
			return
		}
	}
}

// clearPix zeroes a recycled buffer so transparent regions stay transparent.
func clearPix(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
