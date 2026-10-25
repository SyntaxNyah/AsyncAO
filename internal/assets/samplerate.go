package assets

import "encoding/binary"

// SniffAudioSampleRate reports the sample rate (Hz) a compressed audio
// container's own header claims, sniffed purely from bytes already
// resident in memory — zero I/O (hard rule 2), in the same bounded,
// magic-byte-walking style as Sniff (sniff.go).
//
// AO and DRO loop points (issue #48) are expressed in SAMPLE COUNTS, but
// AsyncAO seeks in seconds — SDL_mixer's Mix_SetMusicPosition takes a
// double — so converting needs `seconds = samples / rate`, and SDL_mixer
// will not report a track's rate. AO2-Client itself never needs one:
// aomusicplayer.cpp:92-93 declares `float sample_rate;`, fills it via
// `BASS_ChannelGetAttribute(newstream, BASS_ATTRIB_FREQ, &sample_rate)`,
// and then never reads it again — BASS positions are byte offsets into a
// fixed 16-bit stereo decode (`bytes = value * sample_size(2) *
// num_channels(2)`), so its sample-to-byte math never touches the rate.
// Do not "simplify" this sniffer away by citing AO2 as precedent: this is
// the one place AsyncAO's own math genuinely needs the rate AO2's doesn't.
func SniffAudioSampleRate(data []byte) (rate int, ok bool) {
	switch {
	case hasPrefix(data, []byte("RIFF")) && len(data) >= riffHeaderLen && string(data[8:12]) == "WAVE":
		return sniffWAVSampleRate(data)
	case hasPrefix(data, oggSignature):
		return sniffOggSampleRate(data)
	case hasPrefix(data, []byte("fLaC")):
		return sniffFLACSampleRate(data)
	default:
		// MP3 has no fixed magic beyond an optional leading ID3v2 tag; a
		// bounded frame-sync scan both detects and decodes it in one pass,
		// and safely falls through to ok=false for anything that is
		// neither MP3 nor one of the containers above.
		return sniffMP3SampleRate(data)
	}
}

const (
	// opusNativeSampleRate is hardcoded per RFC 7845 §5.1: Opus ALWAYS
	// decodes at 48 kHz regardless of what OpusHead's own
	// input_sample_rate field says — that field is informational only.
	// Reading it instead of this constant is the single easiest silent
	// scaling bug in the whole loop-point feature (issue #48 decision 15).
	opusNativeSampleRate = 48000

	// oggPageHeaderFixedLen is the fixed portion of an Ogg page header
	// before its variable-length segment table (RFC 3533 §6): capture
	// pattern(4) + version(1) + header_type(1) + granule_position(8) +
	// bitstream_serial(4) + page_sequence(4) + checksum(4) +
	// page_segments(1) = 27.
	oggPageHeaderFixedLen = 27

	// flacStreamInfoOffset is "fLaC"(4) plus the 4-byte metadata block
	// header that precedes every block's payload. STREAMINFO is spec-
	// mandated to be FLAC's first metadata block, so no chunk walk is
	// needed to find it (unlike WAV/RIFF, where "fmt " can sit anywhere).
	flacStreamInfoOffset = 8

	// id3v2HeaderLen is the fixed ID3v2 header size ("ID3"(3) +
	// version(2) + flags(1) + size(4)) that precedes the syncsafe
	// tag-size field's own body.
	id3v2HeaderLen = 10

	// mp3FrameSyncScanCap bounds the forward scan for the 11-bit MP3
	// frame sync after the ID3v2 skip, so a truncated or hostile file
	// can never spin an unbounded loop (hard rule 4's spirit applied to
	// a bounded walk rather than a cap+queue).
	mp3FrameSyncScanCap = 4096
)

// ---------------------------------------------------------------------------
// WAV / RIFF
// ---------------------------------------------------------------------------

const (
	wavChunkHdrLen = 8 // 4-byte ASCII chunk id + 4-byte little-endian size

	// wavFmtSampleRateOff is the sample rate's offset inside the "fmt "
	// chunk's OWN payload (payload bytes 4-7 of the WAVEFORMAT struct),
	// not the file.
	wavFmtSampleRateOff = 4
	wavFmtMinPayload    = wavFmtSampleRateOff + 4 // enough of "fmt " to read the rate field

	// riffSizePrefixLen is "RIFF"(4) + its own 4-byte size field — the
	// point the declared RIFF size is measured FROM. That declared size
	// already counts the following "WAVE" fourcc, so the chunk-list bound
	// is riffSizePrefixLen+declaredSize, not riffHeaderLen+declaredSize
	// (which would double-count "WAVE" and make this bound impossible to
	// satisfy for any well-formed file).
	riffSizePrefixLen = 8
)

// sniffWAVSampleRate walks RIFF sub-chunks looking for "fmt "; it does not
// assume "fmt " is first, and it respects both each chunk's declared size
// and RIFF's even-padding rule (an unaware walk misreads every chunk after
// an odd-sized one).
func sniffWAVSampleRate(data []byte) (int, bool) {
	// The RIFF size at bytes 4-7 bounds the walk when it's trustworthy; an
	// oversized or zero declaration falls back to the buffer's own length
	// so a mismatched-but-otherwise-valid file still parses.
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
		// RIFF pads odd-length payloads to an even boundary; skip the pad
		// byte or every later chunk's offset is wrong by one.
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

// ---------------------------------------------------------------------------
// Ogg (Vorbis or Opus share this container)
// ---------------------------------------------------------------------------

// oggPageSegCountOffset is page_segments' own byte offset — the last byte
// of the fixed header, one before oggPageHeaderFixedLen's boundary.
const oggPageSegCountOffset = oggPageHeaderFixedLen - 1

// sniffOggSampleRate reads only the first Ogg page: the codec identification
// header (Vorbis or Opus) always lives there, so nothing further is needed.
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
		// RFC 7845 §5.1: input_sample_rate (payload offset 12) is
		// informational only. Opus ALWAYS decodes at 48 kHz; never read
		// that field for this purpose — see the package-level doc comment
		// and TestSniffAudioSampleRateIgnoresOpusHeadRateField.
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

// ---------------------------------------------------------------------------
// FLAC
// ---------------------------------------------------------------------------

// flacSampleRateFieldOffset adds STREAMINFO's own preceding fixed fields —
// minimum/maximum block size (2+2 bytes) and minimum/maximum frame size
// (3+3 bytes), 10 bytes total — to flacStreamInfoOffset, landing on the
// first of the 3 bytes the 20-bit sample-rate field straddles.
const flacSampleRateFieldOffset = flacStreamInfoOffset + 10

// sniffFLACSampleRate reads STREAMINFO's sample-rate field directly: FLAC
// mandates it as the first metadata block at a fixed offset, so no chunk
// walk is needed (contrast WAV, where "fmt " can be anywhere).
func sniffFLACSampleRate(data []byte) (int, bool) {
	if len(data) < flacSampleRateFieldOffset+3 {
		return 0, false
	}
	// The 20-bit rate is packed big-endian across 3 bytes at a
	// non-byte-aligned bit offset (FLAC format spec,
	// METADATA_BLOCK_STREAMINFO): top 8 bits, next 8 bits, then the top
	// nibble of the third byte.
	b0 := data[flacSampleRateFieldOffset]
	b1 := data[flacSampleRateFieldOffset+1]
	b2 := data[flacSampleRateFieldOffset+2]
	rate := int(b0)<<12 | int(b1)<<4 | int(b2)>>4
	if rate == 0 {
		return 0, false
	}
	return rate, true
}

// ---------------------------------------------------------------------------
// MP3
// ---------------------------------------------------------------------------

// mp3SyncPeekLen is the number of header bytes this sniffer reads once a
// sync candidate is found: the sync/version/layer byte and the following
// bitrate/sample-rate-index byte. The rest of the 4-byte frame header
// (channel mode, emphasis, ...) is never needed here.
const mp3SyncPeekLen = 2

// mp3SampleRates maps [mpegVersion][rateIndex] to Hz (ISO/IEC 11172-3 /
// 13818-3 frame header tables). Version 0=MPEG1, 1=MPEG2, 2=MPEG2.5; the
// wire's own 2-bit version value 0b01 is reserved and has no row, and rate
// index 0b11 is reserved in every version and is simply absent from each
// row — sniffMP3SampleRate checks for both before indexing.
var mp3SampleRates = [3][3]int{
	{44100, 48000, 32000}, // MPEG version 1
	{22050, 24000, 16000}, // MPEG version 2
	{11025, 12000, 8000},  // MPEG version 2.5
}

// sniffMP3SampleRate skips a leading ID3v2 tag if present, then scans
// forward — bounded by mp3FrameSyncScanCap — for the 11-bit frame sync,
// decoding the version and sample-rate-index fields of the first frame it
// finds.
func sniffMP3SampleRate(data []byte) (int, bool) {
	start := 0
	if hasPrefix(data, id3Signature) {
		if len(data) < id3v2HeaderLen {
			return 0, false
		}
		// Bytes 6-9 are a SYNCSAFE 28-bit size: 7 usable bits per byte,
		// top bit always 0. Decoding it as a plain big-endian u32 over-
		// reads past the real tag body and misses the frame sync.
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

// mp3RateFromIndex reads byte 2's sample-rate-index field: bitrate(4) |
// rate index(2) | padding(1) | private(1).
func mp3RateFromIndex(version int, b2 byte) (int, bool) {
	idx := (b2 >> 2) & 0x03
	if idx == 0b11 { // reserved in every MPEG version
		return 0, false
	}
	return mp3SampleRates[version][idx], true
}
