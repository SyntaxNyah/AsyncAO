//go:build !novoice

package ui

import (
	"encoding/base64"
	"math"
	"time"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/voice"
)

// Live voice audio: captures the mic, encodes Opus and sends VS_FRAME; decodes peers' VS_AUDIO, mixes and plays it.
// Hard rule 1: every SDL audio call runs on the main thread, driven by voicePump() each frame through SDL's
// callback-free queue API, so there are no audio-thread callbacks and no goroutines.
// The engine exists only while joined to a voice channel the server advertised (VS_CAPS); init failures disable a half.

const (
	// voiceFrameBytes is one Opus frame as raw S16 mono bytes (960 samples * 2).
	voiceFrameBytes = voice.FrameSize * 2
	// voiceJitterCap bounds a peer's decoded-frame backlog (hard rule 4); the oldest frame is dropped past it.
	voiceJitterCap = 12
	// voicePlayTargetFrames is how many frames stay buffered in the output queue (~60 ms).
	voicePlayTargetFrames = 3
	// voiceCaptureDrainCap caps frames encoded per pump so a backlog cannot stall a frame.
	voiceCaptureDrainCap = 4

	// Adaptive jitter buffer bounds in 20 ms frames: the target grows toward Max when audio runs dry while frames still
	// arrive, and decays toward Min when steady.
	voicePlayMinFrames = 2
	voicePlayMaxFrames = 10
	// voiceBytesPerMs converts queued bytes → ms of audio (48 kHz * 2 bytes / 1000).
	voiceBytesPerMs = voice.SampleRate * 2 / 1000
	// voiceAdaptEveryFrames re-evaluates the buffer/bitrate ~once a second (60 fps).
	voiceAdaptEveryFrames = 60

	// Opus target bitrates (bits/sec): steady, and the lower rate used while the buffer keeps backing up.
	voiceBitrate    = 24000
	voiceBitrateLow = 12000
)

// voicePeer is one remote speaker: its decoder and a bounded jitter FIFO of decoded PCM frames.
type voicePeer struct {
	dec    *voice.Decoder
	frames [][]int16
}

// voiceEngine owns the capture and playback devices and the per-peer decoders; nil when not in voice.
type voiceEngine struct {
	capture  *sdl.AudioStream // nil = no mic (listen-only)
	playback *sdl.AudioStream
	enc      *voice.Encoder // nil = can't encode (listen-only)
	peers    map[int]*voicePeer
	send     func(b64 string)

	micOn  bool
	muted  bool
	outVol int // 0..100 output volume

	capBuf     []byte    // reusable dequeue scratch (one frame)
	acc        []int32   // reusable mix accumulator (FrameSize)
	out        []int16   // reusable mixed-output scratch (FrameSize)
	mixScratch [][]int16 // reusable per-frame "one frame per active peer" gather

	// Adaptive jitter buffer + bitrate state.
	playTarget int       // current output-buffer target, in frames (voicePlayMin..Max)
	lastRecv   time.Time // when the last peer frame arrived (to tell jitter from silence)
	starves    int       // dry-buffer-while-receiving events this adapt window
	adaptCtr   int       // frames since the last adapt evaluation
	lowBitrate bool      // currently on the reduced bitrate
}

// newVoiceEngine opens the audio devices and the encoder; captureDev "" is the system default, outVol is 0..100.
// Capture is best-effort (no mic means listen-only); an error means no audio at all is possible.
func newVoiceEngine(send func(b64 string), captureDev string, outVol int) (*voiceEngine, error) {
	spec := sdl.AudioSpec{
		Freq:     voice.SampleRate,
		Format:   sdl.AUDIO_S16SYS, // native order ⇒ []int16 aliases the bytes 1:1
		Channels: voice.Channels,
	}
	play, err := sdl.OpenAudioDeviceStream("", false, &spec)
	if err != nil {
		return nil, err // no output ⇒ no audio engine (presence-only)
	}
	if outVol <= 0 || outVol > 100 {
		outVol = 100
	}
	e := &voiceEngine{
		playback:   play,
		peers:      make(map[int]*voicePeer),
		send:       send,
		outVol:     outVol,
		capBuf:     make([]byte, voiceFrameBytes),
		acc:        make([]int32, voice.FrameSize),
		out:        make([]int16, voice.FrameSize),
		playTarget: voicePlayTargetFrames,
	}
	play.Resume() // start the output clock

	// Capture is optional: a machine with no mic can still listen.
	if cap, err := sdl.OpenAudioDeviceStream(captureDev, true, &spec); err == nil {
		if enc, err := voice.NewEncoder(); err == nil {
			enc.Tune(voiceBitrate, true) // DTX on (skip silence) + steady VOIP bitrate
			e.capture, e.enc = cap, enc
			cap.Resume()
		} else {
			cap.Destroy() // encoder failed ⇒ no point capturing
		}
	}
	return e, nil
}

// canTalk reports whether this engine can transmit (has a mic + encoder).
func (e *voiceEngine) canTalk() bool { return e != nil && e.capture != nil && e.enc != nil }

// setMic toggles transmitting; pausing the capture device when off stops samples queuing.
func (e *voiceEngine) setMic(on bool) {
	if e == nil || !e.canTalk() {
		return
	}
	e.micOn = on
	if on {
		e.capture.Clear() // drop stale pre-press audio
	}
}

func (e *voiceEngine) setMuted(m bool) {
	if e != nil {
		e.muted = m
	}
}
func (e *voiceEngine) setOutVol(v int) {
	if e != nil {
		e.outVol = value.Clamp(v, 0, 100)
	}
}

// pushRemote decodes one peer Opus frame (base64) and enqueues it for the mixer, from the EventVoiceAudio handler.
func (e *voiceEngine) pushRemote(uid int, b64 string) {
	if e == nil || b64 == "" {
		return
	}
	pkt, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}
	p := e.peers[uid]
	if p == nil {
		dec, err := voice.NewDecoder()
		if err != nil {
			return
		}
		p = &voicePeer{dec: dec}
		e.peers[uid] = p
	}
	pcm, err := p.dec.Decode(pkt)
	if err != nil {
		return
	}
	e.lastRecv = time.Now() // mark "audio is arriving" — distinguishes jitter from silence
	p.frames = append(p.frames, pcm)
	if len(p.frames) > voiceJitterCap { // drop oldest — bounded backlog
		p.frames = p.frames[len(p.frames)-voiceJitterCap:]
	}
}

// dropPeer frees a peer's decoder when they leave voice.
func (e *voiceEngine) dropPeer(uid int) {
	if e == nil {
		return
	}
	if p := e.peers[uid]; p != nil {
		p.dec.Close()
		delete(e.peers, uid)
	}
}

// pump runs once per render frame: drain the mic, encode, send, then mix buffered peer audio and play it.
func (e *voiceEngine) pump() {
	if e == nil {
		return
	}
	e.pumpCapture()
	e.pumpPlayback()
	e.adaptVoice()
}

// adaptVoice re-tunes the jitter buffer and bitrate about once a second: deepen and drop bitrate after an underrun,
// shrink back and restore bitrate when steady.
func (e *voiceEngine) adaptVoice() {
	if e.adaptCtr++; e.adaptCtr < voiceAdaptEveryFrames {
		return
	}
	e.adaptCtr = 0
	if e.starves > 0 { // jittery → more buffer, less bitrate
		if e.playTarget < voicePlayMaxFrames {
			e.playTarget++
		}
		if !e.lowBitrate && e.enc != nil {
			e.enc.SetBitrate(voiceBitrateLow)
			e.lowBitrate = true
		}
	} else { // steady → ease back toward low latency / full quality
		if e.playTarget > voicePlayMinFrames {
			e.playTarget--
		}
		if e.lowBitrate && e.playTarget <= voicePlayTargetFrames && e.enc != nil {
			e.enc.SetBitrate(voiceBitrate)
			e.lowBitrate = false
		}
	}
	e.starves = 0
}

// bufferMs reports the buffered playback audio in ms, the voice latency shown on the chip; 0 when not in voice.
func (e *voiceEngine) bufferMs() int {
	if e == nil || e.playback == nil {
		return 0
	}
	return int(e.playback.Queued()) / voiceBytesPerMs
}

func (e *voiceEngine) pumpCapture() {
	if !e.micOn || !e.canTalk() {
		return
	}
	for n := 0; n < voiceCaptureDrainCap && e.capture.Available() >= voiceFrameBytes; n++ {
		got, err := e.capture.Get(e.capBuf)
		if err != nil || got < voiceFrameBytes {
			return
		}
		pkt, err := e.enc.Encode(bytesAsI16(e.capBuf))
		if err != nil {
			return
		}
		if e.send != nil {
			e.send(base64.StdEncoding.EncodeToString(pkt))
		}
	}
}

func (e *voiceEngine) pumpPlayback() {
	// Starve = the output ran dry while a peer's audio was arriving; silence must not count, as DTX stops frames then.
	if e.playback.Queued() == 0 && time.Since(e.lastRecv) < 250*time.Millisecond {
		e.starves++
	}
	// Pace to the adaptive target: only mix and queue while fewer than playTarget frames are buffered.
	for e.playback.Queued() < e.playTarget*voiceFrameBytes {
		if !e.mixOneFrame() {
			return // nobody has audio queued — let the buffer drain to silence
		}
	}
}

// mixOneFrame mixes one frame from every peer that has data and queues it; false when no peer had one.
func (e *voiceEngine) mixOneFrame() bool {
	e.mixScratch = e.mixScratch[:0]
	for _, p := range e.peers {
		if len(p.frames) == 0 {
			continue
		}
		e.mixScratch = append(e.mixScratch, p.frames[0])
		p.frames = p.frames[1:] // advance the jitter FIFO
	}
	vol := e.outVol
	if e.muted {
		vol = 0
	}
	if !mixFrames(e.out, e.acc, e.mixScratch, vol) {
		return false
	}
	_ = e.playback.Put(i16AsBytes(e.out))
	return true
}

// mixFrames sums one PCM frame per active speaker into out, scaled by vol (0..100) and clamped to int16.
// acc is reused scratch the length of out; it reports whether anything was mixed.
func mixFrames(out []int16, acc []int32, frames [][]int16, vol int) bool {
	for i := range acc {
		acc[i] = 0
	}
	any := false
	for _, f := range frames {
		if len(f) == 0 {
			continue
		}
		any = true
		for i := 0; i < len(f) && i < len(acc); i++ {
			acc[i] += int32(f[i])
		}
	}
	if !any {
		return false
	}
	for i := range out {
		v := acc[i] * int32(vol) / 100
		out[i] = int16(value.Clamp(v, math.MinInt16, math.MaxInt16))
	}
	return true
}

// close pauses + frees both devices and every decoder.
func (e *voiceEngine) close() {
	if e == nil {
		return
	}
	if e.capture != nil {
		e.capture.Destroy()
	}
	e.playback.Destroy()
	if e.enc != nil {
		e.enc.Close()
	}
	for _, p := range e.peers {
		p.dec.Close()
	}
	e.peers = nil
}

// bytesAsI16 / i16AsBytes reinterpret an S16-native PCM buffer without copying.
// Sound because AUDIO_S16SYS is the platform's native int16 layout.
func bytesAsI16(b []byte) []int16 {
	if len(b) < 2 {
		return nil
	}
	return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), len(b)/2)
}

func i16AsBytes(s []int16) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*2)
}
