package render

import (
	"context"
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
	// Audio device parameters; 48 kHz stereo matches modern opus content.
	audioFrequency = 48000
	audioChannels  = 2

	// sfxTrackCount is how many SFX and shouts can overlap; blips and alerts have their own tracks.
	sfxTrackCount = 14

	// chunkCacheMax bounds decoded sounds; evicting a playing one is safe, as SDL3_mixer frees it when done.
	chunkCacheMax = 64

	pendingPlayTTL = 10 * time.Second

	// musicFadeInMs matches AO2's FADE_IN ramp (aomusicplayer.cpp:160); a slider drag just changes the ceiling.
	musicFadeInMs = 1000
	// musicFadeOutMs matches AO2's FADE_OUT ramp (aomusicplayer.cpp:141); the old track fades during the download.
	musicFadeOutMs = 4000

	// MUSIC_EFFECT flags (../AO2-Client/src/datatypes.h:95-101); looping and NO_REPEAT arrive as the loop bool.
	musicEffectFadeIn  = 1 << 0 // ramp the NEW track up (config.MusicFlagFadeIn)
	musicEffectFadeOut = 1 << 1 // ramp the PREVIOUS track down (config.MusicFlagFadeOut)
	musicEffectSyncPos = 1 << 2 // start new track at previous track's position (config.MusicFlagSyncPos)
)

// Compile-time check that these match config's wire values.
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
	// loop and effects are read in startMusic when the bytes arrive, never from stale args.
	loop    bool
	effects int
	// seekSec is a best-effort start position for a cross-tab resume; 0 plays from the top.
	seekSec float64
}

// Audio implements courtroom.AudioSink over SDL3_mixer, with no Go audio decoding; render thread only.
type Audio struct {
	mgr *assets.Manager

	// mixer is nil when no device opened (headless CI), making every method a no-op.
	mixer      *mix.Mixer
	blipTrack  *mix.Track
	alertTrack *mix.Track
	sfxTracks  []*mix.Track

	chunks     map[string]*mix.Audio // key: asset base
	chunkOrder []string              // FIFO eviction order
	pending    map[string]pendingPlay

	// alert is the built-in ping for when the user set no custom sound; it never enters the chunk cache.
	alert *mix.Audio

	// modBan/modKick/modMute are the built-in mod-action sounds, used when the user set no custom file.
	modBan, modKick, modMute *mix.Audio

	// music is the live song, musicOld the one fading out; audio streams from its bytes, so they stay pinned.
	music    musicSlot
	musicOld musicSlot
	musicURL string // the URL of the track currently playing ("" = none); makes PlayMusic
	//          idempotent so a room rebuild re-seeding the same track doesn't restart it.
	musicLoop bool // whether the current stream loops forever; a non-looping (2.9
	//          NO_REPEAT) stream is reaped by reapFinishedMusic when it ends, so "is music playing" stays true.

	// loopMeta caches sidecar results by full music URL, including "none exist"; never re-probed inside TTL (rule 6).
	loopMeta map[string]loopMeta

	// fetchSidecar and wake run on sidecar goroutines; loopFetches and the buffered loopResults bound them.
	fetchSidecar    sidecarFetcher
	wake            func()
	loopFetches     map[string]struct{}
	loopResults     chan loopMetaResult
	loopFetchCtx    context.Context
	cancelLoopFetch context.CancelFunc
	// loopRearmURL is a song whose loop points arrived; musicFadeInEnd delays applying them past a fade-in.
	loopRearmURL   string
	musicFadeInEnd time.Time

	// swapSnaps maps an outgoing URL to its position when swapped out, so the cross-tab resume can seek back.
	swapSnaps     map[string]musicSwapSnap
	swapSnapOrder []string // insertion order, oldest first, for cap eviction

	// Volumes are percents (0-100), applied at play time.
	musicVol, sfxVol, blipVol int
	// alertVol is independent of SFX, so a quiet or muted SFX mix can't silence name pings.
	alertVol int
	// blipScale is the speaker's per-character blip attenuation (100 = none), multiplied into blipVol.
	blipScale int
}

type musicSlot struct {
	track *mix.Track
	audio *mix.Audio // nil = no song loaded
	bytes []byte     // the encoded song the audio streams from
}

const fullVolumePercent = 100

const msPerSecond = 1000

func gain(pct int) float32 { return float32(pct) / fullVolumePercent }

func (a *Audio) enabled() bool { return a.mixer != nil }

// SetVolumes applies music, SFX and blip volumes (0-100); music takes effect on the live track at once.
func (a *Audio) SetVolumes(music, sfx, blip int) {
	a.musicVol, a.sfxVol, a.blipVol = music, sfx, blip
	if a.enabled() {
		a.music.track.SetGain(gain(music))
	}
}

// SetAlertVolume sets the callword and friend ping volume (0-100), independent of SFX.
func (a *Audio) SetAlertVolume(pct int) { a.alertVol = pct }

// SetBlipScale sets the speaker's blip attenuation (0-100); applied at the next blip.
func (a *Audio) SetBlipScale(pct int) {
	pct = value.Clamp(pct, 0, fullVolumePercent)
	a.blipScale = pct
}

// HasChunk reports whether a decoded sound for base is cached, the audio analogue of TextureStore.Contains.
func (a *Audio) HasChunk(base string) bool {
	return a.chunks[base] != nil
}

// NewAudio opens the default device; a failed device gives a disabled but working sink.
func NewAudio(mgr *assets.Manager) *Audio {
	EnableDebugLogCapture()

	m, err := mix.OpenDevice(audioFrequency, audioChannels)
	if err != nil {
		log.Printf("render: audio disabled: %v", err)
		m = nil
	}
	return newAudio(mgr, m)
}

// newAudio builds the sink on mixer m (nil = disabled).
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

		wake:        func() {},
		loopFetches: map[string]struct{}{},
		loopResults: make(chan loopMetaResult, loopFetchCap),
	}
	a.loopFetchCtx, a.cancelLoopFetch = context.WithCancel(context.Background())
	if mgr != nil {
		a.fetchSidecar = mgr.FetchRawLayered
		a.wake = mgr.NotifyDelivery
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

// ModAction identifies a mod-command feedback sound.
type ModAction int

const (
	ModBan ModAction = iota
	ModKick
	ModMute
)

// loadModSounds synthesizes the built-in mod sounds; on failure PlayModAction is a no-op for that action.
func (a *Audio) loadModSounds() {
	a.modBan = a.loadSynth(builtinModWAV(ModBan))
	a.modKick = a.loadSynth(builtinModWAV(ModKick))
	a.modMute = a.loadSynth(builtinModWAV(ModMute))
}

func (a *Audio) loadSynth(wav []byte) *mix.Audio {
	sound, err := a.mixer.Load(wav, true)
	if err != nil {
		log.Printf("render: built-in sound failed: %v", err)
		return nil
	}
	return sound
}

// PlayModAction plays the user's file or the built-in sound on the alert track, so blips and SFX can't cut it.
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

// loadAlert synthesizes the built-in ping; on failure PlayAlert is a no-op.
func (a *Audio) loadAlert() { a.alert = a.loadSynth(builtinAlertWAV()) }

// builtinAlertWAV synthesizes three two-tone dings; each resets phase and decays to zero, so they don't click.
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

// modSegment is one tone: a sine gliding from freqStart to freqEnd over ms, then gapMs of silence.
type modSegment struct {
	freqStart, freqEnd float64
	ms, gapMs          int
	amp                float64
}

// synthSegments renders a mono WAV; phase accumulates across a glide and envelopes end at zero, so joins don't click.
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

// builtinModWAV synthesizes a mod action's sound; ban, kick and mute are distinct so they can be told apart by ear.
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

// PlayAlert plays the built-in ping; a no-op when disabled or if it failed to synthesize.
func (a *Audio) PlayAlert() {
	if !a.enabled() || a.alert == nil {
		return
	}
	a.playChunk(a.alert, pendingAlert)
}

func (a *Audio) Close() {
	a.cancelLoopFetch()
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

// Frame drains delivered audio and fires matured pending plays; call once per frame on the render thread.
func (a *Audio) Frame() {
	for {
		select {
		case asset := <-a.mgr.Audio():
			a.onAudioBytes(asset)
		default:
			a.stepLoopMeta()
			a.expirePending()
			a.stepFadeOutOld()    // reap the crossfade's old track once its native fade halts it
			a.reapFinishedMusic() // clear a naturally-ended one-shot so it's not seen as "still playing"
			return
		}
	}
}

// reapFinishedMusic unloads a non-looping song that has stopped, so PlayMusic's idempotency check stays correct.
// A stopped callback would fire on the audio thread (hard rule 1).
// Music never pauses and Play starts synchronously, so a stopped track with a song loaded has finished.
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
	// Music with no pending entry outlived pendingPlayTTL or was purged; these bytes are discarded, so log it.
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

// loadChunk decodes bytes through SDL3_mixer and caches the sound by base.
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

// cacheChunk stores a sound, evicting the oldest; SDL3_mixer keeps a playing one alive until its track lets go.
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
		playOn(a.blipTrack, chunk, a.blipVol*a.blipScale/fullVolumePercent) // M11 per-char blip scale
	case pendingAlert:
		// A new alert replaces the old; blips and SFX can't halt it.
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

// PlayFile plays a user alert sound from a local path, outside the asset pipeline; render thread only.
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

// request plays base's chunk now if cached, else marks it pending and fetches SFX and shouts itself.
func (a *Audio) request(base string, kind pendingKind) {
	if base == "" || !a.enabled() {
		return
	}
	if chunk, ok := a.chunks[base]; ok {
		a.playChunk(chunk, kind)
		return
	}
	a.pending[base] = pendingPlay{kind: kind, deadline: time.Now().Add(pendingPlayTTL)}
	// Nothing else prefetches SFX and shouts; without this the pending entry just expires and the sound never plays.
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
		// Music can take bodyTransferBudget (60s), longer than this expiry; its single entry is managed elsewhere.
		if p.kind == pendingMusic {
			continue
		}
		if now.After(p.deadline) {
			delete(a.pending, base)
		}
	}
}

// PlayShout plays a character's shout cry. // AssetType: SFX
func (a *Audio) PlayShout(base string) { a.request(base, pendingShout) }

// PlaySFX plays an emote sound; the courtroom's Update already applied SFX_DELAY. // AssetType: SFX
func (a *Audio) PlaySFX(base string, _ time.Duration) { a.request(base, pendingSFX) }

// PlayBlip fires one chat blip. // AssetType: Blip
func (a *Audio) PlayBlip(base string) { a.request(base, pendingBlip) }

// PlayMusic streams a track; a no-op if already playing, so a room rebuild doesn't restart it. // AssetType: Music
func (a *Audio) PlayMusic(url string, loop bool, effects int) {
	if !a.enabled() {
		return
	}
	if url == a.musicURL && a.music.audio != nil {
		return // already playing this exact track — don't restart
	}
	// FADE_OUT: the old track plays on during the download, then startMusic fades it.
	if effects != 0 {
		log.Printf("render: PlayMusic %q loop=%v effects=0x%x (fadeIn=%v fadeOut=%v syncPos=%v)",
			url, loop, effects,
			effects&musicEffectFadeIn != 0,
			effects&musicEffectFadeOut != 0,
			effects&musicEffectSyncPos != 0)
	}
	var syncPosSec float64
	if a.music.audio != nil && a.musicURL != "" {
		// SYNC_POS: read the old position before the new track overwrites it (aomusicplayer.cpp:132-136).
		if effects&musicEffectSyncPos != 0 {
			if pos, _, ok := a.MusicClock(); ok {
				syncPosSec = pos
			}
		}
	}
	// At most one music entry may be pending, or a late fetch of a previous track would revert playback to it.
	a.purgePendingMusic(url)
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL), loop: loop, effects: effects, seekSec: syncPosSec}
	a.mgr.PrefetchExact(url, assets.AssetTypeMusic, network.PriorityHigh) // AssetType: Music
}

// purgePendingMusic drops pending music except keep ("" = all), so a late delivery is ignored; render thread only.
func (a *Audio) purgePendingMusic(keep string) {
	for u, p := range a.pending {
		if p.kind == pendingMusic && u != keep {
			delete(a.pending, u)
		}
	}
}

// StopMusic halts music and cancels any fetch; AO has no stop packet, so this is local. Render thread only.
func (a *Audio) StopMusic() {
	if !a.enabled() {
		return
	}
	a.stopMusic()
	a.purgePendingMusic("") // a pending fetch would otherwise start on arrival
}

// CurrentMusicURL is the live track's URL ("" = none); a tab switch uses it to decide if the stream survives.
func (a *Audio) CurrentMusicURL() string { return a.musicURL }

// MusicPlaying reports whether a track is loaded and rolling; render thread only.
func (a *Audio) MusicPlaying() bool { return a.enabled() && a.music.audio != nil }

// musicSwapSnap is an outgoing track's position, duration and read time; the resume adds elapsed and wraps.
type musicSwapSnap struct {
	posSec float64
	durSec float64
	at     time.Time
}

// musicSwapSnapCap is above the tab ceiling, so every parked tab keeps a snapshot; the oldest is evicted on overflow.
const musicSwapSnapCap = 16

// recordSwapSnap stores an outgoing URL's snapshot; callers skip unknown lengths, which use the wall-clock estimate.
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

// SwappedOutSnap returns the snapshot for url; ok=false means the resume falls back to the wall-clock estimate.
func (a *Audio) SwappedOutSnap(url string) (posSec, durSec float64, at time.Time, ok bool) {
	s, ok := a.swapSnaps[url]
	if !ok {
		return 0, 0, time.Time{}, false
	}
	return s.posSec, s.durSec, s.at, true
}

// PlayMusicAt is PlayMusic with a best-effort seek, for resuming a tab's song. // AssetType: Music
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

// startMusic decodes and plays a fetched track, following AO2's effects (aomusicplayer.cpp:24-160).
func (a *Audio) startMusic(url string, data []byte, loop bool, effects int, seekSec float64) {
	// Read the outgoing track's position before stopMusic frees it; an unreadable clock records nothing.
	if a.music.audio != nil && a.musicURL != "" && a.musicURL != url {
		if pos, dur, ok := a.MusicClock(); ok {
			a.recordSwapSnap(a.musicURL, pos, dur, time.Now())
		}
	}
	// FADE_OUT with an audible old track: keep it on the other music track and fade it in parallel with the new song.
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
	// Wrap by the real length: the ui's wall-clock fallback isn't wrapped and short loops land past the end.
	if seekSec > 0 && haveDur && durSec > 0 {
		seekSec = math.Mod(seekSec, durSec)
	}
	opts.StartSec = seekSec
	// A sidecar's loop points make the decoder loop gaplessly.
	if loop {
		if p, ok := a.loopPointsFor(url, durSec, haveDur, seekSec); ok {
			opts.LoopStartSec, opts.LoopEndSec = p.StartSec, p.EndSec
		}
	}
	if err := a.music.track.SetAudio(song); err != nil {
		log.Printf("render: music play failed: %v", err)
		song.Destroy()
		a.restoreOldMusic()
		return
	}
	// Set the gain first so a fade-in climbs to the right ceiling.
	a.music.track.SetGain(gain(a.musicVol))
	if err := a.music.track.Play(opts); err != nil {
		log.Printf("render: music play failed: %v", err)
		song.Destroy()
		a.restoreOldMusic()
		return
	}
	a.music.audio, a.music.bytes = song, data
	a.musicFadeInEnd = time.Now().Add(time.Duration(opts.FadeInMs) * time.Millisecond)
	if loop {
		a.requestLoopMeta(url, data)
	}
	a.musicLoop = loop // remember loop-ness so reapFinishedMusic only reaps a finished one-shot
	if crossfade {
		// stepFadeOutOld unloads the old song once the fade has stopped its track.
		a.musicOld.track.Stop(musicFadeOutMs)
	}
	a.musicURL = url // now this exact track is playing — PlayMusic(url) becomes a no-op
}

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
	// No song loaded; reset so a stale true can't skip a future reap.
	a.musicLoop = false
}

// restoreOldMusic makes a crossfade's old song live again after a failed load, so it doesn't drop out.
func (a *Audio) restoreOldMusic() {
	if a.musicOld.audio == nil {
		return
	}
	a.music, a.musicOld = a.musicOld, a.music
}

// stepFadeOutOld unloads a crossfade's old song once its track stops; called every Frame.
func (a *Audio) stepFadeOutOld() {
	if a.musicOld.audio == nil || a.musicOld.track.Playing() {
		return // nothing fading, or the fade is still running
	}
	log.Printf("render: fade-out of old track complete")
	a.clearSlot(&a.musicOld)
}
