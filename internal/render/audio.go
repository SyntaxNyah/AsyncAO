package render

import (
	"encoding/binary"
	"log"
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/mix"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// Audio device parameters (48 kHz stereo matches modern opus content).
	audioFrequency = 48000
	audioChannels  = 2

	// sfxTrackCount is how many emote SFX and shouts can overlap. Blips and
	// alerts each have a track of their own, so a new blip replaces the last
	// (AO blips never stack) and a callword ping is never cut by the
	// typewriter's first blip or a burst of SFX ("can't hear the callword when
	// blips are on").
	sfxTrackCount = 14

	// chunkCacheMax bounds decoded SFX/blip sounds held in memory. Evicting one
	// that is still playing is safe: SDL3_mixer frees it when the track is done.
	chunkCacheMax = 64

	// pendingPlayTTL drops play requests whose asset never arrived.
	pendingPlayTTL = 10 * time.Second

	// musicFadeInMs is the FADE_IN ramp length (#15). AO2's BASS path slides the
	// new stream's volume up over 1000ms (../AO2-Client/src/aomusicplayer.cpp:160
	// FADE_IN branch); we match it. The ramp is SDL3_mixer's own play-time fade,
	// applied on top of the track gain — so a mid-fade volume-slider drag just
	// changes the ceiling the ramp climbs to (no fight, no extra state).
	musicFadeInMs = 1000
	// musicFadeOutMs is the FADE_OUT ramp length (#112). AO2's BASS path slides
	// the PREVIOUS track down over 4000ms (../AO2-Client/src/aomusicplayer.cpp:141
	// FADE_OUT branch); we match it with SDL3_mixer's linear fade-out stop. The
	// outgoing track fades WHILE the incoming one downloads (zero added latency),
	// then stepFadeOutOld reaps it once it has stopped.
	musicFadeOutMs = 4000

	// Music effect bit flags (MUSIC_EFFECT, ../AO2-Client/src/datatypes.h:95-101).
	// Pinned against config.MusicFlag* to catch config drift at compile time.
	// The looping field / NO_REPEAT are applied by the courtroom before we ever
	// see them (they arrive as the loop bool).
	musicEffectFadeIn  = 1 << 0 // ramp the NEW track up (config.MusicFlagFadeIn)
	musicEffectFadeOut = 1 << 1 // ramp the PREVIOUS track down (config.MusicFlagFadeOut)
	musicEffectSyncPos = 1 << 2 // start new track at previous track's position (config.MusicFlagSyncPos)
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

// Audio implements courtroom.AudioSink over SDL3_mixer: raw bytes from the
// asset pipeline decode in C (spec §8 — no Go audio decoding anywhere).
// All methods run on the render/game thread.
type Audio struct {
	mgr *assets.Manager

	// mixer is nil when no audio device opened (headless CI): every method is
	// then a no-op. The tracks are created with it and live as long as it.
	mixer      *mix.Mixer
	blipTrack  *mix.Track
	alertTrack *mix.Track
	sfxTracks  []*mix.Track

	chunks     map[string]*mix.Audio // key: asset base
	chunkOrder []string              // FIFO eviction order
	pending    map[string]pendingPlay

	// alert is the built-in notification ping — the guaranteed-audible default
	// for callword/friend alerts whenever the user set no custom sound file.
	// Synthesized once at open, freed at close; never enters the asset chunk
	// cache.
	alert *mix.Audio

	// modBan/modKick/modMute are the built-in defaults for the optional
	// mod-command feedback sounds (#60) — distinct synthesized tones, the
	// fallback when the user sets no custom file for an action. Same lifecycle
	// as alert (synth once, freed at close).
	modBan, modKick, modMute *mix.Audio

	// music is the live song; musicOld is the one fading out during a true
	// crossfade (#112). Each slot's audio streams from its bytes, so the bytes
	// stay pinned until the audio is destroyed.
	music    musicSlot
	musicOld musicSlot
	musicURL string // the URL of the track currently playing ("" = none); makes PlayMusic
	//          idempotent so a room rebuild (random char, char switch, tab reactivation)
	//          re-seeding the SAME track doesn't restart it.
	musicLoop bool // whether the current stream loops forever; a non-looping (2.9
	//          NO_REPEAT, #15) stream is reaped by reapFinishedMusic when it ends, so a
	//          finished one-shot stops lying to the "is music playing" guards. A looping
	//          track never ends on its own, so it's never reaped.

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
}

// musicSlot is one music track and the song on it.
type musicSlot struct {
	track *mix.Track
	audio *mix.Audio // nil = no song loaded
	bytes []byte     // the encoded song the audio streams from
}

// fullVolumePercent maps 100% onto a track gain of 1.
const fullVolumePercent = 100

// msPerSecond converts the synthesizers' millisecond lengths to sample counts.
const msPerSecond = 1000

func gain(pct int) float32 { return float32(pct) / fullVolumePercent }

// enabled reports whether an audio device is open.
func (a *Audio) enabled() bool { return a.mixer != nil }

// SetVolumes applies the user's music/SFX/blip volumes (0–100). Music volume
// takes effect immediately on the live track (the "music slider does nothing"
// report came from a volume that only reached FUTURE songs).
func (a *Audio) SetVolumes(music, sfx, blip int) {
	a.musicVol, a.sfxVol, a.blipVol = music, sfx, blip
	if a.enabled() {
		a.music.track.SetGain(gain(music))
	}
}

// SetAlertVolume sets the callword/friend ping volume (0–100), independent of
// SFX. Applied to the reserved alert channel at the next ping.
func (a *Audio) SetAlertVolume(pct int) { a.alertVol = pct }

// SetBlipScale sets the current speaker's per-character blip attenuation (0–100;
// 100 = full blip volume, M11). Multiplied into the blip channel volume at the
// next blip play; set once per message by the courtroom from per-char prefs.
func (a *Audio) SetBlipScale(pct int) {
	pct = value.Clamp(pct, 0, fullVolumePercent)
	a.blipScale = pct
}

// HasChunk reports whether a decoded chunk for base is cached and ready to play
// (the strict-sequencing gate's audio-residency probe — the audio analogue of
// TextureStore.Contains). Same-thread, a plain map read.
func (a *Audio) HasChunk(base string) bool {
	return a.chunks[base] != nil
}

// NewAudio opens the default audio device. A failed device (headless CI)
// degrades to a disabled-but-functional sink.
func NewAudio(mgr *assets.Manager) *Audio {
	// Enable debug log capture so render package log.Printf appears in F8 panel
	EnableDebugLogCapture()

	m, err := mix.OpenDevice(audioFrequency, audioChannels)
	if err != nil {
		log.Printf("render: audio disabled: %v", err)
		m = nil
	}
	return newAudio(mgr, m)
}

// newAudio builds the sink on mixer m (nil = disabled). Tests pass a headless
// mixer. Every codec SDL3_mixer was built with (opus, ogg, mp3, flac, wav)
// decodes without further setup.
func newAudio(mgr *assets.Manager, m *mix.Mixer) *Audio {
	a := &Audio{
		mgr:       mgr,
		chunks:    map[string]*mix.Audio{},
		pending:   map[string]pendingPlay{},
		loopMeta:  map[string]loopMeta{},
		musicVol:  fullVolumePercent,
		sfxVol:    fullVolumePercent,
		blipVol:   fullVolumePercent,
		alertVol:  fullVolumePercent,
		blipScale: fullVolumePercent,
	}
	if m == nil {
		return a
	}
	var err error
	track := func() *mix.Track {
		if err != nil {
			return nil
		}
		var t *mix.Track
		t, err = m.NewTrack()
		return t
	}
	a.blipTrack, a.alertTrack = track(), track()
	a.music.track, a.musicOld.track = track(), track()
	a.sfxTracks = make([]*mix.Track, sfxTrackCount)
	for i := range a.sfxTracks {
		a.sfxTracks[i] = track()
	}
	if err != nil {
		log.Printf("render: audio disabled: %v", err)
		m.Close() // frees the tracks made so far
		return newAudio(mgr, nil)
	}
	a.mixer = m
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
	a.modBan = a.loadSynth(builtinModWAV(ModBan))
	a.modKick = a.loadSynth(builtinModWAV(ModKick))
	a.modMute = a.loadSynth(builtinModWAV(ModMute))
}

// loadSynth decodes an in-memory WAV (nil on failure).
func (a *Audio) loadSynth(wav []byte) *mix.Audio {
	sound, err := a.mixer.Load(wav, true)
	if err != nil {
		log.Printf("render: built-in sound failed: %v", err)
		return nil
	}
	return sound
}

// PlayModAction plays the feedback sound for a mod action (#60): the user's
// custom file if set, else the synthesized built-in default. Routed on the
// reserved alert channel (its own volume, never cut by blips/SFX), same as the
// callword/friend ping. No-op when the device is disabled.
func (a *Audio) PlayModAction(action ModAction, customPath string) {
	if !a.enabled() {
		return
	}
	if customPath != "" {
		a.PlayFile(customPath)
		return
	}
	var ch *mix.Audio
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

// loadAlert synthesizes the built-in notification ping. Best-effort: a failure
// leaves a.alert nil and PlayAlert simply no-ops.
func (a *Audio) loadAlert() { a.alert = a.loadSynth(builtinAlertWAV()) }

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
		alertLiftAt     = 0.5    // fraction of a ding where the tone lifts
	)
	dingN := alertSampleRate * alertDingMs / msPerSecond
	unitN := alertSampleRate * (alertDingMs + alertGapMs) / msPerSecond
	totalN := alertSampleRate * alertTotalMs / msPerSecond
	pcm := make([]byte, 0, totalN*2)
	for i := 0; i < totalN; i++ {
		var v int16
		if pos := i % unitN; pos < dingN { // inside a ding (else the silent gap)
			t := float64(pos) / float64(alertSampleRate) // phase per ding
			prog := float64(pos) / float64(dingN)        // 0..1 through the ding
			freq := alertFreqLo
			if prog >= alertLiftAt {
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
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
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
	const pcmInitialCap = 4096
	pcm := make([]byte, 0, pcmInitialCap)
	phase := 0.0
	for _, s := range segs {
		n := sr * s.ms / msPerSecond
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
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
			phase += 2 * math.Pi * freq / sr
		}
		for i := 0; i < sr*s.gapMs/msPerSecond; i++ {
			pcm = append(pcm, 0, 0)
		}
	}
	return wavMono16(pcm, sr)
}

// builtinModWAV synthesizes the default sound for a mod action — each distinct
// so they're tell-apart by ear: ban = a low, heavy two-tone "thud"; kick = two
// quick punchy blips; mute = a muffled downward sweep ("shush").
//
//nolint:mnd // a named tone table (rule 9)
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
		channels     = 1
		bits         = 16
		pcmFormat    = 1 // WAVE_FORMAT_PCM
		fmtChunk     = 16
		headerSize   = 44
		riffPreamble = 8 // "RIFF" and the size field, not counted in the size
		bitsPerByte  = 8
	)
	byteRate := sampleRate * channels * bits / bitsPerByte
	blockAlign := channels * bits / bitsPerByte
	buf := make([]byte, 0, headerSize+len(pcm))
	put4 := func(s string) { buf = append(buf, s...) }
	put32 := func(v int) { buf = binary.LittleEndian.AppendUint32(buf, uint32(v)) }
	put16 := func(v int) { buf = binary.LittleEndian.AppendUint16(buf, uint16(v)) }
	put4("RIFF")
	put32(headerSize - riffPreamble + len(pcm)) // RIFF chunk size: everything after its size field
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
	if !a.enabled() || a.alert == nil {
		return
	}
	a.playChunk(a.alert, pendingAlert)
}

// Close stops playback and shuts the device.
func (a *Audio) Close() {
	if !a.enabled() {
		return
	}
	a.stopMusic()
	for _, c := range a.chunks {
		c.Destroy()
	}
	a.chunks = map[string]*mix.Audio{}
	a.chunkOrder = nil
	for _, c := range []*mix.Audio{a.alert, a.modBan, a.modKick, a.modMute} {
		if c != nil {
			c.Destroy()
		}
	}
	a.alert, a.modBan, a.modKick, a.modMute = nil, nil, nil, nil
	a.mixer.Close() // stops and frees every track
	a.mixer = nil
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

// reapFinishedMusic unloads the song once a NON-looping track has played to its
// natural end, so the "is music playing" signal reflects reality. WHY this exists:
// a play-once track (2.9 NO_REPEAT / looping=0, #15) stays loaded after it
// finishes — there is no natural-end observer otherwise (a stopped callback would
// fire on the audio thread and can't touch this state under rule #1). A stale
// loaded song then lies to PlayMusic's idempotency check. Only reaps a loaded,
// non-looping song that has stopped: a looping track never stops on its own, and
// music is never paused in this client, so a stopped track with a song loaded is
// unambiguously a finished one-shot — never a pause or a load gap (startMusic's
// Play begins synchronously before this runs).
func (a *Audio) reapFinishedMusic() {
	if !a.enabled() || a.music.audio == nil || a.musicLoop || a.music.track.Playing() {
		return // disabled, nothing loaded, a looper, or still rolling
	}
	a.stopMusic() // natural end: free the finished song and clear musicURL so it can be re-triggered/re-fetched
}

func (a *Audio) onAudioBytes(asset assets.AudioAsset) {
	if !a.enabled() {
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

// loadChunk decodes bytes via SDL3_mixer (wav/ogg/opus/mp3/flac) and caches
// the fully-decoded sound by base.
func (a *Audio) loadChunk(base string, data []byte) *mix.Audio {
	if chunk, ok := a.chunks[base]; ok {
		return chunk
	}
	chunk, err := a.mixer.Load(data, true)
	if err != nil {
		log.Printf("render: audio decode %s failed: %v", base, err)
		return nil
	}
	a.cacheChunk(base, chunk)
	return chunk
}

// cacheChunk stores a decoded sound, first evicting the oldest one when the
// cache is full. Evicting a sound that is still playing is safe: SDL3_mixer
// keeps it alive until its track lets go (#4's use-after-free cannot happen).
func (a *Audio) cacheChunk(key string, chunk *mix.Audio) {
	if len(a.chunkOrder) >= chunkCacheMax {
		oldest := a.chunkOrder[0]
		a.chunkOrder = a.chunkOrder[1:]
		a.chunks[oldest].Destroy()
		delete(a.chunks, oldest)
	}
	a.chunks[key] = chunk
	a.chunkOrder = append(a.chunkOrder, key)
}

// playOn plays chunk on track at the given volume, replacing whatever it played.
func playOn(track *mix.Track, chunk *mix.Audio, pct int) {
	if err := track.SetAudio(chunk); err != nil {
		return
	}
	track.SetGain(gain(pct))
	_ = track.Play(mix.PlayOptions{})
}

func (a *Audio) playChunk(chunk *mix.Audio, kind pendingKind) {
	switch kind {
	case pendingBlip:
		// Blips replace each other on their own track — playing one is a
		// pointer pass (spec §8).
		playOn(a.blipTrack, chunk, a.blipVol*a.blipScale/fullVolumePercent) // M11 per-char blip scale
	case pendingAlert:
		// Callword/friend ping on its own track: a new alert replaces the old
		// (no stacking), and neither blips nor a burst of emote SFX can steal or
		// halt it. Its OWN volume (alertVol), independent of SFX.
		playOn(a.alertTrack, chunk, a.alertVol)
	default:
		// The first idle SFX track; when all are busy the sound is dropped.
		for _, t := range a.sfxTracks {
			if !t.Playing() {
				playOn(t, chunk, a.sfxVol)
				return
			}
		}
	}
}

// PlayFile plays a sound from a local file PATH (an opt-in user alert sound:
// callword / friend), loaded + cached by path — OFF the courtroom asset
// pipeline. A missing/bad path just no-ops. Render thread only; the file loads
// once (first play), then it's a cached pointer pass like any other sound.
func (a *Audio) PlayFile(path string) {
	if path == "" || !a.enabled() {
		return
	}
	chunk, ok := a.chunks[path]
	if !ok {
		c, err := a.mixer.LoadFile(path)
		if err != nil {
			log.Printf("render: alert sound %s failed: %v", path, err)
			return
		}
		a.cacheChunk(path, c)
		chunk = c
	}
	a.playChunk(chunk, pendingAlert) // alert track: a custom callword/friend sound is never cut by blips
}

// request plays the chunk for base now if cached, else marks it pending and —
// for SFX/shout, which nothing else prefetches — fetches the bytes itself.
// Blips are prefetched by the courtroom (resolveBlip); music self-fetches in
// PlayMusic.
func (a *Audio) request(base string, kind pendingKind) {
	if base == "" || !a.enabled() {
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
	if !a.enabled() {
		return
	}
	if url == a.musicURL && a.music.audio != nil {
		return // already playing this exact track — don't restart
	}
	// FADE_OUT (#112): with one SDL3_mixer track per song we do AO2's
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
	if a.music.audio != nil && a.musicURL != "" {
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
	if !a.enabled() {
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
func (a *Audio) CurrentMusicURL() string { return a.musicURL }

// MusicPlaying reports whether the single music stream is live (a track is
// loaded and rolling). Cross-tab continuity uses it to know there's a stream to
// keep alive (ducked) across a switch rather than one to restart. Render thread
// only.
func (a *Audio) MusicPlaying() bool { return a.enabled() && a.music.audio != nil }

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
	if !a.enabled() {
		return
	}
	if url == a.musicURL && a.music.audio != nil {
		return // already this exact stream — never restart or re-seek a live track
	}
	a.purgePendingMusic(url)
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL), loop: loop, effects: effects, seekSec: seekSec}
	a.mgr.PrefetchExact(url, assets.AssetTypeMusic, network.PriorityHigh) // AssetType: Music
}

// startMusic decodes and plays a fetched track. loop picks loop-forever over
// play-once (2.9 looping/NO_REPEAT, #15); effects carries the MUSIC_EFFECT bit
// flags (FADE_IN, FADE_OUT, SYNC_POS — #112); seekSec is a best-effort start
// position (>0 = start there, 0 = play from top).
// Effect handling (../AO2-Client/src/aomusicplayer.cpp:24-160):
func (a *Audio) startMusic(url string, data []byte, loop bool, effects int, seekSec float64) {
	// Takeover snapshot (cross-tab resume, wave 13): this call is about to SWAP a
	// live, different track out for `url`. Capture the OUTGOING track's true
	// position NOW — before stopMusic() frees it — so a later resume of that
	// parked track can seek to exactly where it was plus the seconds it keeps
	// playing while `url` downloads. Only a readable (ok) clock is recorded; a
	// song of unknown length records nothing, and the resume falls back to the
	// old wall-clock estimate.
	if a.music.audio != nil && a.musicURL != "" && a.musicURL != url {
		if pos, dur, ok := a.MusicClock(); ok {
			a.recordSwapSnap(a.musicURL, pos, dur, time.Now())
		}
	}
	// True crossfade (#112): if FADE_OUT is set and a live, audible old track is
	// still rolling, keep it playing on the other music track and fade it out in
	// parallel with the new song, instead of hard-stopping it.
	crossfade := effects&musicEffectFadeOut != 0 && a.music.audio != nil && a.musicVol > 0
	if effects != 0 {
		log.Printf("render: startMusic %q effects=0x%x crossfade=%v musicVol=%d seek=%.2fs",
			url, effects, crossfade, a.musicVol, seekSec)
	}
	if crossfade {
		a.clearSlot(&a.musicOld) // a crossfade still fading from before ends now
		a.music, a.musicOld = a.musicOld, a.music
	} else {
		a.stopMusic() // clears musicURL; set below only on a successful start
	}
	song, err := a.mixer.Load(data, false) // streamed: data stays pinned in the slot
	if err != nil {
		log.Printf("render: music decode failed: %v", err)
		a.restoreOldMusic()
		return
	}
	opts := mix.PlayOptions{}
	if loop {
		opts.Loops = mix.LoopForever
	}
	if effects&musicEffectFadeIn != 0 {
		opts.FadeInMs = musicFadeInMs // linear 1000ms ramp (AO2 FADE_IN)
	}
	durSec, haveDur := song.Duration()
	// Cross-tab resume + SYNC_POS (best-effort): start where the track should
	// be. Wrap the target by the song's REAL length: the ui-side wall-clock
	// fallback is not duration-wrapped, and AO area loops are short, so its
	// target routinely lands past the end.
	if seekSec > 0 && haveDur && durSec > 0 {
		seekSec = math.Mod(seekSec, durSec)
	}
	opts.StartSec = seekSec
	// Loop points (#48): a looping song with a custom sidecar loops gaplessly
	// at the decoder level between its loop start and end.
	if loop {
		if p, ok := a.loopPointsFor(url, data, durSec, haveDur, seekSec); ok {
			opts.LoopStartSec, opts.LoopEndSec = p.StartSec, p.EndSec
		}
	}
	if err := a.music.track.SetAudio(song); err != nil {
		log.Printf("render: music play failed: %v", err)
		song.Destroy()
		a.restoreOldMusic()
		return
	}
	// Set the gain BEFORE playing so a fade-in climbs to the right ceiling.
	a.music.track.SetGain(gain(a.musicVol))
	if err := a.music.track.Play(opts); err != nil {
		log.Printf("render: music play failed: %v", err)
		song.Destroy()
		a.restoreOldMusic()
		return
	}
	a.music.audio, a.music.bytes = song, data
	a.musicLoop = loop // remember loop-ness so reapFinishedMusic only reaps a finished one-shot
	if crossfade {
		// Fade the old song out now that the new one is live; stepFadeOutOld
		// unloads it once the fade has stopped its track.
		a.musicOld.track.Stop(musicFadeOutMs)
	}
	a.musicURL = url // now this exact track is playing — PlayMusic(url) becomes a no-op
}

// clearSlot stops a music slot and unloads its song.
func (a *Audio) clearSlot(s *musicSlot) {
	if s.audio == nil {
		return
	}
	s.track.Stop(0)
	s.audio.Destroy()
	s.audio, s.bytes = nil, nil
}

func (a *Audio) stopMusic() {
	a.clearSlot(&a.music)
	a.clearSlot(&a.musicOld) // tear down an in-flight crossfade too (StopMusic mid-fade)
	a.musicURL = ""          // nothing playing now; a later PlayMusic of the same URL plays again
	// No song loaded → loop-ness is moot; reset so a stale true can't skip a future reap.
	a.musicLoop = false
}

// restoreOldMusic puts a crossfade's old song back as the live one after a
// failed load/play, so the previous track keeps playing instead of dropping out.
// No-op when there is no in-flight crossfade.
func (a *Audio) restoreOldMusic() {
	if a.musicOld.audio == nil {
		return
	}
	a.music, a.musicOld = a.musicOld, a.music
}

// stepFadeOutOld unloads a crossfade's old song once its fade-out has stopped
// its track. Called every Frame on the render thread.
func (a *Audio) stepFadeOutOld() {
	if a.musicOld.audio == nil || a.musicOld.track.Playing() {
		return // nothing fading, or the fade is still running
	}
	log.Printf("render: fade-out of old track complete")
	a.clearSlot(&a.musicOld)
}
