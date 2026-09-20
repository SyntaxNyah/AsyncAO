package render

import (
	"log"
	"math"
	"time"

	mix "github.com/SyntaxNyah/AsyncAO/internal/render/mixerx"
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
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
	// chunkCacheHardMax is the absolute ceiling the cache may grow to in the
	// (by-construction impossible) case that every one of the chunkCacheMax
	// oldest chunks is currently playing when eviction is due. At most
	// mixChannelCount (16) chunks can play at once and 16 < chunkCacheMax (64),
	// so evictChunk always finds a non-playing victim — this cap only exists so
	// the cache stays bounded (hard rule #4) even if that invariant were ever
	// violated. We grow-and-log rather than free a playing chunk (rule #7-class
	// safety over the C audio callback).
	chunkCacheHardMax = chunkCacheMax + mixChannelCount

	// pendingPlayTTL drops play requests whose asset never arrived.
	pendingPlayTTL = 10 * time.Second

	// musicFadeInMs is the FADE_IN ramp length (#15). AO2's BASS path slides the
	// new stream's volume up over 1000ms (../AO2-Client/src/aomusicplayer.cpp:160
	// FADE_IN branch); we match it. The ramp rides SDL_mixer's native fade
	// (Mix_FadeInMusic), which interpolates on the C audio callback and always
	// scales toward the current VolumeMusic — so a mid-fade volume-slider drag
	// just changes the ceiling the ramp climbs to (no fight, no extra state).
	musicFadeInMs = 1000
	// musicFadeOutMs is the FADE_OUT ramp length (#112). AO2's BASS path slides
	// the PREVIOUS track down over 4000ms (../AO2-Client/src/aomusicplayer.cpp:141
	// FADE_OUT branch); we match it with SDL Mixer X's native linear
	// Mix_FadeOutMusicStream. The outgoing stream fades WHILE the incoming track
	// downloads (zero added latency), then stepFadeOutOld reaps it once halted.
	musicFadeOutMs = 4000

	// Music effect bit flags (MUSIC_EFFECT, ../AO2-Client/src/datatypes.h:95-101).
	// Pinned against config.MusicFlag* to catch config drift at compile time.
	// The looping field / NO_REPEAT are applied by the courtroom before we ever
	// see them (they arrive as the loop bool).
	musicEffectFadeIn  = 1 << 0 // ramp the NEW track up (config.MusicFlagFadeIn)
	musicEffectFadeOut = 1 << 1 // ramp the PREVIOUS track down (config.MusicFlagFadeOut)
	musicEffectSyncPos = 1 << 2 // start new track at previous track's position (config.MusicFlagSyncPos)

	// mixInitOpus is MIX_INIT_OPUS (0x40). go-sdl2 v0.4.40's mix package
	// predates the Opus flag and doesn't export it, but the SDL2_mixer.dll we
	// ship supports Opus — so we pass the raw value to Mix_Init. Without this,
	// the on-demand opus DLLs (libopusfile/libopus) never load and .opus
	// content (Discord /play links, .opus alert sounds) won't decode.
	mixInitOpus = 0x40
)

// Compile-time assertion: render-side effect constants must match config's wire values.
// If config.MusicFlag* changes, this catches the drift before runtime (#112).
var _ = [1]struct{}{}[config.MusicFlagFadeIn-musicEffectFadeIn]
var _ = [1]struct{}{}[config.MusicFlagFadeOut-musicEffectFadeOut]
var _ = [1]struct{}{}[config.MusicFlagSyncPos-musicEffectSyncPos]

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
	// loop / effects carry the 2.9 MC play semantics for a pendingMusic entry
	// (#15): whether to loop, and the MUSIC_EFFECT bit flags (FADE_IN). They are
	// read in startMusic when the track's bytes arrive — never from stale args.
	loop    bool
	effects int
	// seekSec is a best-effort start position (seconds) for a cross-tab resume:
	// >0 means seek there once the stream loads (PlayMusicAt); 0 = play from top.
	seekSec float64
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

	// modBan/modKick/modMute are the built-in defaults for the optional
	// mod-command feedback sounds (#60) — distinct synthesized tones, the
	// fallback when the user sets no custom file for an action. Same lifecycle
	// as alert (synth once, freed at close).
	modBan, modKick, modMute *mix.Chunk

	musicBytes []byte // keeps streamed music memory alive while playing
	musicRW    *sdl.RWops
	music      *mix.Music
	musicURL   string // the URL of the track currently playing ("" = none); makes PlayMusic
	//          idempotent so a room rebuild (random char, char switch, tab reactivation)
	//          re-seeding the SAME track doesn't restart it.
	musicLoop bool // whether the current stream loops forever; a non-looping (2.9
	//          NO_REPEAT, #15) stream is reaped by reapFinishedMusic when it ends, so a
	//          finished one-shot stops lying to the "is music playing" guards. A looping
	//          track never ends on its own, so it's never reaped.
	// Crossfade transient (#112): while a true crossfade runs, the OLD track is
	// kept alive here and faded out natively by SDL Mixer X
	// (Mix_FadeOutMusicStream) in parallel with the new track in `music`.
	// Zero/nil when no crossfade is in flight; stepFadeOutOld reaps the old
	// track once the native fade has halted it.
	musicOld      *mix.Music
	musicOldRW    *sdl.RWops
	musicOldBytes []byte

	// loopMeta caches sidecar fetch/parse results (#48): keyed by music URL (full,
	// host-included), holding either real loop points or the fact that none exist
	// (conclusively missing: fetch 404, parse failure, direct URL). Entries expire
	// after loopMetaCacheMaxAge and are never re-probed inside TTL (hard rule #6).
	loopMeta map[string]loopMeta

	// swapSnaps records, keyed by an OUTGOING music URL, the true position that
	// track was at the instant startMusic swapped it out for a new URL (a
	// "takeover"). The cross-tab resume (internal/ui) reads it to seek a parked
	// tab's track back to exactly where it was: snapPos + wall time since the
	// snap, loop-wrapped by snapDur. Capturing at takeover (not at request time)
	// is the whole point — the old track keeps PLAYING while the new track
	// downloads, and those seconds must be counted.
	swapSnaps     map[string]musicSwapSnap
	swapSnapOrder []string // insertion order, oldest first, for cap eviction

	// Volumes in percent (0–100), applied as mixer volume at play time
	// (music globally, chunks per returned channel).
	musicVol, sfxVol, blipVol int
	// alertVol is the callword/friend ping volume — independent of SFX so a quiet
	// SFX mix (or the SFX mute) can't silence your name-pings.
	alertVol int
	// blipScale is the current speaker's per-character blip attenuation (0–100,
	// 100 = none; M11), set per message via SetBlipScale and multiplied into
	// blipVol when a blip plays.
	blipScale int

	enabled bool
}

// fullVolumePercent maps 100% onto SDL_mixer's MAX_VOLUME.
const fullVolumePercent = 100

func mixVolume(pct int) int { return pct * mix.MAX_VOLUME / fullVolumePercent }

// SetVolumes applies the user's music/SFX/blip volumes (0–100). Music
// volume takes effect immediately on the LIVE stream: SDL Mixer X plays music
// via PlayStream/FadeInStream, and a playing stream's volume is per-stream
// (SetVolumeStream) — the global VolumeMusic only seeds FUTURE LoadMUS, so it
// alone can't change what's already audible (the "music slider does nothing"
// report). The global is still kept in sync as the next stream's default.
// Chunk (SFX/blip) volumes apply per play.
func (a *Audio) SetVolumes(music, sfx, blip int) {
	a.musicVol, a.sfxVol, a.blipVol = music, sfx, blip
	if a.enabled {
		mix.VolumeMusic(mixVolume(music))
		if a.music != nil {
			a.music.SetVolumeStream(mixVolume(music))
		}
	}
}

// SetAlertVolume sets the callword/friend ping volume (0–100), independent of
// SFX. Applied to the reserved alert channel at the next ping.
func (a *Audio) SetAlertVolume(pct int) { a.alertVol = pct }

// SetBlipScale sets the current speaker's per-character blip attenuation (0–100;
// 100 = full blip volume, M11). Multiplied into the blip channel volume at the
// next blip play; set once per message by the courtroom from per-char prefs.
func (a *Audio) SetBlipScale(pct int) {
	if pct < 0 {
		pct = 0
	}
	if pct > fullVolumePercent {
		pct = fullVolumePercent
	}
	a.blipScale = pct
}

// NewAudio opens the mixer. A failed device (headless CI) degrades to a
// disabled-but-functional sink.
func NewAudio(mgr *assets.Manager) *Audio {
	// Enable debug log capture so render package log.Printf appears in F8 panel
	EnableDebugLogCapture()

	a := &Audio{
		mgr:       mgr,
		chunks:    map[string]*mix.Chunk{},
		pending:   map[string]pendingPlay{},
		loopMeta:  map[string]loopMeta{},
		musicVol:  fullVolumePercent,
		sfxVol:    fullVolumePercent,
		blipVol:   fullVolumePercent,
		alertVol:  fullVolumePercent,
		blipScale: fullVolumePercent,
	}
	// Load the dynamic decoder libraries we ship: opus/ogg/mp3/flac are pulled
	// in on demand (only WAV is built into SDL_mixer), so without this they
	// never decode. FLAC is enabled because .flac sits in the custom-music
	// allowlist (config.musicFileExts) and the macOS bundle asserts libFLAC is
	// staged (bundle-macos.sh check_present) — without the Init flag those two
	// promises pointed at a codec that could never load. Best-effort — Init
	// reports an error if any one codec's DLL is missing, but the codecs that
	// DID load stay usable, so we log and continue.
	if err := mix.Init(mix.INIT_OGG | mix.INIT_MP3 | mixInitOpus | mix.INIT_FLAC); err != nil {
		log.Printf("render: some audio codecs unavailable (opus/ogg/mp3/flac): %v", err)
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
	a.loadModSounds()
	return a
}

// ModAction identifies which mod-command feedback sound to play (#60).
type ModAction int

const (
	ModBan ModAction = iota
	ModKick
	ModMute
)

// loadModSounds synthesizes the three built-in mod-action defaults. Best-effort
// (same as loadAlert): a failure leaves the chunk nil and PlayModAction no-ops
// for that action.
func (a *Audio) loadModSounds() {
	a.modBan = loadSynth(builtinModWAV(ModBan))
	a.modKick = loadSynth(builtinModWAV(ModKick))
	a.modMute = loadSynth(builtinModWAV(ModMute))
}

// loadSynth turns an in-memory WAV into a mixer chunk (nil on failure).
func loadSynth(wav []byte) *mix.Chunk {
	rw, err := sdl.RWFromMem(wav)
	if err != nil {
		return nil
	}
	chunk, err := mix.LoadWAVRW(rw, true) // mixer frees the RW
	if err != nil {
		log.Printf("render: built-in mod sound failed: %v", err)
		return nil
	}
	return chunk
}

// PlayModAction plays the feedback sound for a mod action (#60): the user's
// custom file if set, else the synthesized built-in default. Routed on the
// reserved alert channel (its own volume, never cut by blips/SFX), same as the
// callword/friend ping. No-op when the device is disabled.
func (a *Audio) PlayModAction(action ModAction, customPath string) {
	if !a.enabled {
		return
	}
	if customPath != "" {
		a.PlayFile(customPath)
		return
	}
	var ch *mix.Chunk
	switch action {
	case ModBan:
		ch = a.modBan
	case ModKick:
		ch = a.modKick
	case ModMute:
		ch = a.modMute
	}
	if ch != nil {
		a.playChunk(ch, pendingAlert)
	}
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

// modSegment is one tone in a synthesized mod-action sound: a sine that glides
// from freqStart to freqEnd over ms (equal = steady), then gapMs of silence.
type modSegment struct {
	freqStart, freqEnd float64
	ms, gapMs          int
	amp                float64
}

// synthSegments renders a sequence of modSegments to an in-memory mono WAV. Pure
// generation, run once at open (no hot path). Phase is accumulated across the
// glide so a frequency sweep stays continuous, and each segment's envelope
// decays to zero at its end so the segment seams never click.
func synthSegments(segs []modSegment) []byte {
	const sr = 44100
	const attack = 0.06 // fraction of a segment spent ramping in (anti-click)
	pcm := make([]byte, 0, 4096)
	phase := 0.0
	for _, s := range segs {
		n := sr * s.ms / 1000
		for i := 0; i < n; i++ {
			prog := float64(i) / float64(n) // 0..1 through the segment
			freq := s.freqStart + (s.freqEnd-s.freqStart)*prog
			env := 1.0
			if prog < attack {
				env = prog / attack
			} else {
				env = 1 - (prog-attack)/(1-attack)
			}
			v := int16(math.Sin(phase) * s.amp * env * math.MaxInt16)
			pcm = append(pcm, byte(v), byte(v>>8))
			phase += 2 * math.Pi * freq / sr
		}
		for i := 0; i < sr*s.gapMs/1000; i++ {
			pcm = append(pcm, 0, 0)
		}
	}
	return wavMono16(pcm, sr)
}

// builtinModWAV synthesizes the default sound for a mod action — each distinct
// so they're tell-apart by ear: ban = a low, heavy two-tone "thud"; kick = two
// quick punchy blips; mute = a muffled downward sweep ("shush").
func builtinModWAV(action ModAction) []byte {
	switch action {
	case ModKick:
		return synthSegments([]modSegment{
			{freqStart: 620, freqEnd: 620, ms: 80, gapMs: 55, amp: 0.38},
			{freqStart: 820, freqEnd: 820, ms: 95, gapMs: 0, amp: 0.38},
		})
	case ModMute:
		return synthSegments([]modSegment{
			{freqStart: 640, freqEnd: 200, ms: 420, gapMs: 0, amp: 0.34},
		})
	default: // ModBan
		return synthSegments([]modSegment{
			{freqStart: 220, freqEnd: 220, ms: 180, gapMs: 60, amp: 0.42},
			{freqStart: 160, freqEnd: 160, ms: 360, gapMs: 0, amp: 0.42},
		})
	}
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
	for _, c := range []*mix.Chunk{a.modBan, a.modKick, a.modMute} {
		if c != nil {
			c.Free()
		}
	}
	a.modBan, a.modKick, a.modMute = nil, nil, nil
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
			a.stepFadeOutOld()    // reap the crossfade's old track once its native fade halts it
			a.reapFinishedMusic() // clear a naturally-ended one-shot so it's not seen as "still playing"
			return
		}
	}
}

// reapFinishedMusic clears a.music/a.musicURL once a NON-looping track has played to
// its natural end, so the "is music playing" signal reflects reality. WHY this exists:
// a play-once track (2.9 NO_REPEAT / looping=0, #15) leaves a.music non-nil after it
// finishes — there is no natural-end observer otherwise (no HookMusicFinished callback,
// which would fire on a C thread and can't touch this state under rule #1). Stale
// a.music != nil then lies to two guards: PlayMusic's idempotency check (audio.go:687)
// makes a re-click of the finished song a no-op, and MusicPlaying() staying true makes
// the ui-side cross-tab resume treat a dead stream as live (sameTrack=true → no re-
// fetch), so switching back to a tab whose one-shot ended finds silence. Polling
// Mix_PlayingMusicStream here (render thread, in BOTH the Frame and Background loops that
// call Audio.Frame) is the portable, render-thread-safe fix — no C-callback needed.
//
// Only reaps a loaded, non-looping stream that has stopped: a looping track never
// stops on its own, and music is never paused in this client (no Mix_PauseMusic path),
// so !PlayingStream() with a.music != nil is unambiguously a finished one-shot — never
// a pause or a load gap (startMusic's Play() begins synchronously before this runs).
func (a *Audio) reapFinishedMusic() {
	if !a.enabled || a.music == nil {
		return // nothing loaded or disabled
	}
	// Normal reap: only non-looping tracks that have naturally stopped.
	if a.musicLoop {
		return // looping track never ends by itself
	}
	if a.music.PlayingStream() {
		return // still rolling
	}
	a.stopMusic() // natural end: free the finished stream and clear musicURL so it can be re-triggered/re-fetched
}

func (a *Audio) onAudioBytes(asset assets.AudioAsset) {
	if !a.enabled {
		delete(a.pending, asset.Base)
		return
	}
	p, wanted := a.pending[asset.Base]
	if wanted && p.kind == pendingMusic {
		delete(a.pending, asset.Base)
		a.startMusic(asset.Base, asset.Data, p.loop, p.effects, p.seekSec) // 2.9 loop/effects (#15) + cross-tab resume seek
		return
	}
	// Diagnostic (late-music-drop): a music track delivered with NO pending entry
	// was requested but its entry expired or was purged before the bytes landed —
	// the download outlived pendingPlayTTL. loadChunk can't decode a stream and
	// !wanted short-circuits below, so these bytes are silently discarded. Log it
	// so the "big music silently doesn't play" report can be confirmed in the wild.
	if asset.Type == assets.AssetTypeMusic {
		log.Printf("render: late music dropped for %q (no pending entry)", asset.Base)
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
	a.evictChunk()
	a.chunks[base] = chunk
	a.chunkOrder = append(a.chunkOrder, base)
	return chunk
}

// evictChunk makes room in the chunk cache before a new insert, freeing at most
// one victim. It is a use-after-free guard around Mix_FreeChunk (#4): a cached
// chunk can still be playing on a mixer channel, and freeing a playing chunk
// corrupts SDL_mixer's C audio callback — a crash that -race can't see (the
// callback runs on a C thread, invisible to the race detector), so correctness
// here is argued BY CONSTRUCTION, not by a test.
//
// Invariant: at most mixChannelCount (16) chunks can be playing at once, and
// 16 < chunkCacheMax (64), so a non-playing victim always exists among the 64
// oldest. We therefore scan the mixer's in-use chunk pointers, then evict the
// OLDEST chunk that isn't currently playing (index-based middle removal, not a
// head-only slice — the oldest may be the one still playing). If every one of
// the chunkCacheMax oldest were somehow playing (impossible under the
// invariant), we grow-and-log up to chunkCacheHardMax rather than free a
// playing chunk. Render-thread only: every caller (loadChunk, PlayFile) runs on
// the render/game thread, the only place SDL_mixer may be touched (rule #1).
func (a *Audio) evictChunk() {
	if len(a.chunkOrder) < chunkCacheMax {
		return
	}
	// Build the set of chunk pointers the mixer is currently playing. Scan ALL
	// allocated channels, including the reserved blip/alert channels (0,1): a
	// cached blip chunk lives on channel 0 and must not be freed under it.
	playing := make(map[*mix.Chunk]struct{}, mixChannelCount)
	for ch := 0; ch < mixChannelCount; ch++ {
		if mix.Playing(ch) != 0 {
			if c := mix.GetChunk(ch); c != nil {
				playing[c] = struct{}{}
			}
		}
	}
	// Evict the oldest chunk that isn't playing.
	for i, base := range a.chunkOrder {
		c, ok := a.chunks[base]
		if !ok {
			// Stale order entry (shouldn't happen) — drop it and keep scanning.
			a.chunkOrder = append(a.chunkOrder[:i:i], a.chunkOrder[i+1:]...)
			return
		}
		if _, busy := playing[c]; busy {
			continue // still on a mixer channel — skip to the next-oldest
		}
		c.Free()
		delete(a.chunks, base)
		a.chunkOrder = append(a.chunkOrder[:i:i], a.chunkOrder[i+1:]...)
		return
	}
	// Unreachable under the invariant (at most mixChannelCount play at once, and
	// mixChannelCount < chunkCacheMax): every one of the oldest chunks is playing.
	// We keep the cache bounded (rule #4) by growing to chunkCacheHardMax; only
	// at that ceiling do we HALT the channels playing the oldest chunk and then
	// free it (halt-before-free is defined behavior — the C callback stops
	// touching the chunk before Free), so the cache can never exceed the hard cap.
	if len(a.chunkOrder) < chunkCacheHardMax {
		return // grow silently; a non-playing victim will free up next insert
	}
	oldest := a.chunkOrder[0]
	c := a.chunks[oldest]
	log.Printf("render: audio chunk cache at hard cap %d with all oldest chunks playing; halting to free %q",
		chunkCacheHardMax, oldest)
	for ch := 0; ch < mixChannelCount; ch++ {
		if mix.GetChunk(ch) == c {
			mix.HaltChannel(ch) // stop the C callback before we free the chunk
		}
	}
	if c != nil {
		c.Free()
	}
	delete(a.chunks, oldest)
	a.chunkOrder = a.chunkOrder[1:]
}

func (a *Audio) playChunk(chunk *mix.Chunk, kind pendingKind) {
	switch kind {
	case pendingBlip:
		// Blips replace each other on the reserved channel — playing one
		// is a pointer pass (spec §8).
		mix.HaltChannel(blipChannel)
		if _, err := chunk.Play(blipChannel, 0); err == nil {
			mix.Volume(blipChannel, mixVolume(a.blipVol*a.blipScale/fullVolumePercent)) // M11 per-char blip scale
		}
	case pendingAlert:
		// Callword/friend ping on its own reserved channel: a new alert replaces
		// the old (no stacking), and neither blips nor a burst of emote SFX can
		// steal or halt it. Its OWN volume (alertVol), independent of SFX.
		mix.HaltChannel(alertChannel)
		if _, err := chunk.Play(alertChannel, 0); err == nil {
			mix.Volume(alertChannel, mixVolume(a.alertVol))
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
		a.evictChunk() // UAF guard: never free a chunk still on a mixer channel (#4)
		a.chunks[path] = c
		a.chunkOrder = append(a.chunkOrder, path)
		chunk = c
	}
	a.playChunk(chunk, pendingAlert) // alert channel: a custom callword/friend sound is never cut by blips
}

// request plays the chunk for base now if cached, else marks it pending and —
// for SFX/shout, which nothing else prefetches — fetches the bytes itself.
// Blips are prefetched by the courtroom (resolveBlip); music self-fetches in
// PlayMusic.
func (a *Audio) request(base string, kind pendingKind) {
	if base == "" || !a.enabled {
		return
	}
	if chunk, ok := a.chunks[base]; ok {
		a.playChunk(chunk, kind)
		return
	}
	a.pending[base] = pendingPlay{kind: kind, deadline: time.Now().Add(pendingPlayTTL)}
	// Music self-fetches in PlayMusic and blips are prefetched by the courtroom
	// (resolveBlip); the emote SFX, the dropdown audition and the shout cry are
	// NOT prefetched anywhere, so fetch them here — without this the pending entry
	// above just expires after pendingPlayTTL and the sound never plays.
	if (kind == pendingSFX || kind == pendingShout) && a.mgr != nil {
		a.mgr.Prefetch(base, assets.AssetTypeSFX, network.PriorityHigh) // AssetType: SFX
	}
}

func (a *Audio) expirePending() {
	if len(a.pending) == 0 {
		return
	}
	now := time.Now()
	for base, p := range a.pending {
		// Music is the one large/slow kind: the network layer budgets up to
		// bodyTransferBudget (60s) for a single track body, so the 10s pending
		// expiry here would drop a still-in-flight download's pending entry and
		// silently discard the bytes when they land late (onAudioBytes finds no
		// entry → "big music silently doesn't play"). Music pending entries are
		// already bounded to at most one and fully lifecycle-managed by
		// purgePendingMusic (supersession), StopMusic (cancellation) and
		// onAudioBytes (delivery), so the timer adds nothing but this bug — leave
		// them alone.
		if p.kind == pendingMusic {
			continue
		}
		if now.After(p.deadline) {
			delete(a.pending, base)
		}
	}
}

// --- courtroom.AudioSink ------------------------------------------------------

// PlayShout plays a character's shout cry. // AssetType: SFX
func (a *Audio) PlayShout(base string) { a.request(base, pendingShout) }

// PlaySFX plays an emote sound effect immediately. The SFX_DELAY is applied
// UPSTREAM by the courtroom's Update tick (armSFXDelay / fireSFXDelay, #12):
// the courtroom holds the play until the deadline, so by the time this is
// called the delay has already elapsed — the duration arg is vestigial (the
// AudioSink signature keeps it) and intentionally ignored here. // AssetType: SFX
func (a *Audio) PlaySFX(base string, _ time.Duration) { a.request(base, pendingSFX) }

// PlayBlip fires one chat blip. // AssetType: Blip
func (a *Audio) PlayBlip(base string) { a.request(base, pendingBlip) }

// PlayMusic streams a track from its full URL. loop=false plays it once (2.9
// NO_REPEAT / looping=0, #15); effects carries the MUSIC_EFFECT bit flags
// (FADE_IN, FADE_OUT, SYNC_POS — #112). Idempotent: if that exact track is
// already playing, it's a no-op — so a room rebuild (random char, char switch,
// tab reactivation) re-seeding the current song doesn't restart it from the top
// (a re-seed's loop/effects are intentionally ignored, same reasoning as the
// no-restart). A genuinely new track (or a resume after the song stopped) plays
// as normal. // AssetType: Music
func (a *Audio) PlayMusic(url string, loop bool, effects int) {
	if !a.enabled {
		return
	}
	if url == a.musicURL && a.music != nil {
		return // already playing this exact track — don't restart
	}
	// FADE_OUT (#112): with SDL Mixer X's multi-music support we now do AO2's
	// true overlapping crossfade — the PREVIOUS track keeps playing at full
	// volume while the new one downloads, then startMusic fades the old out
	// (dB-linear log ramp) in parallel with the new. Nothing is armed here; the
	// effects bits ride on the pending entry and startMusic reads them.
	if effects != 0 {
		log.Printf("render: PlayMusic %q loop=%v effects=0x%x (fadeIn=%v fadeOut=%v syncPos=%v)",
			url, loop, effects,
			effects&musicEffectFadeIn != 0,
			effects&musicEffectFadeOut != 0,
			effects&musicEffectSyncPos != 0)
	}
	var syncPosSec float64
	if a.music != nil && a.musicURL != "" {
		// SYNC_POS: capture the PREVIOUS track's position NOW, before the new one
		// overwrites it (#112). AO2: ../AO2-Client/src/aomusicplayer.cpp:132-136
		// reads the old stream's pos, then plays the new one at that exact spot.
		// We record it on the pendingPlay entry; startMusic consumes it as seekSec.
		if effects&musicEffectSyncPos != 0 {
			if pos, _, ok := a.MusicClock(); ok {
				syncPosSec = pos
			}
		}
	}
	// Purge any OTHER pendingMusic entry from a prior, still-in-flight PlayMusic
	// so at most one music track is ever pending at a time (§1.2). Without this,
	// switching tracks A→B leaves both pending; if A's fetch lands after B was
	// requested, onAudioBytes matches A and playback reverts to the stale track.
	// The superseded delivery is IGNORED on arrival (onAudioBytes finds it absent
	// and falls through to loadChunk) — never dropped from the channel (rule 7).
	// PlayMusic and onAudioBytes both run on the locked render thread, so this
	// purge is atomic with respect to deliveries — no generation counter needed.
	a.purgePendingMusic(url)
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL), loop: loop, effects: effects, seekSec: syncPosSec}
	a.mgr.PrefetchExact(url, assets.AssetTypeMusic, network.PriorityHigh) // AssetType: Music
}

// purgePendingMusic deletes every pendingMusic entry except keep (pass "" to
// drop all of them). It leaves non-music pending entries untouched. Guarantees
// at most one pendingMusic key survives so a late delivery for a superseded
// track is ignored on arrival (§1.2). Render thread only; only touches the
// local a.pending map, never a pool result (rule 7).
func (a *Audio) purgePendingMusic(keep string) {
	for u, p := range a.pending {
		if p.kind == pendingMusic && u != keep {
			delete(a.pending, u)
		}
	}
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
	a.purgePendingMusic("") // a pending fetch would otherwise start on arrival
}

// CurrentMusicURL is the URL of the track the single music stream is currently
// playing ("" = nothing). Multi-tab music continuity (see internal/ui parkActive)
// reads it to decide whether the active tab OWNS the live stream: if the tab's
// resumed track equals this, PlayMusic is a no-op and the stream (and its
// position) survives the switch untouched; if it differs, the stream must swap.
// Render thread only.
func (a *Audio) CurrentMusicURL() string { return a.musicURL }

// MusicPlaying reports whether the single music stream is live (a track is
// loaded and rolling). Cross-tab continuity uses it to know there's a stream to
// keep alive (ducked) across a switch rather than one to restart. Render thread
// only.
func (a *Audio) MusicPlaying() bool { return a.enabled && a.music != nil }

// musicSwapSnap is one takeover snapshot: the outgoing track's true position and
// total duration (both seconds, from MusicClock) captured the instant a new URL
// swapped it out, plus the wall time of the capture. The cross-tab resume adds
// (now - at) to posSec and loop-wraps by durSec to land exactly where the parked
// track would be now.
type musicSwapSnap struct {
	posSec float64
	durSec float64
	at     time.Time
}

// musicSwapSnapCap bounds the takeover-snapshot map. 16 is comfortably above the
// tab ceiling (a parked tab is the only thing whose stream gets swapped out and
// later resumed), so every parked tab's outgoing track keeps a live snapshot; on
// overflow the OLDEST entry is evicted (insertion order via swapSnapOrder), which
// can only drop a snapshot older than any resumable tab.
const musicSwapSnapCap = 16

// recordSwapSnap stores (or refreshes) the takeover snapshot for an outgoing
// music URL. Callers pass the position/duration read from MusicClock at the swap;
// only ok reads are recorded (a mod/midi with unknown duration, or unresolved
// symbols, records NOTHING — its absence makes the resume fall back to the old
// wall-clock math rather than seek into a track whose length we can't know).
// Re-parking a URL that's already snapped updates the value in place and keeps
// its ORIGINAL order slot, so the insertion-order slice never desyncs from the
// map (a duplicate order entry would evict the wrong key at cap). Render thread
// only; the map is a plain field, no lock (rule #1 hot path is untouched).
func (a *Audio) recordSwapSnap(url string, posSec, durSec float64, at time.Time) {
	if url == "" {
		return
	}
	if a.swapSnaps == nil {
		a.swapSnaps = make(map[string]musicSwapSnap, musicSwapSnapCap)
	}
	if _, exists := a.swapSnaps[url]; !exists {
		if len(a.swapSnapOrder) >= musicSwapSnapCap {
			oldest := a.swapSnapOrder[0]
			a.swapSnapOrder = a.swapSnapOrder[1:]
			delete(a.swapSnaps, oldest)
		}
		a.swapSnapOrder = append(a.swapSnapOrder, url)
	}
	a.swapSnaps[url] = musicSwapSnap{posSec: posSec, durSec: durSec, at: at}
}

// SwappedOutSnap returns the takeover snapshot recorded for url (the position the
// track was at when it was last swapped out, its duration, and the wall time of
// that capture), or ok=false when none exists. The cross-tab resume calls this to
// compute an exact, loop-aware seek; a miss (old build, mod/midi, symbols
// unresolved, or never parked) means it falls back to the wall-clock estimate.
// Render thread only.
func (a *Audio) SwappedOutSnap(url string) (posSec, durSec float64, at time.Time, ok bool) {
	s, ok := a.swapSnaps[url]
	if !ok {
		return 0, 0, time.Time{}, false
	}
	return s.posSec, s.durSec, s.at, true
}

// PlayMusicAt is PlayMusic with a best-effort SEEK to seekSec seconds into the
// track, for resuming a backgrounded tab's song near where it would be now
// (cross-tab music continuity). Same idempotency guard as PlayMusic: if that
// exact URL is already the live stream this is a no-op (the stream — and its true
// position — is already preserved, so the virtual seek is neither needed nor
// wanted). Otherwise it fetches and, on decode, seeks. seekSec<=0 behaves exactly
// like PlayMusic (play from the top). // AssetType: Music
func (a *Audio) PlayMusicAt(url string, loop bool, effects int, seekSec float64) {
	if !a.enabled {
		return
	}
	if url == a.musicURL && a.music != nil {
		return // already this exact stream — never restart or re-seek a live track
	}
	a.purgePendingMusic(url)
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL), loop: loop, effects: effects, seekSec: seekSec}
	a.mgr.PrefetchExact(url, assets.AssetTypeMusic, network.PriorityHigh) // AssetType: Music
}

// startMusic decodes and plays a fetched track. loop drives the SDL_mixer loop
// count (-1 = forever, 0 = play once — 2.9 looping/NO_REPEAT, #15); effects
// carries the MUSIC_EFFECT bit flags (FADE_IN, FADE_OUT, SYNC_POS — #112);
// seekSec is a best-effort start position (>0 = seek there, 0 = play from top).
// Render thread only.
//
// Effect handling (../AO2-Client/src/aomusicplayer.cpp:24-160):
//   - FADE_IN: honored via SDL_mixer's native Mix_FadeInMusic, which ramps the
//     new stream up over musicFadeInMs on the C audio callback (no goroutine —
//     rule #1) and always scales toward the current VolumeMusic, so a volume
//     slider drag mid-fade just changes the ceiling (no fight).
//   - FADE_OUT: armed in PlayMusic at request time (#112). The PREVIOUS track
//     fades down WHILE the new track downloads (zero added latency vs. today's
//     hard chop). When the bytes land here, stopMusic calls Mix_HaltMusic
//     (cancels any remaining fade) then Frees — no use-after-free, no deferred
//     swap, no stale-position bug. AO2's true overlapping crossfade (both streams
//     audible) is impossible with SDL_mixer's single Mix_Music slot.
//   - SYNC_POS: captured in PlayMusic at request time (#112), before the new
//     track overwrites the old one's clock. Delivered here via seekSec and routed
//     through the existing cross-tab resume seek logic below. An explicit resume
//     seek (PlayMusicAt) outranks a SYNC_POS capture (the resume is a deliberate
//     user-visible continuity feature; SYNC_POS is a DJ nicety).
//
// seekSec best-effort notes: Mix_SetMusicPosition only supports some formats
// (ogg/mp3/mod, and flac/opus with a new enough SDL_mixer), returning -1
// otherwise, so a failed seek degrades to playing from the top (today's behavior)
// rather than faking anything. CAUTION reading its failure log: SDL_mixer masks
// EVERY negative return from the codec's Seek with the one generic "Position not
// implemented for music type" string (release-2.8.x src/music.c Mix_SetMusicPosition
// — the same Mix_SetError used when Seek is absent). So that message usually
// means an OUT-OF-RANGE target the codec rejected (e.g. opusfile's op_pcm_seek
// returns OP_EINVAL for a target past the track's end), NOT a missing codec; the
// duration wrap in the seek block below exists so we never hand SDL_mixer such a
// target.
func (a *Audio) startMusic(url string, data []byte, loop bool, effects int, seekSec float64) {
	// Takeover snapshot (cross-tab resume, wave 13): this call is about to SWAP a
	// live, different track out for `url`. Capture the OUTGOING track's true
	// position NOW — before stopMusic() frees it — so a later resume of that
	// parked track can seek to exactly where it was plus the seconds it keeps
	// playing while `url` downloads. Only a readable (ok) clock is recorded;
	// mod/midi (duration -1) or a pre-2.6 SDL_mixer records nothing, and the
	// resume falls back to the old wall-clock estimate. Read BEFORE stopMusic,
	// which nils a.music and would make MusicClock report "no stream".
	if a.music != nil && a.musicURL != "" && a.musicURL != url {
		if pos, dur, ok := a.MusicClock(); ok {
			a.recordSwapSnap(a.musicURL, pos, dur, time.Now())
		}
	}
	// True crossfade (#112): if FADE_OUT is set and a live, audible old track is
	// still rolling, keep it alive (move it to the musicOld slot) and fade it out
	// in parallel with the new track, instead of hard-stopping it.
	crossfade := effects&musicEffectFadeOut != 0 && a.music != nil && a.musicVol > 0
	if effects != 0 {
		log.Printf("render: startMusic %q effects=0x%x crossfade=%v musicVol=%d seek=%.2fs",
			url, effects, crossfade, a.musicVol, seekSec)
	}
	if crossfade {
		a.musicOld = a.music
		a.musicOldRW = a.musicRW
		a.musicOldBytes = a.musicBytes
		a.music = nil
		a.musicRW = nil
		a.musicBytes = nil
	} else {
		a.stopMusic() // clears musicURL; set below only on a successful start
	}
	rw, err := sdl.RWFromMem(data)
	if err != nil {
		a.restoreOldMusic()
		return
	}
	music, err := mix.LoadMUSRW(rw, 0) // we own the RW; bytes stay alive below
	if err != nil {
		log.Printf("render: music decode failed: %v", err)
		_ = rw.Free()
		a.restoreOldMusic()
		return
	}
	a.musicBytes = data // pin the payload while the mixer streams from it
	a.musicRW = rw
	a.music = music
	a.musicLoop = loop // remember loop-ness so reapFinishedMusic only reaps a finished one-shot
	// SDL Mixer X PlayStream/FadeInStream loop counts: -1 = loop forever;
	// playOnceLoops (1) = play through exactly once. We use 1, not 0, for
	// play-once: the API documents loops as "play the music loop times through",
	// so 1 is unambiguously one play, while 0 reads as zero plays (a silent
	// no-loop track — the exact bug #15 is fixing).
	const (
		loopForeverLoops = -1
		playOnceLoops    = 1
	)
	loops := playOnceLoops
	if loop {
		loops = loopForeverLoops
	}
	// Set the new stream's target volume BEFORE the fade-in so the ramp climbs to
	// the right ceiling (SetVolumeStream is the per-stream equivalent of VolumeMusic).
	music.SetVolumeStream(mixVolume(a.musicVol))
	if effects&musicEffectFadeIn != 0 {
		err = music.FadeInStream(loops, musicFadeInMs) // linear 1000ms ramp (AO2 FADE_IN)
	} else {
		err = music.PlayStream(loops)
	}
	if err != nil {
		log.Printf("render: music play failed: %v", err)
		music.HaltStream()
		music.Free()
		a.music = nil
		a.musicRW = nil
		a.musicBytes = nil
		a.restoreOldMusic()
		return
	}
	if crossfade {
		// Start the old track's native fade-out now that the new track is live.
		// Mix_FadeOutMusicStream is linear (SDL Mixer X has no log fade), which is
		// close enough to AO2's BASS_SLIDE_LOG for a 4s music fade; it fades in the
		// audio callback and halts the stream when done (stepFadeOutOld reaps it).
		a.musicOld.FadeOutStream(musicFadeOutMs)
	}
	// Cross-tab resume + SYNC_POS seek (best-effort): jump to where the track
	// should be. An explicit resume seek (PlayMusicAt) outranks a SYNC_POS
	// capture (the resume is deliberate user continuity; SYNC_POS is a DJ
	// nicety). We seek the NEW stream explicitly (Mix_SetMusicPositionStream),
	// not the global Mix_SetMusicPosition, so a crossfade's old stream is left
	// untouched.
	if seekSec > 0 {
		// Wrap the target by the freshly-loaded stream's REAL length first: the
		// ui-side wall-clock fallback is NOT duration-wrapped, and AO area loops
		// are short, so its target routinely lands past the end — which opusfile
		// rejects (OP_EINVAL) rather than degrading quietly.
		durSec := -1.0 // MusicClock's own "unknown length" sentinel; logged below
		if _, dur, ok := a.MusicClock(); ok && dur > 0 {
			durSec = dur
			seekSec = math.Mod(seekSec, dur)
		}
		if seekSec > 0 {
			if err := music.SeekStream(seekSec); err != nil {
				// Not fatal — playback continues from the start.
				log.Printf("render: music seek to %.0fs (track length %.0fs) failed for %q (playing from top)", seekSec, durSec, url)
			}
		}
	}
	a.musicURL = url // now this exact track is playing — PlayMusic(url) becomes a no-op
	// Keep the global music volume in sync for future LoadMUS (each new stream's
	// default); the live stream was already set via SetVolumeStream above.
	mix.VolumeMusic(mixVolume(a.musicVol))
	// Loop points (#48): when the stream is looping, apply any custom sidecar
	// loop points to SDL Mixer X's native loop_start/loop_end. The mixer then
	// loops gaplessly at the decoder level (no deadline polling).
	if loop {
		a.applyLoopPoints(url, data)
	}
}

func (a *Audio) stopMusic() {
	if a.music != nil {
		a.music.HaltStream()
		a.music.Free()
		a.music = nil
	}
	if a.musicRW != nil {
		_ = a.musicRW.Free()
		a.musicRW = nil
	}
	a.musicBytes = nil
	a.musicURL = "" // nothing playing now; a later PlayMusic of the same URL plays again
	// No stream loaded → loop-ness is moot; reset so a stale true can't skip a future reap.
	a.musicLoop = false
	// Tear down any in-flight crossfade's old track too (StopMusic mid-fade).
	if a.musicOld != nil {
		a.musicOld.HaltStream()
		a.musicOld.Free()
		a.musicOld = nil
	}
	if a.musicOldRW != nil {
		_ = a.musicOldRW.Free()
		a.musicOldRW = nil
	}
	a.musicOldBytes = nil
}

// restoreOldMusic puts a crossfade's old track back into the live slot after a
// failed load/play, so the previous track keeps playing instead of dropping out.
// No-op when there is no in-flight crossfade.
func (a *Audio) restoreOldMusic() {
	if a.musicOld == nil {
		return
	}
	a.music = a.musicOld
	a.musicRW = a.musicOldRW
	a.musicBytes = a.musicOldBytes
	a.musicOld = nil
	a.musicOldRW = nil
	a.musicOldBytes = nil
}

// stepFadeOutOld reaps a crossfade's old track once its native fade-out has
// halted it. The fade itself is Mix_FadeOutMusicStream (linear, started in
// startMusic); this just polls PlayingStream and frees the stream when the
// fade has run its course. Called every Frame on the render thread.
func (a *Audio) stepFadeOutOld() {
	if a.musicOld == nil {
		return
	}
	if a.musicOld.PlayingStream() {
		return // native fade still running
	}
	log.Printf("render: fade-out of old track complete")
	a.musicOld.Free()
	a.musicOld = nil
	if a.musicOldRW != nil {
		_ = a.musicOldRW.Free()
		a.musicOldRW = nil
	}
	a.musicOldBytes = nil
}
