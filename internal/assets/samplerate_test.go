package assets

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// --- fixtures (synthetic, minimal containers built in-memory; per the
// assignment, no binary audio fixtures are added to the repo) -------------

// writeWAVChunk appends one RIFF sub-chunk — 4-byte ASCII id, 4-byte LE
// size, payload, padded to an even length — the exact shape
// sniffWAVSampleRate must walk.
func writeWAVChunk(buf *bytes.Buffer, id string, payload []byte) {
	buf.WriteString(id)
	var sizeBuf [4]byte
	binary.LittleEndian.PutUint32(sizeBuf[:], uint32(len(payload)))
	buf.Write(sizeBuf[:])
	buf.Write(payload)
	if len(payload)%2 != 0 {
		buf.WriteByte(0) // RIFF pad byte
	}
}

// wavFmtPayload builds a minimal 16-byte PCM "fmt " payload carrying
// sampleRate at its own bytes 4-7.
func wavFmtPayload(sampleRate uint32) []byte {
	p := make([]byte, 16)
	binary.LittleEndian.PutUint16(p[0:2], 1) // WAVE_FORMAT_PCM
	binary.LittleEndian.PutUint16(p[2:4], 1) // mono
	binary.LittleEndian.PutUint32(p[4:8], sampleRate)
	binary.LittleEndian.PutUint32(p[8:12], sampleRate*2) // byte rate, unused by the sniffer
	binary.LittleEndian.PutUint16(p[12:14], 2)           // block align
	binary.LittleEndian.PutUint16(p[14:16], 16)          // bits per sample
	return p
}

// buildWAV assembles "RIFF"+size+"WAVE" followed by preFmtChunks (in order)
// and then the "fmt " chunk itself — placing "fmt " after caller-supplied
// chunks proves the walk does not assume it comes first.
func buildWAV(sampleRate uint32, preFmtChunks ...func(*bytes.Buffer)) []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	buf.Write(make([]byte, 4)) // size placeholder, patched below
	buf.WriteString("WAVE")
	for _, chunk := range preFmtChunks {
		chunk(&buf)
	}
	writeWAVChunk(&buf, "fmt ", wavFmtPayload(sampleRate))
	writeWAVChunk(&buf, "data", []byte{0, 0})
	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-riffSizePrefixLen))
	return out
}

// oggSegmentTable lays a payload of length n out across 255-byte Ogg lacing
// segments (RFC 3533 §6): full 255-byte segments, then one final segment
// (possibly zero) that is always < 255 and thus terminates the packet.
func oggSegmentTable(n int) []byte {
	var segs []byte
	for n >= 255 {
		segs = append(segs, 255)
		n -= 255
	}
	return append(segs, byte(n))
}

// buildOggPage wraps payload in a single minimal Ogg page. The fields this
// sniffer never reads (version, header_type, granule position, serial,
// sequence, checksum) are left zero.
func buildOggPage(payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("OggS")
	buf.WriteByte(0)           // version
	buf.WriteByte(0)           // header_type
	buf.Write(make([]byte, 8)) // granule_position
	buf.Write(make([]byte, 4)) // bitstream_serial_number
	buf.Write(make([]byte, 4)) // page_sequence_number
	buf.Write(make([]byte, 4)) // CRC checksum
	segs := oggSegmentTable(len(payload))
	buf.WriteByte(byte(len(segs)))
	buf.Write(segs)
	buf.Write(payload)
	return buf.Bytes()
}

// buildOggVorbisIdent builds the Vorbis identification header packet (the
// first page's payload) carrying sampleRate at its own offset 12.
func buildOggVorbisIdent(sampleRate uint32) []byte {
	payload := make([]byte, 30) // identification header is 30 bytes; only offset 12-15 matters here
	payload[0] = 0x01
	copy(payload[1:7], "vorbis")
	binary.LittleEndian.PutUint32(payload[7:11], 0) // vorbis_version
	payload[11] = 2                                 // audio_channels
	binary.LittleEndian.PutUint32(payload[12:16], sampleRate)
	return buildOggPage(payload)
}

// buildOggOpusHead builds an OpusHead packet that DECLARES declaredRate at
// its own input_sample_rate field (payload offset 12) — the field
// TestSniffAudioSampleRateIgnoresOpusHeadRateField proves is never read.
func buildOggOpusHead(declaredRate uint32) []byte {
	payload := make([]byte, 19)
	copy(payload[0:8], "OpusHead")
	payload[8] = 1                                   // version
	payload[9] = 2                                   // channel count
	binary.LittleEndian.PutUint16(payload[10:12], 0) // pre-skip
	binary.LittleEndian.PutUint32(payload[12:16], declaredRate)
	binary.LittleEndian.PutUint16(payload[16:18], 0) // output gain
	payload[18] = 0                                  // channel mapping family
	return buildOggPage(payload)
}

// buildFLAC packs sampleRate into a spec-shaped STREAMINFO block behind the
// "fLaC" magic, using the exact bit layout sniffFLACSampleRate decodes:
// (b0<<12)|(b1<<4)|(b2>>4).
func buildFLAC(sampleRate uint32) []byte {
	body := make([]byte, 34) // full STREAMINFO size; bytes 0-9 (block/frame sizes) are left zero, unused by the sniffer
	rate20 := sampleRate & 0xFFFFF
	body[10] = byte(rate20 >> 12)
	body[11] = byte(rate20 >> 4)
	body[12] = byte((rate20 & 0xF) << 4) // low nibble here is channels/bps, left zero

	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.WriteByte(0x80)                      // last-metadata-block flag set, block type 0 = STREAMINFO
	buf.Write([]byte{0, 0, byte(len(body))}) // 24-bit BE metadata length
	buf.Write(body)
	return buf.Bytes()
}

// mp3FrameHeaderBytes builds a 4-byte MPEG1 Layer III frame header (the
// 0xFFFB two-byte sync is the common no-CRC MPEG1/Layer III combination)
// carrying rateIndex (0/1/2, the wire's own 2-bit field) at its own bit
// position.
func mp3FrameHeaderBytes(rateIndex byte) []byte {
	const (
		byte1MPEG1LayerIIINoCRC = 0xFB // sync tail(111) version(11=MPEG1) layer(01=III) protection(1=none)
		bitrateIndexArbitrary   = 0x09 // any non-reserved bitrate index; this sniffer never reads it
	)
	byte2 := bitrateIndexArbitrary<<4 | rateIndex<<2 // padding=0, private=0
	return []byte{0xFF, byte1MPEG1LayerIIINoCRC, byte2, 0x00}
}

// buildID3v2Header returns a 10-byte ID3v2 header declaring a syncsafe
// bodyLen — the exact prefix sniffMP3SampleRate must skip.
func buildID3v2Header(bodyLen int) []byte {
	h := make([]byte, id3v2HeaderLen)
	copy(h[0:3], "ID3")
	h[3], h[4] = 4, 0 // version 2.4.0
	h[5] = 0          // flags
	h[6] = byte((bodyLen >> 21) & 0x7F)
	h[7] = byte((bodyLen >> 14) & 0x7F)
	h[8] = byte((bodyLen >> 7) & 0x7F)
	h[9] = byte(bodyLen & 0x7F)
	return h
}

// --- happy paths -----------------------------------------------------------

func TestSniffAudioSampleRateWAV(t *testing.T) {
	const want = 44100
	rate, ok := SniffAudioSampleRate(buildWAV(want))
	if !ok {
		t.Fatal("SniffAudioSampleRate(WAV) ok=false, want true")
	}
	if rate != want {
		t.Fatalf("SniffAudioSampleRate(WAV) = %d, want %d", rate, want)
	}
}

// TestSniffAudioSampleRateHandlesOddPaddedWAVChunks pins the RIFF
// even-padding rule: an odd-length junk chunk precedes "fmt ", so a walk
// that forgets the pad byte lands one byte short of "fmt " and misreads it.
func TestSniffAudioSampleRateHandlesOddPaddedWAVChunks(t *testing.T) {
	const want = 22050
	data := buildWAV(want, func(buf *bytes.Buffer) {
		writeWAVChunk(buf, "JUNK", []byte{1, 2, 3}) // odd (3-byte) payload forces a pad byte
	})
	rate, ok := SniffAudioSampleRate(data)
	if !ok {
		t.Fatal("SniffAudioSampleRate(WAV with odd junk chunk) ok=false, want true")
	}
	if rate != want {
		t.Fatalf("SniffAudioSampleRate(WAV with odd junk chunk) = %d, want %d — the walk likely lost the pad byte", rate, want)
	}
}

func TestSniffAudioSampleRateVorbis(t *testing.T) {
	const want = 48000
	rate, ok := SniffAudioSampleRate(buildOggVorbisIdent(want))
	if !ok {
		t.Fatal("SniffAudioSampleRate(Ogg Vorbis) ok=false, want true")
	}
	if rate != want {
		t.Fatalf("SniffAudioSampleRate(Ogg Vorbis) = %d, want %d", rate, want)
	}
}

func TestSniffAudioSampleRateFLAC(t *testing.T) {
	const want = 96000
	rate, ok := SniffAudioSampleRate(buildFLAC(want))
	if !ok {
		t.Fatal("SniffAudioSampleRate(FLAC) ok=false, want true")
	}
	if rate != want {
		t.Fatalf("SniffAudioSampleRate(FLAC) = %d, want %d", rate, want)
	}
}

func TestSniffAudioSampleRateMP3(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{"bareFrame_44100", mp3FrameHeaderBytes(0), 44100},
		{"bareFrame_48000", mp3FrameHeaderBytes(1), 48000},
		{"bareFrame_32000", mp3FrameHeaderBytes(2), 32000},
		{
			"id3ThenFrame",
			append(append(buildID3v2Header(8), make([]byte, 8)...), mp3FrameHeaderBytes(0)...),
			44100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, ok := SniffAudioSampleRate(tt.data)
			if !ok {
				t.Fatalf("SniffAudioSampleRate(%s) ok=false, want true", tt.name)
			}
			if rate != tt.want {
				t.Fatalf("SniffAudioSampleRate(%s) = %d, want %d", tt.name, rate, tt.want)
			}
		})
	}
}

// TestSniffAudioSampleRateReservedMP3IndicesFail pins the "index 11 is
// reserved in all three tables" rule (both the version and the rate-index
// fields have a reserved encoding) — the assignment's own "malformed"
// requirement for a container that otherwise looks completely valid.
func TestSniffAudioSampleRateReservedMP3IndicesFail(t *testing.T) {
	reservedVersion := []byte{0xFF, 0xE8, 0x90, 0x00} // version bits 01 = reserved
	reservedRateIdx := []byte{0xFF, 0xFB, 0xFC, 0x00} // rate-index bits 11 = reserved
	for name, data := range map[string][]byte{
		"reservedVersion": reservedVersion,
		"reservedRateIdx": reservedRateIdx,
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := SniffAudioSampleRate(data); ok {
				t.Fatalf("SniffAudioSampleRate(%s) ok=true, want false (reserved encoding)", name)
			}
		})
	}
}

// --- the encapsulation test -------------------------------------------------

// TestSniffAudioSampleRateIgnoresOpusHeadRateField is the encapsulation test
// for the Opus hardcode (issue #48 decision 15; music-architect-corrections.md
// correction 4). A mirror test would read OpusHead's own input_sample_rate
// field back out of the fixture and assert the sniffer returns THAT value —
// it would pass whether the implementation hardcodes 48000 or faithfully
// decodes the field, because an honest fixture makes the two coincide.
// Instead this test builds a technically-valid OpusHead packet that
// DECLARES A WRONG RATE — 44100, a real and extremely common rate, not a
// nonsense sentinel — and asserts the answer is 48000 regardless. The only
// way that assertion can pass is if the sniffer never reads the field.
//
// THE MUTATION THIS ANSWERS TO: replace the OpusHead branch's
// `return opusNativeSampleRate, true` with a decode of payload offset 12
// (`return int(binary.LittleEndian.Uint32(payload[12:16])), true`). This
// test goes red — it would report 44100, want 48000 — and nothing else in
// this suite would catch it: RFC 7845 mandates decoders ignore the field,
// so no other codec path in this sniffer ever exercises it.
func TestSniffAudioSampleRateIgnoresOpusHeadRateField(t *testing.T) {
	const wrongDeclaredRate = 44100 // a real, common, plausible rate — never a nonsense value a decoder would obviously reject
	data := buildOggOpusHead(wrongDeclaredRate)

	rate, ok := SniffAudioSampleRate(data)
	if !ok {
		t.Fatal("SniffAudioSampleRate(OpusHead) ok=false, want true")
	}
	if rate != opusNativeSampleRate {
		t.Fatalf("SniffAudioSampleRate(OpusHead declaring %d Hz) = %d, want the hardcoded %d — "+
			"the sniffer read OpusHead's informational input_sample_rate field instead of ignoring it "+
			"(RFC 7845 §5.1); every .opus loop-point seek would now be scaled wrong",
			wrongDeclaredRate, rate, opusNativeSampleRate)
	}
}

// --- garbage, malformed and truncated input --------------------------------

// TestSniffAudioSampleRateRejectsGarbage covers empty input, a format we do
// not recognise at all, and something that looks like each supported
// format but is malformed in a way specific to that format's own parser.
func TestSniffAudioSampleRateRejectsGarbage(t *testing.T) {
	riffButNotWave := func() []byte {
		var buf bytes.Buffer
		buf.WriteString("RIFF")
		buf.Write(make([]byte, 4))
		buf.WriteString("AVI ") // a real RIFF form this sniffer must not mistake for WAVE
		return buf.Bytes()
	}()

	wavWithoutFmtChunk := func() []byte {
		var buf bytes.Buffer
		buf.WriteString("RIFF")
		buf.Write(make([]byte, 4))
		buf.WriteString("WAVE")
		writeWAVChunk(&buf, "JUNK", []byte{1, 2, 3, 4})
		out := buf.Bytes()
		binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-riffSizePrefixLen))
		return out
	}()

	oggUnknownCodec := buildOggPage([]byte("theora unknown codec ident header......"))

	tests := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"unrecognizedFormat", bytes.Repeat([]byte{0x00, 0x01, 0x02, 0x03}, 8)},
		{"pngLooksNothingLikeAudio", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}},
		{"riffButNotWave", riffButNotWave},
		{"wavWithoutFmtChunk", wavWithoutFmtChunk},
		{"oggUnknownCodec", oggUnknownCodec},
		{"flacTooShortForStreamInfo", []byte("fLaC")},
		{"id3ClaimsSizePastEOF", append(buildID3v2Header(1<<20), []byte{1, 2, 3}...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rate, ok := SniffAudioSampleRate(tt.data); ok {
				t.Fatalf("SniffAudioSampleRate(%s) = (%d, true), want ok=false", tt.name, rate)
			}
		})
	}
}

// TestSniffAudioSampleRateRejectsTruncatedInputForEveryFormat feeds every
// prefix length of every happy-path fixture (0 through the full length)
// through the sniffer: it must never panic or read out of bounds (the
// assignment's core safety contract), and definitely-too-short prefixes
// must report ok=false rather than a garbage rate read from whatever bytes
// happened to be present.
func TestSniffAudioSampleRateRejectsTruncatedInputForEveryFormat(t *testing.T) {
	tests := []struct {
		name          string
		full          []byte
		neverOKBefore int // prefixes shorter than this must always be ok=false
	}{
		{"wav", buildWAV(44100), riffSizePrefixLen + wavChunkHdrLen},
		{"vorbis", buildOggVorbisIdent(44100), oggPageHeaderFixedLen},
		{"opus", buildOggOpusHead(48000), oggPageHeaderFixedLen},
		{"flac", buildFLAC(44100), flacSampleRateFieldOffset},
		{"mp3", mp3FrameHeaderBytes(0), 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := SniffAudioSampleRate(tt.full); !ok {
				t.Fatalf("the full %s fixture itself failed to sniff — nothing below is testing truncation", tt.name)
			}
			for n := 0; n <= len(tt.full); n++ {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("SniffAudioSampleRate(%s[:%d]) panicked: %v", tt.name, n, r)
						}
					}()
					rate, ok := SniffAudioSampleRate(tt.full[:n])
					if n < tt.neverOKBefore && ok {
						t.Fatalf("SniffAudioSampleRate(%s[:%d]) = (%d, true), want ok=false at this length", tt.name, n, rate)
					}
				}()
			}
		})
	}
}

// FuzzSniffAudioSampleRate is the general robustness net for the assignment's
// "never panic, never index out of range" contract, seeded with every
// synthetic fixture above (whole and none of them need be valid).
func FuzzSniffAudioSampleRate(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		buildWAV(44100),
		buildOggVorbisIdent(48000),
		buildOggOpusHead(44100),
		buildFLAC(96000),
		mp3FrameHeaderBytes(1),
		append(buildID3v2Header(8), append(make([]byte, 8), mp3FrameHeaderBytes(0)...)...),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = SniffAudioSampleRate(data)
	})
}
