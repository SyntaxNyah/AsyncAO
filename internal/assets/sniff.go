package assets

import "encoding/binary"

// Format is a payload format detected from magic bytes — never from the file
// extension (spec §8: servers lie, payloads don't).
type Format int

const (
	FormatUnknown Format = iota
	FormatPNG
	FormatAPNG
	FormatWebP     // static webp payload
	FormatWebPAnim // webp payload with the VP8X ANIM flag set
	FormatGIF
	FormatJPEG
	FormatOgg // Ogg container: Vorbis or Opus
	FormatWAV
	FormatMP3
)

// String names the format for logs and warnings.
func (f Format) String() string {
	switch f {
	case FormatPNG:
		return "png"
	case FormatAPNG:
		return "apng"
	case FormatWebP:
		return "webp"
	case FormatWebPAnim:
		return "webp(animated)"
	case FormatGIF:
		return "gif"
	case FormatJPEG:
		return "jpeg"
	case FormatOgg:
		return "ogg"
	case FormatWAV:
		return "wav"
	case FormatMP3:
		return "mp3"
	default:
		return "unknown"
	}
}

// IsImage reports whether the format goes through the image decode pool.
func (f Format) IsImage() bool {
	switch f {
	case FormatPNG, FormatAPNG, FormatWebP, FormatWebPAnim, FormatGIF, FormatJPEG:
		return true
	default:
		return false
	}
}

const (
	riffHeaderLen = 12 // "RIFF" + size + fourcc
	vp8xANIMFlag  = 0x02

	pngSigLen       = 8
	pngChunkHdrLen  = 8 // length + type
	pngChunkCRCLen  = 4
	mp3SyncMask     = 0xE0
	id3TagLen       = 3
	minSniffableLen = 4
)

var (
	pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	gifSignature = []byte("GIF8")
	oggSignature = []byte("OggS")
	id3Signature = []byte("ID3")
)

// Sniff classifies payload bytes by magic numbers.
func Sniff(data []byte) Format {
	if len(data) < minSniffableLen {
		return FormatUnknown
	}
	switch {
	case hasPrefix(data, pngSignature):
		if pngHasACTL(data) {
			return FormatAPNG
		}
		return FormatPNG
	case hasPrefix(data, []byte("RIFF")) && len(data) >= riffHeaderLen:
		switch string(data[8:12]) {
		case "WEBP":
			if webpHasANIM(data) {
				return FormatWebPAnim
			}
			return FormatWebP
		case "WAVE":
			return FormatWAV
		}
		return FormatUnknown
	case hasPrefix(data, gifSignature):
		return FormatGIF
	case data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return FormatJPEG
	case hasPrefix(data, oggSignature):
		return FormatOgg
	case hasPrefix(data, id3Signature):
		return FormatMP3
	case data[0] == 0xFF && (data[1]&mp3SyncMask) == mp3SyncMask:
		return FormatMP3
	default:
		return FormatUnknown
	}
}

func hasPrefix(data, prefix []byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := range prefix {
		if data[i] != prefix[i] {
			return false
		}
	}
	return true
}

// pngHasACTL scans PNG chunks for an acTL (animation control) chunk before
// the first IDAT — the defining marker of APNG.
func pngHasACTL(data []byte) bool {
	offset := pngSigLen
	for offset+pngChunkHdrLen <= len(data) {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		chunkType := string(data[offset+4 : offset+8])
		switch chunkType {
		case "acTL":
			return true
		case "IDAT", "IEND":
			return false
		}
		// Guard against corrupt lengths walking off the payload.
		next := offset + pngChunkHdrLen + length + pngChunkCRCLen
		if length < 0 || next <= offset || next > len(data) {
			return false
		}
		offset = next
	}
	return false
}

// webpHasANIM checks the VP8X extended-header ANIM flag. Plain VP8/VP8L
// payloads (no VP8X chunk) are never animated.
func webpHasANIM(data []byte) bool {
	const (
		chunkHeaderLen = 8
		vp8xFlagsIndex = riffHeaderLen + chunkHeaderLen // flags byte right after the VP8X header
	)
	if len(data) <= vp8xFlagsIndex {
		return false
	}
	if string(data[riffHeaderLen:riffHeaderLen+4]) != "VP8X" {
		return false
	}
	return data[vp8xFlagsIndex]&vp8xANIMFlag != 0
}
