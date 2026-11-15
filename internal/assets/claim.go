package assets

import (
	"bytes"
	"encoding/binary"
	"image/jpeg"
)

const (
	// imageClaimLimit bounds the decoded pixels background decodes hold before the render thread uploads them.
	imageClaimLimit = 64 << 20
	// unknownImageClaim stands in for a payload whose header can't be read: one 1920x1080 frame.
	unknownImageClaim = 1920 * 1080 * rgbaBytesPerPixel

	pngIHDRDataOffset = pngSigLen + pngChunkHdrLen
	pngIHDRDimsLen    = 8
	pngACTLFramesLen  = 4

	gifScreenOffset   = 6
	gifScreenLen      = 7
	gifFlagsOffset    = 4
	gifTableFlag      = 0x80
	gifTableSizeMask  = 0x07
	gifTableEntryLen  = 3
	gifExtension      = 0x21
	gifImage          = 0x2C
	gifExtHeaderLen   = 2
	gifImageHeaderLen = 10
	gifLZWCodeLen     = 1
)

// decodeClaim estimates the memory a decode of data holds, from its header: canvas bytes times the frames kept.
func decodeClaim(data []byte, playAnimations bool) int64 {
	w, h, frames, ok := peekImage(data)
	if !ok {
		return unknownImageClaim
	}
	if !playAnimations || frames < 1 {
		frames = 1
	}
	canvas := int64(w) * int64(h) * rgbaBytesPerPixel
	claim := canvas * int64(frames)
	if budget := maxAnimatedDecodedAssetBytes.Load(); frames > 1 && budget > 0 && claim > budget {
		claim = max(budget, canvas) // the decoder downscales or drops frames to fit the budget
	}
	return claim
}

// peekImage reads an image's canvas size and frame count without decoding pixels.
func peekImage(data []byte) (width, height, frames int, ok bool) {
	switch Sniff(data) {
	case FormatPNG, FormatAPNG:
		return peekPNG(data)
	case FormatGIF:
		return peekGIF(data)
	case FormatJPEG:
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		return cfg.Width, cfg.Height, 1, err == nil
	case FormatWebP, FormatWebPAnim:
		return peekWebPAnimDims(data)
	case FormatAVIF, FormatAVIFAnim:
		return peekAVIFAnimDims(data)
	default:
		return 0, 0, 0, false
	}
}

// peekPNG reads IHDR's size and, for APNG, acTL's frame count.
func peekPNG(data []byte) (width, height, frames int, ok bool) {
	if len(data) < pngIHDRDataOffset+pngIHDRDimsLen {
		return 0, 0, 0, false
	}
	width = int(binary.BigEndian.Uint32(data[pngIHDRDataOffset:]))
	height = int(binary.BigEndian.Uint32(data[pngIHDRDataOffset+4:]))
	frames = 1
	for offset := pngSigLen; offset+pngChunkHdrLen <= len(data); {
		length := int(binary.BigEndian.Uint32(data[offset:]))
		body := offset + pngChunkHdrLen
		next := body + length + pngChunkCRCLen
		if length < 0 || next <= offset || next > len(data) {
			break
		}
		chunk := string(data[offset+4 : body])
		if chunk == "acTL" && length >= pngACTLFramesLen {
			frames = int(binary.BigEndian.Uint32(data[body:]))
			break
		}
		if chunk == "IDAT" {
			break
		}
		offset = next
	}
	return width, height, frames, width > 0 && height > 0
}

// peekGIF reads the logical screen size and counts image descriptors by skipping each block's data.
func peekGIF(data []byte) (width, height, frames int, ok bool) {
	if len(data) < gifScreenOffset+gifScreenLen {
		return 0, 0, 0, false
	}
	width = int(binary.LittleEndian.Uint16(data[gifScreenOffset:]))
	height = int(binary.LittleEndian.Uint16(data[gifScreenOffset+2:]))
	offset := gifScreenOffset + gifScreenLen + gifTableLen(data[gifScreenOffset+gifFlagsOffset])
	for offset < len(data) {
		switch data[offset] {
		case gifExtension:
			offset = skipGIFSubBlocks(data, offset+gifExtHeaderLen)
		case gifImage:
			if offset+gifImageHeaderLen > len(data) {
				return width, height, max(frames, 1), width > 0 && height > 0
			}
			frames++
			offset += gifImageHeaderLen + gifTableLen(data[offset+gifImageHeaderLen-1])
			offset = skipGIFSubBlocks(data, offset+gifLZWCodeLen)
		default: // the trailer, or damage: what was counted stands
			return width, height, max(frames, 1), width > 0 && height > 0
		}
	}
	return width, height, max(frames, 1), width > 0 && height > 0
}

// gifTableLen is the size of the colour table a screen or image descriptor's flags byte declares.
func gifTableLen(flags byte) int {
	if flags&gifTableFlag == 0 {
		return 0
	}
	return gifTableEntryLen << (int(flags&gifTableSizeMask) + 1)
}

// skipGIFSubBlocks returns the offset after the sub-block run starting at offset, or len(data) if it runs off the end.
func skipGIFSubBlocks(data []byte, offset int) int {
	for offset < len(data) {
		n := int(data[offset])
		offset++
		if n == 0 {
			return offset
		}
		offset += n
	}
	return len(data)
}
