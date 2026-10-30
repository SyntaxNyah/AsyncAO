package assets

import "encoding/binary"

// SniffAudioSampleRate reads the sample rate from a compressed audio header in memory, without I/O (hard rule 2).
// AO loop points are sample counts and our music API speaks seconds, so we need the rate. AO2-Client does not
// (aomusicplayer.cpp:92-93 never reads it; BASS positions are byte offsets), so do not remove this citing AO2.
func SniffAudioSampleRate(data []byte) (rate int, ok bool) {
	switch {
	case hasPrefix(data, []byte("RIFF")) && len(data) >= riffHeaderLen && string(data[8:12]) == "WAVE":
		return sniffWAVSampleRate(data)
	case hasPrefix(data, oggSignature):
		return sniffOggSampleRate(data)
	case hasPrefix(data, []byte("fLaC")):
		return sniffFLACSampleRate(data)
	default:
		// MP3 has no fixed magic; the bounded frame-sync scan both detects and decodes it.
		return sniffMP3SampleRate(data)
	}
}

const (
	// opusNativeSampleRate is fixed by RFC 7845 §5.1: Opus decodes at 48 kHz; never read OpusHead's input_sample_rate.
	opusNativeSampleRate = 48000

	// oggPageHeaderFixedLen is the fixed Ogg page header before the segment table (RFC 3533 §6).
	oggPageHeaderFixedLen = 27

	// flacStreamInfoOffset is "fLaC"(4) plus the 4-byte block header; STREAMINFO is always the first block.
	flacStreamInfoOffset = 8

	// id3v2HeaderLen is the fixed ID3v2 header: "ID3"(3), version(2), flags(1), size(4).
	id3v2HeaderLen = 10

	// mp3FrameSyncScanCap bounds the scan for the 11-bit frame sync, so a hostile file cannot spin the loop.
	mp3FrameSyncScanCap = 4096
)

const (
	wavChunkHdrLen = 8 // 4-byte ASCII chunk id + 4-byte little-endian size

	// wavFmtSampleRateOff is the sample rate's offset inside the "fmt " chunk's payload, not the file.
	wavFmtSampleRateOff = 4
	wavFmtMinPayload    = wavFmtSampleRateOff + 4 // enough of "fmt " to read the rate field

	// riffSizePrefixLen is "RIFF"(4) plus its size field; the declared size already counts "WAVE".
	riffSizePrefixLen = 8
)

// sniffWAVSampleRate walks RIFF chunks for "fmt ", which may be anywhere, honoring declared sizes and even padding.
func sniffWAVSampleRate(data []byte) (int, bool) {
	// An oversized or zero RIFF size falls back to the buffer length, so a mismatched but valid file still parses.
	end := len(data)
	if riffSize := int(binary.LittleEndian.Uint32(data[4:8])); riffSizePrefixLen+riffSize <= len(data) {
		end = riffSizePrefixLen + riffSize
	}
	offset := riffHeaderLen
	for offset+wavChunkHdrLen <= end {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payload := offset + wavChunkHdrLen
		if size < 0 || payload+size > len(data) {
			return 0, false // declared chunk size runs past what's actually resident
		}
		if id == "fmt " {
			if size < wavFmtMinPayload {
				return 0, false
			}
			off := payload + wavFmtSampleRateOff
			return int(binary.LittleEndian.Uint32(data[off : off+4])), true
		}
		// RIFF pads odd-length payloads; skip the pad byte or every later offset is off by one.
		next := payload + size
		if size%2 != 0 {
			next++
		}
		if next <= offset { // guards against a zero/negative stride hanging the walk
			return 0, false
		}
		offset = next
	}
	return 0, false
}

// oggPageSegCountOffset is page_segments' offset, the last byte of the fixed header.
const oggPageSegCountOffset = oggPageHeaderFixedLen - 1

// sniffOggSampleRate reads only the first Ogg page, where the codec identification header always lives.
func sniffOggSampleRate(data []byte) (int, bool) {
	if len(data) <= oggPageSegCountOffset {
		return 0, false
	}
	segments := int(data[oggPageSegCountOffset])
	payloadOff := oggPageHeaderFixedLen + segments
	if payloadOff > len(data) {
		return 0, false
	}
	payload := data[payloadOff:]

	const (
		opusHeadMagic  = "OpusHead"
		vorbisIDPacket = "\x01vorbis" // packet type 1 (identification header) + codec name
		vorbisRateOff  = 12           // LE u32 sample rate inside the Vorbis identification header
	)
	switch {
	case hasPrefix(payload, []byte(opusHeadMagic)):
		// RFC 7845 §5.1: see opusNativeSampleRate.
		return opusNativeSampleRate, true
	case hasPrefix(payload, []byte(vorbisIDPacket)):
		if len(payload) < vorbisRateOff+4 {
			return 0, false
		}
		return int(binary.LittleEndian.Uint32(payload[vorbisRateOff : vorbisRateOff+4])), true
	default:
		return 0, false
	}
}

// flacSampleRateFieldOffset skips block sizes (2+2) and frame sizes (3+3), to the bytes holding the 20-bit rate.
const flacSampleRateFieldOffset = flacStreamInfoOffset + 10

// sniffFLACSampleRate reads STREAMINFO's sample-rate field at its fixed offset.
func sniffFLACSampleRate(data []byte) (int, bool) {
	if len(data) < flacSampleRateFieldOffset+3 {
		return 0, false
	}
	// The 20-bit rate is big-endian and unaligned: top 8 bits, next 8 bits, then the third byte's top nibble.
	b0 := data[flacSampleRateFieldOffset]
	b1 := data[flacSampleRateFieldOffset+1]
	b2 := data[flacSampleRateFieldOffset+2]
	rate := int(b0)<<12 | int(b1)<<4 | int(b2)>>4
	if rate == 0 {
		return 0, false
	}
	return rate, true
}

// mp3SyncPeekLen is the header bytes read after a sync candidate: sync/version/layer and bitrate/rate index.
const mp3SyncPeekLen = 2

// mp3SampleRates maps [version][rateIndex] to Hz, with 0=MPEG1, 1=MPEG2, 2=MPEG2.5 (ISO/IEC 11172-3, 13818-3).
// Reserved version 0b01 and rate index 0b11 are absent; sniffMP3SampleRate checks both first.
var mp3SampleRates = [3][3]int{
	{44100, 48000, 32000}, // MPEG version 1
	{22050, 24000, 16000}, // MPEG version 2
	{11025, 12000, 8000},  // MPEG version 2.5
}

// sniffMP3SampleRate skips any ID3v2 tag, then decodes the first frame sync's version and rate index.
func sniffMP3SampleRate(data []byte) (int, bool) {
	start := 0
	if hasPrefix(data, id3Signature) {
		if len(data) < id3v2HeaderLen {
			return 0, false
		}
		// Bytes 6-9 are a syncsafe 28-bit size (7 bits per byte); a plain u32 over-reads past the tag.
		size := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
		start = id3v2HeaderLen + size
		if start > len(data) {
			return 0, false // tag claims to run past what's actually resident
		}
	}
	limit := start + mp3FrameSyncScanCap
	if limit > len(data) {
		limit = len(data)
	}
	for i := start; i+mp3SyncPeekLen < limit; i++ {
		if data[i] != 0xFF || data[i+1]&mp3SyncMask != mp3SyncMask {
			continue
		}
		// Byte 1 layout: sync(3) | version(2) | layer(2) | protection(1).
		switch (data[i+1] >> 3) & 0x03 {
		case 0b11: // MPEG version 1
			return mp3RateFromIndex(0, data[i+2])
		case 0b10: // MPEG version 2
			return mp3RateFromIndex(1, data[i+2])
		case 0b00: // MPEG version 2.5 (unofficial extension, still deployed)
			return mp3RateFromIndex(2, data[i+2])
		default: // 0b01 reserved
			return 0, false
		}
	}
	return 0, false
}

// mp3RateFromIndex reads byte 2's rate index: bitrate(4) | rate index(2) | padding(1) | private(1).
func mp3RateFromIndex(version int, b2 byte) (int, bool) {
	idx := (b2 >> 2) & 0x03
	if idx == 0b11 { // reserved in every MPEG version
		return 0, false
	}
	return mp3SampleRates[version][idx], true
}
