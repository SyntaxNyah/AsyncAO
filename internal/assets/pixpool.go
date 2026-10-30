package assets

import "sync"

// Pooled pixel buffers by size class, released by the render thread after upload.

// pixClassSizes are the pooled capacities; 8 MiB fits 1600x1200 animated frames, which waste 2x in the 16 MiB class.
var pixClassSizes = [...]int{256 << 10, 1 << 20, 4 << 20, 8 << 20, 16 << 20}

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

// getPixBuf returns a zeroed buffer from the smallest fitting class, or an unpooled one with a nil token.
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

// putPixBuf returns a pooled buffer to its class; nil tokens are ignored.
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
