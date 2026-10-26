//go:build !novoice

package ui

import (
	"encoding/base64"
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/voice"
)

// Live voice AUDIO — the slice that turns the VS_* signaling (courtroom/voice.go)
// + the Opus codec (internal/voice) into real talking. It captures the mic,
// encodes Opus, and hands frames to the session (VS_FRAME); and decodes peers'
// VS_AUDIO, mixes them, and plays the result.
//
// Threading / hard rule #1: every SDL audio call here runs on the MAIN thread —
// the engine is driven by voicePump() from App.Frame() each frame, using SDL's
// callback-free QUEUE api (QueueAudio / DequeueAudio), so there are no audio-
// thread callbacks into Go and no goroutines. Outgoing frames are sent from the
// same loop (single writer — never races other packet sends).
//
// Safety: the engine only exists while you've JOINED a Nyathena voice channel
// (opt-in; default is not-in-voice) and the server advertised voice (VS_CAPS), so
// the audio path is unreachable for everyone else. Every init step is fail-safe:
// a missing mic / device / codec disables that half (or voice entirely) and is
// never fatal.

const (
	// voiceFrameBytes is one Opus frame as raw S16 mono bytes (960 samples * 2).
	voiceFrameBytes = voice.FrameSize * 2
	// voiceDeviceSamples sizes the SDL device buffer (power of two, ~21 ms @ 48 kHz).
	voiceDeviceSamples = 1024
	// voiceJitterCap bounds a peer's decoded-frame backlog (hard rule #4): ~240 ms,
	// then the oldest frame is dropped so a stalled peer can't grow unbounded.
	voiceJitterCap = 12
	// voicePlayTargetFrames is how many frames we keep buffered in the output queue
	// (~60 ms) — enough to ride network jitter, small enough to stay responsive.
	voicePlayTargetFrames = 3
	// voiceCaptureDrainCap caps frames encoded per pump so a backlog can't stall a
	// frame (bounded work per render frame).
	voiceCaptureDrainCap = 4

	// Adaptive jitter buffer bounds (in 20 ms frames): the playback target grows
	// toward Max when audio runs dry while frames are still arriving (network jitter
	// — TCP retransmit delays, since voice rides the reliable WebSocket), and decays
	// toward Min when steady, trading latency for smoothness only as needed.
	voicePlayMinFrames = 2
	voicePlayMaxFrames = 10
	// voiceBytesPerMs converts queued bytes → ms of audio (48 kHz * 2 bytes / 1000).
	voiceBytesPerMs = voice.SampleRate * 2 / 1000
	// voiceAdaptEveryFrames re-evaluates the buffer/bitrate ~once a second (60 fps).
	voiceAdaptEveryFrames = 60

	// Opus target bitrates (bits/sec): steady vs. the lower rate used when the buffer
	// keeps backing up (a struggling connection — fewer bytes catch up faster).
	voiceBitrate    = 24000
	voiceBitrateLow = 12000
)

// voicePeer is one remote speaker: its decoder + a bounded jitter FIFO of decoded
// PCM frames awaiting mix.
type voicePeer struct {
	dec    *voice.Decoder
	frames [][]int16
}

// voiceEngine owns the capture + playback devices and the per-peer decoders. nil
// when not in voice; presence still works without it.
type voiceEngine struct {
	capture  sdl.AudioDeviceID // 0 = no mic (listen-only)
	playback sdl.AudioDeviceID
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

// newVoiceEngine opens the audio devices and the encoder. captureDev is the mic
// device name ("" = system default); outVol is the initial output volume (0..100).
// Playback is required; capture is best-effort (no mic ⇒ listen-only). Returns an
// error only when no audio at all is possible — the caller stays presence-only.
func newVoiceEngine(send func(b64 string), captureDev string, outVol int) (*voiceEngine, error) {
	spec := sdl.AudioSpec{
		Freq:     voice.SampleRate,
		Format:   sdl.AUDIO_S16SYS, // native order ⇒ []int16 aliases the bytes 1:1
		Channels: voice.Channels,
		Samples:  voiceDeviceSamples,
	}
	play, err := sdl.OpenAudioDevice("", false, &spec, nil, 0)
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
	sdl.PauseAudioDevice(play, false) // start the output clock

	// Capture is optional: a machine with no mic can still listen. captureDev ""
	// asks SDL for the system-default recording device.
	if cap, err := sdl.OpenAudioDevice(captureDev, true, &spec, nil, 0); err == nil {
		if enc, err := voice.NewEncoder(); err == nil {
			enc.Tune(voiceBitrate, true) // DTX on (skip silence) + steady VOIP bitrate
			e.capture, e.enc = cap, enc
			sdl.PauseAudioDevice(cap, false)
		} else {
			sdl.CloseAudioDevice(cap) // encoder failed ⇒ no point capturing
		}
	}
	return e, nil
}

// canTalk reports whether this engine can transmit (has a mic + encoder).
func (e *voiceEngine) canTalk() bool { return e != nil && e.capture != 0 && e.enc != nil }

// setMic toggles transmitting. Pausing the capture device when off means no
// samples queue up to drain (and the OS mic indicator goes quiet).
func (e *voiceEngine) setMic(on bool) {
	if e == nil || !e.canTalk() {
		return
	}
	e.micOn = on
	if on {
		sdl.ClearQueuedAudio(e.capture) // drop stale pre-press audio
	}
}

func (e *voiceEngine) setMuted(m bool) {
	if e != nil {
		e.muted = m
	}
}
func (e *voiceEngine) setOutVol(v int) {
	if e != nil {
		e.outVol = clampInt(v, 0, 100)
	}
}

// pushRemote decodes one peer Opus frame (base64) and enqueues it for the mixer.
// Called on the main loop from the EventVoiceAudio handler.
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

// pump runs once per render frame (main thread): drain the mic → encode → send,
// then mix any buffered peer audio → play. No-op-cheap when idle.
func (e *voiceEngine) pump() {
	if e == nil {
		return
	}
	e.pumpCapture()
	e.pumpPlayback()
	e.adaptVoice()
}

// adaptVoice re-tunes the jitter buffer + bitrate ~once a second: if the output
// ran dry while frames were still arriving (network jitter), deepen the buffer and
// drop to the lower bitrate; when it's been steady, shrink the buffer back toward
// minimum and restore full bitrate. Bounded both ways, so the worst case is a
// slightly deeper buffer, never broken audio.
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

// bufferMs reports how much audio is buffered for playback, in milliseconds — the
// voice output latency we add (shown on the voice-latency chip). 0 when not in voice.
func (e *voiceEngine) bufferMs() int {
	if e == nil || e.playback == 0 {
		return 0
	}
	return int(sdl.GetQueuedAudioSize(e.playback)) / voiceBytesPerMs
}

func (e *voiceEngine) pumpCapture() {
	if !e.micOn || !e.canTalk() {
		return
	}
	for n := 0; n < voiceCaptureDrainCap && sdl.GetQueuedAudioSize(e.capture) >= voiceFrameBytes; n++ {
		got, err := sdl.DequeueAudio(e.capture, e.capBuf)
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
	// Starve = the output ran dry while a peer's audio was arriving (< ~250 ms ago) —
	// a real jitter underrun, not just silence (DTX stops frames during silence, so a
	// quiet gap must NOT be counted). adaptVoice deepens the buffer when this happens.
	if sdl.GetQueuedAudioSize(e.playback) == 0 && time.Since(e.lastRecv) < 250*time.Millisecond {
		e.starves++
	}
	// Pace to the (adaptive) output target so latency stays bounded: only mix-and-queue
	// while fewer than playTarget frames are buffered.
	for sdl.GetQueuedAudioSize(e.playback) < uint32(e.playTarget)*voiceFrameBytes {
		if !e.mixOneFrame() {
			return // nobody has audio queued — let the buffer drain to silence
		}
	}
}

// mixOneFrame pops one frame from every peer that has data, mixes them, and
// queues the result for playback. Returns false when no peer had a frame.
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
	_ = sdl.QueueAudio(e.playback, i16AsBytes(e.out))
	return true
}

// mixFrames sums one PCM frame per active speaker into out, scaled by vol (0..100)
// and clamped to int16. acc is reused scratch (same length as out). Returns
// whether anything was mixed (false ⇒ all silent). Pure (no SDL) — unit-tested.
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
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return true
}

// close pauses + frees both devices and every decoder.
func (e *voiceEngine) close() {
	if e == nil {
		return
	}
	if e.capture != 0 {
		sdl.PauseAudioDevice(e.capture, true)
		sdl.CloseAudioDevice(e.capture)
	}
	sdl.PauseAudioDevice(e.playback, true)
	sdl.CloseAudioDevice(e.playback)
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
