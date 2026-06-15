package render

import (
	"log"
	"math"
	"time"

	"github.com/veandco/go-sdl2/mix"
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// Audio device parameters (48 kHz stereo matches modern opus content).
	audioFrequency = 48000
	audioChannels  = 2
	audioChunkSize = 1024

	// mixChannelCount is the SFX mixing channel pool.
	mixChannelCount = 16
	// blipChannel is reserved so a new blip replaces the previous one
	// (AO blips never stack).
	blipChannel = 0
	// alertChannel is reserved for callword/friend pings. A callword fires on
	// message ARRIVAL, when the blip channel is momentarily free — so Play(-1)
	// used to land the ping on channel 0, and the typewriter's very first blip
	// then HaltChannel(0)'d it, cutting the ping the instant blips started
	// ("can't hear the callword when blips are on"). A dedicated reserved
	// channel keeps blips and a burst of emote SFX from ever stealing it.
	alertChannel = 1
	// reservedChannels holds channels [0,N) (blip + alert) back from the
	// Play(-1) SFX pool so only their explicit owners use them.
	reservedChannels = 2

	// chunkCacheMax bounds decoded SFX/blip chunks held in memory.
	chunkCacheMax = 64

	// pendingPlayTTL drops play requests whose asset never arrived.
	pendingPlayTTL = 10 * time.Second

	// mixInitOpus is MIX_INIT_OPUS (0x40). go-sdl2 v0.4.40's mix package
	// predates the Opus flag and doesn't export it, but the SDL2_mixer.dll we
	// ship supports Opus — so we pass the raw value to Mix_Init. Without this,
	// the on-demand opus DLLs (libopusfile/libopus) never load and .opus
	// content (Discord /play links, .opus alert sounds) won't decode.
	mixInitOpus = 0x40
)

type pendingKind int

const (
	pendingShout pendingKind = iota
	pendingSFX
	pendingBlip
	pendingMusic
	pendingAlert // callword/friend ping — its own reserved channel
)

type pendingPlay struct {
	kind     pendingKind
	deadline time.Time
}

// Audio implements courtroom.AudioSink over SDL_mixer: raw bytes from the
// asset pipeline decode in C (spec §8 — no Go audio decoding anywhere).
// All methods run on the render/game thread.
type Audio struct {
	mgr *assets.Manager

	chunks     map[string]*mix.Chunk // key: asset base
	chunkOrder []string              // FIFO eviction order
	pending    map[string]pendingPlay

	// alert is the built-in notification ping — the guaranteed-audible default
	// for callword/friend alerts whenever the user set no custom sound file.
	// Synthesized once at open, freed at close; never enters the asset chunk
	// cache.
	alert *mix.Chunk

	musicBytes []byte // keeps streamed music memory alive while playing
	musicRW    *sdl.RWops
	music      *mix.Music

	// Volumes in percent (0–100), applied as mixer volume at play time
	// (music globally, chunks per returned channel).
	musicVol, sfxVol, blipVol int

	enabled bool
}

// fullVolumePercent maps 100% onto SDL_mixer's MAX_VOLUME.
const fullVolumePercent = 100

func mixVolume(pct int) int { return pct * mix.MAX_VOLUME / fullVolumePercent }

// SetVolumes applies the user's music/SFX/blip volumes (0–100). Music
// volume takes effect immediately; chunk volumes apply per play.
func (a *Audio) SetVolumes(music, sfx, blip int) {
	a.musicVol, a.sfxVol, a.blipVol = music, sfx, blip
	if a.enabled {
		mix.VolumeMusic(mixVolume(music))
	}
}

// NewAudio opens the mixer. A failed device (headless CI) degrades to a
// disabled-but-functional sink.
func NewAudio(mgr *assets.Manager) *Audio {
	a := &Audio{
		mgr:      mgr,
		chunks:   map[string]*mix.Chunk{},
		pending:  map[string]pendingPlay{},
		musicVol: fullVolumePercent,
		sfxVol:   fullVolumePercent,
		blipVol:  fullVolumePercent,
	}
	// Load the dynamic decoder libraries we ship: opus/ogg/mp3 are pulled in on
	// demand (only WAV is built into SDL_mixer), so without this they never
	// decode. Best-effort — Init reports an error if any one codec's DLL is
	// missing, but the codecs that DID load stay usable, so we log and continue.
	if err := mix.Init(mix.INIT_OGG | mix.INIT_MP3 | mixInitOpus); err != nil {
		log.Printf("render: some audio codecs unavailable (opus/ogg/mp3): %v", err)
	}
	if err := mix.OpenAudio(audioFrequency, mix.DEFAULT_FORMAT, audioChannels, audioChunkSize); err != nil {
		log.Printf("render: audio disabled: %v", err)
		mix.Quit() // pair the Init above even though the device failed
		return a
	}
	mix.AllocateChannels(mixChannelCount)
	mix.ReserveChannels(reservedChannels) // keep blips + alerts off the Play(-1) SFX pool
	a.enabled = true
	a.loadAlert()
	return a
}

// loadAlert synthesizes the built-in notification ping and loads it as a
// mixer chunk (same in-memory path as loadChunk). Best-effort: a failure
// leaves a.alert nil and PlayAlert simply no-ops.
func (a *Audio) loadAlert() {
	wav := builtinAlertWAV()
	rw, err := sdl.RWFromMem(wav)
	if err != nil {
		return
	}
	chunk, err := mix.LoadWAVRW(rw, true) // mixer frees the RW
	if err != nil {
		log.Printf("render: built-in alert sound failed: %v", err)
		return
	}
	a.alert = chunk
}

// builtinAlertWAV synthesizes the callword/friend "ping" as an in-memory WAV
// (16-bit mono PCM): a ~1.5 s trill of three short two-tone dings, long enough
// to clearly notice (a single 160 ms ping "played too short"). Each ding resets
// phase and decays to zero, so the dings and the gaps between them are
// click-free. The reliable default when no custom sound is set — see
// App.checkCallwords / signalFriend.
func builtinAlertWAV() []byte {
	const (
		alertSampleRate = 44100
		alertTotalMs    = 1500   // total length — the user asked for ~1.5 s
		alertDingMs     = 300    // one ding within the trill
		alertGapMs      = 200    // silence between dings (1500 = 3 × (300+200))
		alertFreqLo     = 880.0  // first tone (A5)
		alertFreqHi     = 1320.0 // lift on the ding's second half — the "ping" feel
		alertAmp        = 0.33   // headroom so it isn't jarring
		alertAttack     = 0.05   // fraction of a ding spent ramping in (anti-click)
	)
	dingN := alertSampleRate * alertDingMs / 1000
	unitN := alertSampleRate * (alertDingMs + alertGapMs) / 1000
	totalN := alertSampleRate * alertTotalMs / 1000
	pcm := make([]byte, 0, totalN*2)
	for i := 0; i < totalN; i++ {
		var v int16
		if pos := i % unitN; pos < dingN { // inside a ding (else the silent gap)
			t := float64(pos) / float64(alertSampleRate) // phase per ding
			prog := float64(pos) / float64(dingN)        // 0..1 through the ding
			freq := alertFreqLo
			if prog >= 0.5 {
				freq = alertFreqHi
			}
			env := 1.0 // attack ramp, then linear decay to 0 (no click)
			if prog < alertAttack {
				env = prog / alertAttack
			} else {
				env = 1 - (prog-alertAttack)/(1-alertAttack)
			}
			v = int16(math.Sin(2*math.Pi*freq*t) * alertAmp * env * math.MaxInt16)
		}
		pcm = append(pcm, byte(v), byte(v>>8)) // little-endian
	}
	return wavMono16(pcm, alertSampleRate)
}

// wavMono16 wraps 16-bit mono PCM samples in a minimal RIFF/WAVE container.
func wavMono16(pcm []byte, sampleRate int) []byte {
	const (
		channels   = 1
		bits       = 16
		pcmFormat  = 1 // WAVE_FORMAT_PCM
		fmtChunk   = 16
		headerSize = 44
	)
	byteRate := sampleRate * channels * bits / 8
	blockAlign := channels * bits / 8
	buf := make([]byte, 0, headerSize+len(pcm))
	put4 := func(s string) { buf = append(buf, s...) }
	put32 := func(v int) { buf = append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
	put16 := func(v int) { buf = append(buf, byte(v), byte(v>>8)) }
	put4("RIFF")
	put32(36 + len(pcm)) // RIFF chunk size = 4 ("WAVE") + (8+fmt) + (8+data)
	put4("WAVE")
	put4("fmt ")
	put32(fmtChunk)
	put16(pcmFormat)
	put16(channels)
	put32(sampleRate)
	put32(byteRate)
	put16(blockAlign)
	put16(bits)
	put4("data")
	put32(len(pcm))
	return append(buf, pcm...)
}

// PlayAlert plays the built-in notification ping (callword/friend fallback).
// No-op when the device is disabled or the ping failed to synthesize.
func (a *Audio) PlayAlert() {
	if !a.enabled || a.alert == nil {
		return
	}
	a.playChunk(a.alert, pendingAlert)
}

// Close stops playback and shuts the device.
func (a *Audio) Close() {
	if !a.enabled {
		return
	}
	a.stopMusic()
	for _, c := range a.chunks {
		c.Free()
	}
	a.chunks = map[string]*mix.Chunk{}
	if a.alert != nil {
		a.alert.Free()
		a.alert = nil
	}
	mix.CloseAudio()
	mix.Quit() // unload the codec libs loaded in NewAudio
	a.enabled = false
}

// Frame drains delivered audio payloads and fires matured pending plays.
// Call once per frame on the render/game thread.
func (a *Audio) Frame() {
	for {
		select {
		case asset := <-a.mgr.Audio():
			a.onAudioBytes(asset)
		default:
			a.expirePending()
			return
		}
	}
}

func (a *Audio) onAudioBytes(asset assets.AudioAsset) {
	if !a.enabled {
		delete(a.pending, asset.Base)
		return
	}
	p, wanted := a.pending[asset.Base]
	if wanted && p.kind == pendingMusic {
		delete(a.pending, asset.Base)
		a.startMusic(asset.Data)
		return
	}
	chunk := a.loadChunk(asset.Base, asset.Data)
	if chunk == nil || !wanted {
		return
	}
	delete(a.pending, asset.Base)
	a.playChunk(chunk, p.kind)
}

// loadChunk decodes bytes via SDL_mixer (wav/ogg/opus/mp3) and caches the
// fully-decoded chunk by base.
func (a *Audio) loadChunk(base string, data []byte) *mix.Chunk {
	if chunk, ok := a.chunks[base]; ok {
		return chunk
	}
	rw, err := sdl.RWFromMem(data)
	if err != nil {
		return nil
	}
	chunk, err := mix.LoadWAVRW(rw, true) // mixer frees the RW
	if err != nil {
		log.Printf("render: audio decode %s failed: %v", base, err)
		return nil
	}
	if len(a.chunkOrder) >= chunkCacheMax {
		oldest := a.chunkOrder[0]
		a.chunkOrder = a.chunkOrder[1:]
		if old, ok := a.chunks[oldest]; ok {
			old.Free()
			delete(a.chunks, oldest)
		}
	}
	a.chunks[base] = chunk
	a.chunkOrder = append(a.chunkOrder, base)
	return chunk
}

func (a *Audio) playChunk(chunk *mix.Chunk, kind pendingKind) {
	switch kind {
	case pendingBlip:
		// Blips replace each other on the reserved channel — playing one
		// is a pointer pass (spec §8).
		mix.HaltChannel(blipChannel)
		if _, err := chunk.Play(blipChannel, 0); err == nil {
			mix.Volume(blipChannel, mixVolume(a.blipVol))
		}
	case pendingAlert:
		// Callword/friend ping on its own reserved channel: a new alert replaces
		// the old (no stacking), and neither blips nor a burst of emote SFX can
		// steal or halt it. SFX volume so the mute toggle still covers it.
		mix.HaltChannel(alertChannel)
		if _, err := chunk.Play(alertChannel, 0); err == nil {
			mix.Volume(alertChannel, mixVolume(a.sfxVol))
		}
	default:
		if ch, err := chunk.Play(-1, 0); err == nil {
			mix.Volume(ch, mixVolume(a.sfxVol))
		}
	}
}

// PlayFile plays a sound from a local file PATH (an opt-in user alert sound:
// callword / friend), loaded + cached by path — OFF the courtroom asset
// pipeline. A missing/bad path just no-ops. Render thread only; the file loads
// once (first play), then it's a cached pointer pass like any other chunk.
func (a *Audio) PlayFile(path string) {
	if path == "" || !a.enabled {
		return
	}
	chunk, ok := a.chunks[path]
	if !ok {
		c, err := mix.LoadWAV(path)
		if err != nil {
			log.Printf("render: alert sound %s failed: %v", path, err)
			return
		}
		if len(a.chunkOrder) >= chunkCacheMax {
			oldest := a.chunkOrder[0]
			a.chunkOrder = a.chunkOrder[1:]
			if old, ok := a.chunks[oldest]; ok {
				old.Free()
				delete(a.chunks, oldest)
			}
		}
		a.chunks[path] = c
		a.chunkOrder = append(a.chunkOrder, path)
		chunk = c
	}
	a.playChunk(chunk, pendingAlert) // alert channel: a custom callword/friend sound is never cut by blips
}

// request plays the chunk for base now if cached, else marks it pending
// (the courtroom already prefetched it at HIGH priority).
func (a *Audio) request(base string, kind pendingKind) {
	if base == "" || !a.enabled {
		return
	}
	if chunk, ok := a.chunks[base]; ok {
		a.playChunk(chunk, kind)
		return
	}
	a.pending[base] = pendingPlay{kind: kind, deadline: time.Now().Add(pendingPlayTTL)}
}

func (a *Audio) expirePending() {
	if len(a.pending) == 0 {
		return
	}
	now := time.Now()
	for base, p := range a.pending {
		if now.After(p.deadline) {
			delete(a.pending, base)
		}
	}
}

// --- courtroom.AudioSink ------------------------------------------------------

// PlayShout plays a character's shout cry. // AssetType: SFX
func (a *Audio) PlayShout(base string) { a.request(base, pendingShout) }

// PlaySFX plays an emote sound effect. Delay is honored by the courtroom
// phase machine; here it is best-effort immediate. // AssetType: SFX
func (a *Audio) PlaySFX(base string, _ time.Duration) { a.request(base, pendingSFX) }

// PlayBlip fires one chat blip. // AssetType: Blip
func (a *Audio) PlayBlip(base string) { a.request(base, pendingBlip) }

// PlayMusic streams a track from its full URL. // AssetType: Music
func (a *Audio) PlayMusic(url string) {
	if !a.enabled {
		return
	}
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL)}
	a.mgr.PrefetchExact(url, assets.AssetTypeMusic, network.PriorityHigh) // AssetType: Music
}

// StopMusic halts music playback immediately AND cancels any track still being
// fetched. AO has no stop packet, so the "Stop music" button can't rely on the
// server recognizing a fake stop track — this lets a listener silence music in
// their own client right away, regardless of DJ rights. Render thread only.
func (a *Audio) StopMusic() {
	if !a.enabled {
		return
	}
	a.stopMusic()
	for url, p := range a.pending { // a pending fetch would otherwise start on arrival
		if p.kind == pendingMusic {
			delete(a.pending, url)
		}
	}
}

func (a *Audio) startMusic(data []byte) {
	a.stopMusic()
	rw, err := sdl.RWFromMem(data)
	if err != nil {
		return
	}
	music, err := mix.LoadMUSRW(rw, 0) // we own the RW; bytes stay alive below
	if err != nil {
		log.Printf("render: music decode failed: %v", err)
		_ = rw.Free()
		return
	}
	a.musicBytes = data // pin the payload while the mixer streams from it
	a.musicRW = rw
	a.music = music
	const loopForever = -1
	if err := music.Play(loopForever); err != nil {
		log.Printf("render: music play failed: %v", err)
		a.stopMusic()
		return
	}
	mix.VolumeMusic(mixVolume(a.musicVol))
}

func (a *Audio) stopMusic() {
	if a.music != nil {
		mix.HaltMusic()
		a.music.Free()
		a.music = nil
	}
	if a.musicRW != nil {
		_ = a.musicRW.Free()
		a.musicRW = nil
	}
	a.musicBytes = nil
}
