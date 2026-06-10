package render

import (
	"log"
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

	// chunkCacheMax bounds decoded SFX/blip chunks held in memory.
	chunkCacheMax = 64

	// pendingPlayTTL drops play requests whose asset never arrived.
	pendingPlayTTL = 10 * time.Second
)

type pendingKind int

const (
	pendingShout pendingKind = iota
	pendingSFX
	pendingBlip
	pendingMusic
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

	musicBytes []byte // keeps streamed music memory alive while playing
	musicRW    *sdl.RWops
	music      *mix.Music

	enabled bool
}

// NewAudio opens the mixer. A failed device (headless CI) degrades to a
// disabled-but-functional sink.
func NewAudio(mgr *assets.Manager) *Audio {
	a := &Audio{
		mgr:     mgr,
		chunks:  map[string]*mix.Chunk{},
		pending: map[string]pendingPlay{},
	}
	if err := mix.OpenAudio(audioFrequency, mix.DEFAULT_FORMAT, audioChannels, audioChunkSize); err != nil {
		log.Printf("render: audio disabled: %v", err)
		return a
	}
	mix.AllocateChannels(mixChannelCount)
	a.enabled = true
	return a
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
	mix.CloseAudio()
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
		_, _ = chunk.Play(blipChannel, 0)
	default:
		_, _ = chunk.Play(-1, 0)
	}
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
	}
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
