package assets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// decodedChanCap bounds the decoded-asset handoff to the render thread.
	// When full, decoder workers block briefly (the render thread drains
	// every frame); results are never dropped (spec §17.7).
	decodedChanCap = 64
	// audioChanCap bounds the raw-audio handoff to the audio system.
	audioChanCap = 64
	// warningChanCap bounds missing-asset warnings to the UI; warnings are
	// droppable advisories, results are not.
	warningChanCap = 32
	// musicFailChanCap bounds the music-fetch-failure advisory lane to the UI.
	// A transient music failure (timeout / 5xx / host backoff) is otherwise
	// completely invisible — counted in the pump, never surfaced (§1.1) — so
	// the jukebox warn line needs a signal. Droppable like warnings (newest
	// wins on flood); scoped strictly to AssetTypeMusic so sprite backoff
	// bursts never reach it. Small because at most one track plays at a time.
	musicFailChanCap = 8
	// localOriginHistoryCap bounds how many RETIRED mount-set origins stay
	// answerable after the overlay moves on (rule §17.4 — the ring is capped, not
	// a leak). Each entry is one LocalFetcher: a string slice and a hashed origin,
	// nothing paged in, so the ceiling is bytes.
	//
	// Four is sized off what can actually still be holding a stale base. A base
	// only survives a mount change inside a live scene or a PARKED TAB's URL
	// builder (ui/tabs.go activateTab does not re-mint), so the reachable
	// generations are "the ones the user made while those tabs sat in the
	// background" — folder attaches happen one at a time in a settings dialog, not
	// in bursts. Four covers a full editing session with room to spare and still
	// evicts, so a user who reconfigures mounts all afternoon does not accumulate
	// fetchers for folders they detached hours ago.
	localOriginHistoryCap = 4
	// conclusiveMissCap bounds the session's exhausted-chain set (rule §17.4 —
	// see missSet for what it is and why the 404 cache could not be it).
	//
	// Sized off the case that produced it: a large character roster where most
	// entries ship neither a char_icon nor emotions/button<N>_off art. One absent
	// icon per character on a two-thousand-name roster, plus the ~60 button cells
	// (on and off) of every character whose emote grid the user opens, plus the
	// stray background and sound, all fit — so a whole session on such a server
	// settles at zero repeat probes instead of churning the set. At roughly 150
	// bytes an entry that is ~1.2 MiB against the 256 MiB budget.
	//
	// Overflow is a safety valve, not a design point: it drops the oldest eighth,
	// and each dropped key costs at most one more pipeline pass before it is
	// recorded again.
	conclusiveMissCap = 8192
)

// DecodedAsset is the manager's handoff to the render thread: decoded frames
// ready for texture upload, or the error that ended the attempt.
type DecodedAsset struct {
	URL   string
	Base  string
	Type  AssetType
	Asset *Decoded
	Err   error
	// Transient marks a NETWORK-stage failure (5xx, timeout, host backoff):
	// the bytes were never seen, so nothing is known about the asset itself
	// and it must stay re-demandable. Only non-transient errors (the bytes
	// arrived and failed to DECODE) may enter the texture store's negative
	// cache — conflating the two pinned every asset touched during a flaky
	// origin's backoff window as "failed" for decodeFailTTL (the
	// "whole server's files go missing in waves" report).
	Transient bool
	// FromPack marks bytes that came from the user's LOCAL MOUNT FOLDERS rather
	// than the server. Base is still the SERVER's URL either way — that is the
	// whole point of layering, the asset keeps one identity — so the render side
	// MUST consult this before negative-caching a decode failure. Calling
	// MarkFailed(Base) for a corrupt PACK file would take the SERVER's perfectly
	// good copy of that asset offline for decodeFailTTL, because one file in a
	// hand-assembled pack was damaged.
	FromPack bool
}

// AudioAsset is the manager's handoff to the audio system: raw bytes for
// SDL_mixer (decode happens in C at native speed — spec §8).
type AudioAsset struct {
	URL  string
	Base string
	Type AssetType
	Data []byte
	// FromPack marks local-mount bytes; see DecodedAsset.FromPack. Audio never
	// reaches the upload pump, so the audio loader owns its own quarantine call.
	FromPack bool
}

// Warning reports an asset that 404'd in every probed format, for the
// visible in-client warning (spec §4).
type Warning struct {
	Base  string
	Type  AssetType
	Tried []string
}

// MusicFailure reports a TRANSIENT music-fetch failure (timeout / 5xx / host
// backoff — never a 404, which is a definitive miss surfaced elsewhere) so the
// jukebox can tell the user a track silently didn't load (§1.1). Scoped to
// AssetTypeMusic only: sprite/icon transient bursts must not surface here.
type MusicFailure struct {
	URL string
	Err error
}

// Fetcher is the byte source the manager probes: network.Client for asset
// streaming, LocalFetcher for the no-streaming legacy mode.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Manager walks the tiers per spec §8: T1 texture hit → done; T2 raw
// hit → decode; T3 disk hit → promote + decode + learn; else network probe
// by candidate order, then T2 + async T3 + learn + decode; all-404 → warning.
type Manager struct {
	resolver *Resolver
	prefs    *config.AssetPreferences
	t2       *cache.ByteBudgetLRU[string, []byte]
	disk     *cache.DiskCache
	client   Fetcher
	pool     *network.Pool
	decoder  *DecoderPool
	thumbs   *ThumbCache // optional persistent low-q sprite thumbnails (nil = feature absent)

	// localMode skips the T3 disk cache (assets already live on disk).
	localMode bool

	// t1Contains asks the render side whether a texture is already uploaded;
	// nil means "no T1 yet" (headless tests). The TextureStore keys probed
	// assets by BASE (extension-free) and exact assets by their full URL —
	// callers must pass whichever key the upload used.
	t1Contains func(url string) bool
	// t1Failed asks the render side whether base recently failed to DECODE (a
	// corrupt/truncated payload); the prefetch gate backs off so one bad asset
	// can't storm the network + log every retry. nil in headless tests.
	t1Failed func(base string) bool

	decodedCh   chan DecodedAsset
	audioCh     chan AudioAsset
	warningCh   chan Warning
	musicFailCh chan MusicFailure

	// deliveryNotify, when set, fires after each decodedCh/audioCh send — the
	// experimental event-driven render loop's wake hook, so a finished decode
	// uploads (and an audio payload plays) on the next pass instead of waiting
	// out an idle tick. Called from pool workers: must be cheap, non-blocking,
	// and SDL-free here (the UI injects an SDL wake-event push).
	deliveryNotify atomic.Pointer[func()]

	inflight sync.Map // base|type → struct{}: one pipeline pass per asset

	// conclusiveMiss remembers the chains already probed to exhaustion this
	// session. Consulted in prefetchChain/PrefetchExact BEFORE the pool submit,
	// so re-demanding an asset the server does not have costs one map lookup
	// instead of a job, a resolver walk, two failed disk reads and a 404 on the
	// wire. Emptied only by ForgetConclusiveMisses and the source-change setters
	// that call it — see missSet for why it has no TTL.
	conclusiveMiss *missSet

	// offline gates every network egress (rehearsal mode: a server's
	// already-cached assets browse, nothing probes). Cache-tier reads
	// keep working; misses report as not-found without touching the
	// client, so no 404s get learned from being offline.
	offline atomic.Bool

	// archiveSrc, when set, is consulted BEFORE the normal client in netFetch —
	// a bundled-scene replay points it at the archive folder so the same shared
	// Manager (whose Decoded channel the render Pump drains) serves the archive's
	// local:// URLs. A miss falls through to the normal source, so non-archive
	// fetches (theme/UI chrome) during a replay are unaffected. atomic: the UI
	// thread sets/clears it while pool workers read it.
	archiveSrc atomic.Pointer[Fetcher]

	// localOverlay, when set, serves local:// URLs in a STREAMING manager (whose
	// fixed client is the network client and would transport-error on a local://
	// URL). It lets the content report / a mid-session source switch resolve a
	// recording against the user's configured mounts WITHOUT rebuilding the
	// Manager, which is mode-locked at construction (see LocalMode). Nil = no
	// mounts configured. Consulted in BOTH modes, by ORIGIN (see localSourceFor).
	// atomic: the render thread swaps it on every mounts/pref change while pool
	// workers read it.
	localOverlay atomic.Pointer[LocalFetcher]

	// localRetired holds the mount sets the overlay has been swapped OFF, newest
	// first, so a URL minted under a PREVIOUS generation still resolves — see
	// localSourceFor and localOriginHistoryCap. Copy-on-write: SetLocalOverlay
	// publishes a fresh slice and readers only ever range over an immutable one,
	// so pool workers never lock (rule §17.5). Nil until the first swap.
	localRetired atomic.Pointer[[]*LocalFetcher]

	// mountLayer, when set, answers asset fetches from the user's local mount
	// folders and .zip packs BEFORE the network, under the SERVER's own URL
	// (v1.89.0 layering). Nil — the default, and the state for every user who has
	// never configured a mount — costs exactly one atomic load on the fetch path;
	// see activeMountLayer for why the nil check is ordered first.
	mountLayer atomic.Pointer[MountLayer]

	t1Hits          atomic.Int64
	t2Hits          atomic.Int64
	diskHits        atomic.Int64
	netFetches      atomic.Int64
	missing         atomic.Int64
	mountFetches    atomic.Int64
	packQuarantined atomic.Int64
}

// SetOffline flips rehearsal mode's network gate.
func (m *Manager) SetOffline(on bool) { m.offline.Store(on) }

// Offline reports the network gate's current state, so a caller that closes it
// TEMPORARILY can put back what it found rather than assuming "open".
//
// It exists for the theme editor's offline preview (ui/themeeditorpreview.go),
// which brackets the gate for the length of a canned demo. Without a reader, a
// preview taken from inside a rehearsal session would re-open the network on the
// way out and quietly end rehearsal mode — the bug that a save/restore pair with
// no getter always writes.
func (m *Manager) Offline() bool { return m.offline.Load() }

// ErrLocalOverlayUnavailable reports that a local:// URL was requested of a
// STREAMING manager that has no mount overlay configured. It is DELIBERATELY
// distinct from network.ErrAssetNotFound: a nil overlay means "this client
// cannot serve local:// at all," not "the file is absent." Reporting it as a
// 404 would let the diagnostic walk accumulate it into tried404 and mark every
// asset [missing] (the false-missing bug the overlay fixes); reporting it as a
// transport error keeps the report honest ("unreachable" / cannot query),
// exactly like an https:// origin the streaming client can't reach. A genuinely
// missing mount FILE still returns ErrAssetNotFound from LocalFetcher.Fetch, so
// real absence stays conclusive and negative-cacheable.
var ErrLocalOverlayUnavailable = errors.New("assets: no local mount overlay configured")

// netFetch is the manager's ONLY network egress: the offline gate lives
// here so every pipeline path (probe chains, raw text, sync fetch)
// respects rehearsal mode structurally.
func (m *Manager) netFetch(ctx context.Context, url string) ([]byte, error) {
	// Bundled-archive replay: serve the archive folder first; a miss falls
	// through so concurrent non-archive fetches (theme/UI) still hit the network.
	// This sits ABOVE the offline gate on purpose: archive/local reads are disk,
	// not network egress, so rehearsal (offline) must not block them.
	if ov := m.archiveSrc.Load(); ov != nil {
		if data, err := (*ov).Fetch(ctx, url); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	// A local:// URL names the mount set it was minted under (LocalFetcher's
	// origin embeds a hash of the mount list), so route it to whichever local
	// source OWNS that origin — see localSourceFor. Rides ABOVE the offline gate:
	// local mount reads are disk, not network egress, so they are legal in
	// rehearsal mode. Placed AFTER archiveSrc so a replay archive's own local://
	// origin still wins.
	if strings.HasPrefix(url, LocalScheme) {
		if src := m.localSourceFor(url); src != nil {
			// A missing mount file returns ErrAssetNotFound here (conclusive miss),
			// exactly matching a streaming 404 — learned formats and warnings behave
			// identically. No disk-tier round-trip: the mounts ARE disk (skipDisk).
			return src.Fetch(ctx, url)
		}
		if m.localMode {
			// No source claims this origin, but m.client IS a LocalFetcher: let it
			// answer so the error names the origin it does serve (today's message).
			return m.client.Fetch(ctx, url)
		}
		return nil, ErrLocalOverlayUnavailable // cannot serve — NOT a 404 (see above)
	}
	if m.offline.Load() {
		return nil, network.ErrAssetNotFound
	}
	return m.client.Fetch(ctx, url)
}

// localSourceFor picks the mount fetcher a local:// URL was MINTED under, or nil
// when no configured source claims that origin.
//
// The origin is not decoration: LocalFetcher hashes the whole mount list into it
// (local.go NewLocalFetcher) so two mount configurations occupy disjoint cache
// keyspace, exactly like two asset hosts. That makes the origin a GENERATION
// stamp — attaching or detaching a folder mints a new one — and a Fetcher only
// ever answers URLs under its own.
//
// Before this, a mid-session mount change stranded the client: the UI re-minted
// its URL builder onto the NEW origin (ui/app.go rebuildAssetOrigin) while a
// local-mode Manager's source stayed the LocalFetcher built at startup, so every
// freshly-built URL came back "is not under local origin" until the user
// restarted. The overlay pointer was already being re-pushed on every mounts
// change — it was simply never read in local mode. Consulting it by ORIGIN fixes
// both directions at once and keeps the separation structural:
//
//   - the CURRENT mount set (the overlay, re-pushed on every change) serves the
//     URLs minted after the change, so content added mid-session resolves;
//   - the mount set a still-held URL was minted under (a local-mode Manager's
//     own source) keeps serving it, so a scene already on stage does not blink
//     out — and its bytes stay cached under the origin they actually came from.
//
// Those two alone leave a hole one generation wide, and the field report walks
// straight into it: a URL builder is PER-TAB session state and the UI does not
// re-mint it on a tab switch (ui/tabs.go activateTab), so after a SECOND mount
// change a parked tab still holds generation-2 URLs while the overlay is on 3
// and the fixed source is on 1 — neither claims them, and that tab is stranded
// exactly the way the whole session used to be. localRetired closes it: the
// overlay's predecessors stay answerable for a bounded number of generations, so
// any base minted recently enough to still be on screen resolves.
//
// This does NOT weaken the origin scheme, which is the point of the whole
// mechanism: every fetcher still answers ONLY URLs under its own origin, bytes
// are still cached under the origin they came from, and two mount
// configurations still occupy disjoint keyspace. All that changes is how many
// generations remain reachable at once.
//
// Overlay first: it is the newest mount set, and when nothing changed every
// origin here is equal so the order cannot matter. Cheap (two atomic loads and a
// handful of prefix compares against a cap-bounded slice) and only ever reached
// for a local:// URL.
func (m *Manager) localSourceFor(url string) Fetcher {
	if ov := m.localOverlay.Load(); ov != nil && strings.HasPrefix(url, ov.BaseURL()) {
		return ov
	}
	if retired := m.localRetired.Load(); retired != nil {
		for _, lf := range *retired { // newest first, ≤ localOriginHistoryCap entries
			if strings.HasPrefix(url, lf.BaseURL()) {
				return lf
			}
		}
	}
	// A local-mode Manager's fixed source is a LocalFetcher; a streaming one's is
	// the network client, which claims no local:// origin at all.
	if lf, ok := m.client.(*LocalFetcher); ok && strings.HasPrefix(url, lf.BaseURL()) {
		return lf
	}
	return nil
}

// SetLocalOverlay installs (or clears, with nil) the mount overlay a STREAMING
// manager consults for local:// URLs. Safe to call from the render thread
// mid-session: readers load the atomic pointer, never lock (spec §17.5). Pass
// the LocalFetcher built from the CURRENT mounts (its BaseURL must equal the
// origin the UI builds URLs against, so fetch routing matches byte-for-byte); an
// empty mount set should be passed as nil, not an empty LocalFetcher (an empty
// LocalFetcher answers every URL with a conclusive 404, which is wrong for
// "cannot serve").
//
// It is meaningful in LOCAL mode too, and that is not a widening of the feature:
// the overlay is simply the CURRENT mount set, and localSourceFor picks whichever
// source owns the URL's origin. A local-mode Manager whose mounts never change
// installs an overlay with the SAME origin as its own source, so routing is
// unchanged; one whose mounts DID change can finally serve the URLs the UI
// re-minted (see localSourceFor).
//
// The overlay it REPLACES is retired rather than dropped: URLs minted under it
// may still be on screen (or parked on a background tab, whose URL builder the
// UI does not re-mint), and a fetcher is the only thing that can answer its own
// origin. Bounded at localOriginHistoryCap generations, newest first.
//
// Render thread only for the write (it is the mounts/pref change path); the
// published slice is never mutated afterwards, so readers on pool workers need
// no lock.
func (m *Manager) SetLocalOverlay(f *LocalFetcher) {
	prev := m.localOverlay.Swap(f)
	if sameMountSet(prev, f) {
		return // the same mount set re-pushed: nothing retired, nothing changed
	}
	// Changing a byte source changes the answer to "does this asset exist", so
	// the session's exhausted-chain memory goes with it. Inside the setter rather
	// than at the call sites because forgetting it is SILENT: the prefetch gate
	// consults the set BEFORE any source is reached, so a stale entry would keep
	// a just-attached folder's files unreachable with nothing on screen to say
	// why. Guarded on a real change because rebuildAssetOrigin re-pushes on every
	// connect and settings edit, and one Manager serves every tab.
	m.ForgetConclusiveMisses()
	if prev == nil {
		return // nothing to retire
	}
	old := m.localRetired.Load()
	next := make([]*LocalFetcher, 0, localOriginHistoryCap)
	next = append(next, prev) // newest first
	if old != nil {
		for _, lf := range *old {
			if len(next) == localOriginHistoryCap {
				break // oldest generations fall off the end
			}
			if lf.BaseURL() == prev.BaseURL() {
				continue // it is back at the head; never list a generation twice
			}
			next = append(next, lf)
		}
	}
	m.localRetired.Store(&next)
}

// sameMountSet reports whether two overlays name the same mount configuration.
// Identity is the ORIGIN, not the pointer: rebuildAssetOrigin mints a fresh
// LocalFetcher on every call, and a LocalFetcher's BaseURL hashes the whole
// mount list (NewLocalFetcher), so equal origins mean equal mounts.
func sameMountSet(a, b *LocalFetcher) bool {
	if a == nil || b == nil {
		return a == b // nil→nil is the no-mounts steady state; nil↔set is a real change
	}
	return a.BaseURL() == b.BaseURL()
}

// skipDisk reports whether url's bytes must bypass the T3 disk tier: a local://
// URL (in any manager) reads from mount folders that ARE disk, so a disk-tier
// Get/Put would pointlessly duplicate the mounts — the same reason a local-mode
// Manager skips T3 wholesale. In a streaming manager only the local:// overlay
// URLs skip; the network origin's URLs still use T3 normally.
func (m *Manager) skipDisk(url string) bool {
	return m.localMode || strings.HasPrefix(url, LocalScheme)
}

// SetArchiveSource routes fetches through f (an archive folder's LocalFetcher)
// before the normal source, for the duration of a bundled-scene replay.
// ClearArchiveSource restores normal fetching.
func (m *Manager) SetArchiveSource(f Fetcher) { m.archiveSrc.Store(&f) }

// ClearArchiveSource removes the bundled-archive source override.
func (m *Manager) ClearArchiveSource() { m.archiveSrc.Store(nil) }

// LocalMode reports whether this Manager was built to read assets from local
// mounts (a single LocalFetcher source) rather than stream from the network.
// It is FIXED at construction (NewManager reads deps.LocalMode) — toggling the
// local-assets pref mid-session only rebuilds the URL builder, never the
// Manager's source — so callers that must know which URL scheme the single
// source can actually serve (e.g. the content picker deciding whether an
// https:// vs local:// origin is reachable at all) MUST key off this, NOT the
// live pref: the pref can invert while the source stays put.
func (m *Manager) LocalMode() bool { return m.localMode }

// SetAssetOrigin forwards a power-user Origin / CORS header override to the network
// source. No-op in local / no-streaming mode (the source isn't a network client).
func (m *Manager) SetAssetOrigin(origin string) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAssetOrigin(origin)
	}
}

// SetAdaptiveLatencyMultiple forwards the power-user per-host deadline multiple
// to the network source (0 = the built-in default). No-op in local mode.
func (m *Manager) SetAdaptiveLatencyMultiple(n int) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAdaptiveLatencyMultiple(n)
	}
}

// SetSpriteCap forwards the decode-downscale height cap to the decoder pool
// (0 = no cap). Live-safe; applies to NEW decodes.
func (m *Manager) SetSpriteCap(px int) { m.decoder.SetSpriteCap(px) }

// ColdLoadStats reports the fetch (all-hosts TTFB) and decode (+fit) EWMAs for
// the debug overlay's cold-load profiling line; the upload stage comes from the
// render-side TextureStore. Zeroes until samples exist / in local mode.
func (m *Manager) ColdLoadStats() (fetch, decode time.Duration) {
	if c, ok := m.client.(*network.Client); ok {
		fetch = c.AvgTTFB()
	}
	return fetch, m.decoder.Stats().AvgDecode
}

// ManagerDeps wires a Manager; every field is required except T1Contains.
type ManagerDeps struct {
	Resolver *Resolver
	Prefs    *config.AssetPreferences
	T2       *cache.ByteBudgetLRU[string, []byte]
	Disk     *cache.DiskCache
	// Source is the byte origin: network.Client when streaming from the
	// server's asset URL, LocalFetcher in no-streaming mode.
	Source Fetcher
	// LocalMode skips the T3 disk cache (assets already live on disk).
	LocalMode  bool
	Pool       *network.Pool
	Decoder    *DecoderPool
	T1Contains func(url string) bool
	T1Failed   func(base string) bool
	// Thumbs, when set, receives every successfully-decoded character sprite for
	// low-q thumbnailing (the opt-in cold-load stand-in cache). nil = absent.
	Thumbs *ThumbCache
}

// NewManager builds the pipeline orchestrator.
func NewManager(deps ManagerDeps) *Manager {
	return &Manager{
		resolver:       deps.Resolver,
		prefs:          deps.Prefs,
		t2:             deps.T2,
		disk:           deps.Disk,
		client:         deps.Source,
		localMode:      deps.LocalMode,
		pool:           deps.Pool,
		decoder:        deps.Decoder,
		thumbs:         deps.Thumbs,
		t1Contains:     deps.T1Contains,
		t1Failed:       deps.T1Failed,
		decodedCh:      make(chan DecodedAsset, decodedChanCap),
		audioCh:        make(chan AudioAsset, audioChanCap),
		warningCh:      make(chan Warning, warningChanCap),
		musicFailCh:    make(chan MusicFailure, musicFailChanCap),
		conclusiveMiss: newMissSet(conclusiveMissCap),
	}
}

// SetDeliveryNotify installs (or clears, with nil) the callback fired after
// each Decoded/Audio channel delivery. Safe at any time from any goroutine.
func (m *Manager) SetDeliveryNotify(f func()) {
	if f == nil {
		m.deliveryNotify.Store(nil)
		return
	}
	m.deliveryNotify.Store(&f)
}

// notifyDelivery invokes the delivery wake callback if one is installed.
func (m *Manager) notifyDelivery() {
	if f := m.deliveryNotify.Load(); f != nil {
		(*f)()
	}
}

// Decoded returns the channel the render thread drains for texture uploads.
func (m *Manager) Decoded() <-chan DecodedAsset { return m.decodedCh }

// Audio returns the channel the audio system drains for SDL_mixer loads.
func (m *Manager) Audio() <-chan AudioAsset { return m.audioCh }

// Warnings returns the channel the UI drains for missing-asset banners.
func (m *Manager) Warnings() <-chan Warning { return m.warningCh }

// MusicFailures returns the channel the UI drains for transient music-fetch
// failures — the jukebox warn line's source (§1.1). Advisory: drained on the
// render thread, never blocks a pool worker.
func (m *Manager) MusicFailures() <-chan MusicFailure { return m.musicFailCh }

// reportMusicFailure surfaces a transient music-fetch failure to the jukebox,
// scoped strictly to AssetTypeMusic (callers must check). Non-blocking like
// reportMissing: the lane is a droppable advisory (rule 4), never a result
// (rule 7 — the fetch's own delivery/error path is unchanged; this is an
// EXTRA signal). Fires the delivery wake so an idle event-loop redraws the
// warn line promptly.
func (m *Manager) reportMusicFailure(url string, err error) {
	select {
	case m.musicFailCh <- MusicFailure{URL: url, Err: err}:
		m.notifyDelivery()
	default:
	}
}

// Prefetch queues a full pipeline pass for base (URL without extension) at
// the given priority, tagged with the pool's current epoch so room changes
// cancel stale speculation.
func (m *Manager) Prefetch(base string, t AssetType, prio network.Priority) {
	m.PrefetchWithFallback(base, "", t, prio)
}

// PrefetchWithFallback is Prefetch with a second URL base probed only when
// every format of the first one 404s. AO sprite naming requires it: packs
// ship "(a)<emote>"/"(b)<emote>" files OR bare "<emote>" files, and
// AO2-Client probes the prefixed path then the unprefixed one
// (CharLayer::load_image's pathlist). The asset keeps the PRIMARY base as
// its identity everywhere (T1 key, scene layers); alt is just a second
// spelling of the same asset. The client's 404 cache keeps the extra probe
// from repeating inside its TTL, and once the texture is resident the T1
// short-circuit costs zero probes.
func (m *Manager) PrefetchWithFallback(base, alt string, t AssetType, prio network.Priority) {
	if alt == "" {
		m.PrefetchChain(base, nil, t, prio)
		return
	}
	m.PrefetchChain(base, []string{alt}, t, prio)
}

// PrefetchChain is the N-spelling generalization of PrefetchWithFallback:
// alts are further spellings of the SAME asset, probed in order only while
// every format of each earlier base 404s. Born for chatbox skins, which need
// two stems × two casings (AO2's chat→chatbox order on a case-insensitive
// filesystem, spoken over case-sensitive HTTP mirrors). base stays the
// identity whichever link answers; the chain is bounded by its call sites
// (rule §17.4) and every miss is 404-cached, so a settled chain costs zero
// probes inside the TTL.
func (m *Manager) PrefetchChain(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, false)
}

// PrefetchChainSpeculative is PrefetchChain for a PREDICTED (not yet demanded)
// asset: identical probing, but a total miss does NOT surface the §4
// missing-asset warning. The Markov prefetcher warms guesses that may 404 in
// every format on bare-named packs — reporting those would spam the debug log
// and the on-screen banner with warnings for assets no one asked for, and feed
// NotifyAssetMissing for a base that isn't on screen. The 404 cache and
// singleflight are untouched, so rule §17.6 (no re-probe inside the TTL) still
// holds. Callers pass PriorityLow so the speculation sheds under backpressure.
func (m *Manager) PrefetchChainSpeculative(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, true)
}

func (m *Manager) prefetchChain(base string, alts []string, t AssetType, prio network.Priority, suppressMissing bool) {
	if base == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(base) {
		return // recently failed to decode — back off (the negative cache absorbs retries)
	}
	if m.conclusiveMiss.has(missChainFor(base, alts, t), m.probeListGen()) {
		m.skipExhausted(base, t, suppressMissing)
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveChain(base, alts, t, suppressMissing)
		},
	})
}

// missChainFor builds the conclusive-miss identity for a chain. Allocation-free
// for the alt-less case, which is the demand cadence's entire traffic.
func missChainFor(base string, alts []string, t AssetType) missChain {
	k := missChain{base: base, typ: t}
	switch len(alts) {
	case 0:
	case 1:
		k.chain = alts[0] // the overwhelmingly common chain (prefixed → bare sprite): no join, no alloc
	default:
		k.chain = strings.Join(alts, missChainSeparator)
	}
	return k
}

// IsConclusiveMiss reports whether an ALT-LESS prefetch of base was already
// probed to exhaustion this session — i.e. whether Prefetch would do anything.
//
// It exists for the drawing side. A cell that will never be filled should not
// hold the frame pump awake waiting for it (ui/app.go demandAsset), and asking
// here is how the UI learns that without duplicating the Manager's knowledge of
// what was tried. Alt-less because every one of those call sites goes through
// Prefetch; a chain's answer is not this question's to give.
//
// Nil-receiver safe, like every method on this memory and like missSet itself:
// without a Manager the answer is "nothing is known missing", which is what a
// headless UI test wants and is the truth in any case. One contract for the
// whole quartet, so no call site has to remember which of them needs a guard.
func (m *Manager) IsConclusiveMiss(base string, t AssetType) bool {
	if m == nil {
		return false
	}
	return m.conclusiveMiss.has(missChain{base: base, typ: t}, m.probeListGen())
}

// probeListGen is the configuration the miss memory is valid for: change the
// format order or the fallback toggles and every remembered "we tried
// everything" was an answer about a list that is no longer the list (missSet).
//
// Read from the preferences on every consult rather than cached here, because
// the mutations that move it are user-driven and rare while the consults are
// per-cell and hot — an atomic load is cheaper than any arrangement for being
// told. Zero without preferences (headless rigs): a constant generation just
// means nothing ever retires early.
func (m *Manager) probeListGen() uint64 {
	if m.prefs == nil {
		return 0
	}
	return m.prefs.FormatGeneration()
}

// ForgetConclusiveMisses empties the session's exhausted-chain memory, so every
// asset that 404'd anywhere becomes probeable again.
//
// This is the explicit half of the deal missSet makes: it never expires on a
// timer, so something has to say when the answer might have changed. The
// blanket form is for the changes that are not confined to one server — the
// byte sources (SetMountLayer / SetLocalOverlay call it themselves, so no
// caller can forget) and the user's Settings button. A reconnect uses the
// scoped form below.
func (m *Manager) ForgetConclusiveMisses() {
	if m == nil {
		return
	}
	m.conclusiveMiss.forget()
}

// ForgetConclusiveMissesUnder empties the remembered misses for ONE asset
// origin. It is what a (re)connect calls: joining a server is the moment to
// give that server's absent assets a fresh look, and it is not a statement
// about any OTHER server whose tab is still open against the same Manager.
func (m *Manager) ForgetConclusiveMissesUnder(origin string) {
	if m == nil {
		return
	}
	m.conclusiveMiss.forgetUnder(origin)
}

// ConclusiveMissCount reports how many chains are remembered as exhausted, for
// the Settings button's label and the debug overlay.
func (m *Manager) ConclusiveMissCount() int {
	if m == nil {
		return 0
	}
	return m.conclusiveMiss.size(m.probeListGen())
}

// skipExhausted is what a prefetch does INSTEAD of a pipeline pass once the
// chain is known exhausted: nothing on the wire, but the §4 warning still
// fires.
//
// Reporting is deliberately not skipped with the probing. The warning lane is
// not only a fetch outcome — Courtroom.NotifyAssetMissing rides it to skip a
// preanimation that can never arrive (ui/app.go drainWarnings), so swallowing
// the repeat would hold the stage for the full PreanimTimeout every time after
// the first that the same absent preanim is played. The missing COUNTER is not
// bumped, because this pass found nothing out: the count answers "how many
// assets did we discover are absent", and letting a demand cadence inflate it
// is what made it useless to read while this bug was live.
//
// Tried is empty here on purpose rather than remembered per key: the extension
// list was already logged in full when the miss was discovered, and the lane
// already carries nil from resolveExact, so consumers handle it.
func (m *Manager) skipExhausted(base string, t AssetType, suppressMissing bool) {
	if suppressMissing {
		return // speculative: no one asked for this, so no one hears about it (PrefetchChainSpeculative)
	}
	select {
	case m.warningCh <- Warning{Base: base, Type: t}:
	default:
	}
}

// PrefetchExact queues a pipeline pass for a COMPLETE URL (extension
// included) — AO music tracks ship their extension in the track name, and
// direct http(s) tracks are full URLs already. No candidate probing.
func (m *Manager) PrefetchExact(url string, t AssetType, prio network.Priority) {
	if url == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(url) {
		return // recently failed to decode — back off
	}
	// Same session memory as the probing path — the evidence grid paces its
	// thumbnails on exactly the same retry cadence (ui/court_extras.go
	// demandEvidence), so an evidence file the server never uploaded would storm
	// identically. One rule for the whole Manager: a chain already probed to
	// exhaustion this session is not probed again.
	if m.conclusiveMiss.has(missChain{base: url, typ: t}, m.probeListGen()) {
		m.skipExhausted(url, t, false)
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveExact(url, t)
		},
	})
}

// resolveExact is the candidate-free pipeline pass behind PrefetchExact.
func (m *Manager) resolveExact(url string, t AssetType) {
	if _, loaded := m.inflight.LoadOrStore(url, struct{}{}); loaded {
		return
	}
	defer m.inflight.Delete(url)

	if m.t1Contains != nil && m.t1Contains(url) {
		m.t1Hits.Add(1)
		return
	}
	// The pack answers exact URLs too — evidence ships its extension in the LE
	// name and music tracks are full URLs, so neither goes through the candidate
	// ladder. Placed after T1 (a resident texture is already the right answer) but
	// before T2, so a pack file the user just dropped in wins over the server copy
	// cached from an earlier session.
	if layer := m.activeMountLayer(); layer != nil && m.serveFromMount(layer, url, url, t) {
		return
	}
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		m.deliver(url, url, t, data, false)
		return
	}
	if !m.skipDisk(url) {
		if data, ok := m.disk.Get(url); ok {
			m.diskHits.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			m.deliver(url, url, t, data, false)
			return
		}
	}
	data, err := m.netFetch(context.Background(), url)
	switch {
	case err == nil:
		m.netFetches.Add(1)
		m.t2.Add(url, data, int64(len(data)))
		if !m.skipDisk(url) {
			m.disk.Put(url, data)
		}
		m.deliver(url, url, t, data, false)
	case errors.Is(err, network.ErrAssetNotFound):
		// The CURRENT generation, not a sampled one: this path probes a complete
		// URL and never builds a candidate list, so the format preferences had no
		// say in the verdict and cannot have invalidated it mid-pass. It still
		// rides the same generation bucket as everything else, which only means a
		// format change costs this entry one extra probe it did not strictly need.
		m.rememberExhausted(missChain{base: url, typ: t}, m.probeListGen())
		m.reportMissing(url, t, nil)
	default:
		// Transient failure (timeout / 5xx / host backoff). Music is the one
		// exact-fetch type whose failure is otherwise silent (audio never
		// reaches the pump's decode path, so the "counted, not logged" pump
		// branch doesn't even see it) — surface it to the jukebox. Sprite/icon
		// exact fetches stay unlogged (scoped by type).
		if t == AssetTypeMusic {
			m.reportMusicFailure(url, err)
		}
		m.decodedCh <- DecodedAsset{URL: url, Base: url, Type: t, Err: err, Transient: true}
		m.notifyDelivery()
	}
}

// PrefetchRaw warms T2/T3 for a COMPLETE URL without decoding — text
// assets like char.ini. Hover-driven: picking a character then costs a
// memory hit instead of an RTT. Pool-bounded, inflight-deduped, silent
// on 404 (the negative cache absorbs retries).
func (m *Manager) PrefetchRaw(url string, prio network.Priority) {
	if url == "" {
		return
	}
	// Gated like the typed lanes, on the same session memory. warmCharINI latches
	// on the hovered name, so hovering B then back to A re-fires for A — on a
	// server where neither character ships a char.ini that is a fresh pool job
	// and a fresh probe per hover, forever.
	key := missChain{base: url, typ: missTypeRaw}
	if m.conclusiveMiss.has(key, m.probeListGen()) {
		return // no warning: this lane has no AssetType to report and is silent on 404 by contract
	}
	// A file the user's pack holds has no RTT to hide, and FetchRawLayered will
	// read it from disk without ever asking the server — so warming it would buy
	// nothing and spend a real network probe (plus a remembered miss) on a URL the
	// server may well not have at all. Covers is a MAP lookup, never a read: this
	// runs on the render thread (warmCharINI fires from a hover), where reading the
	// file would be the synchronous disk I/O hard rule 2 forbids.
	if l := m.activeMountLayer(); l != nil && l.Covers(url) {
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			if _, loaded := m.inflight.LoadOrStore(url, struct{}{}); loaded {
				return
			}
			defer m.inflight.Delete(url)
			if _, ok := m.t2.Get(url); ok {
				m.t2Hits.Add(1)
				return
			}
			if !m.skipDisk(url) {
				if data, ok := m.disk.Get(url); ok {
					m.diskHits.Add(1)
					m.t2.Add(url, data, int64(len(data)))
					return
				}
			}
			data, err := m.netFetch(context.Background(), url)
			if err != nil {
				// The current generation, for resolveExact's reason: a complete URL
				// builds no candidate list, so the format preferences had no say.
				if errors.Is(err, network.ErrAssetNotFound) {
					m.rememberExhausted(key, m.probeListGen())
				}
				return // any other error is transient and leaves the URL probeable
			}
			m.netFetches.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			if !m.skipDisk(url) {
				m.disk.Put(url, data)
			}
		},
	})
}

// FetchRaw synchronously fetches a complete URL through T2 → T3 → source
// without decoding — for text assets like char.ini. Call it off the render
// thread (UI screens use a goroutine).
func (m *Manager) FetchRaw(ctx context.Context, url string) ([]byte, error) {
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		return data, nil
	}
	if !m.skipDisk(url) {
		if data, ok := m.disk.Get(url); ok {
			m.diskHits.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			return data, nil
		}
	}
	data, err := m.netFetch(ctx, url)
	if err != nil {
		return nil, err
	}
	m.netFetches.Add(1)
	m.t2.Add(url, data, int64(len(data)))
	if !m.skipDisk(url) {
		m.disk.Put(url, data)
	}
	return data, nil
}

// ResolveRaw resolves an extensionless base to the first candidate URL whose
// bytes fetch — learned-first, the SAME candidate order the render path probes
// (BuildCandidates) — returning that complete URL and its bytes. Synchronous,
// for tooling (the scene-archive exporter) that needs the resolved file itself,
// not a decoded texture. It learns the winning format so a repeat call is a
// single-probe hit. ok=false when every candidate is missing.
//
// Because export and replay both resolve through this same candidate logic, the
// relative path an asset is written to (resolvedURL minus origin) is exactly the
// path replay will later request — symmetry by construction (no hand-built
// paths, no pre-seeded format table needed).
func (m *Manager) ResolveRaw(base string, t AssetType) (string, []byte, bool) {
	if base == "" || !t.Valid() {
		return "", nil, false
	}
	host := hostOf(base)
	cands := m.resolver.BuildCandidates(base, t, host)
	defer m.resolver.PutCandidates(cands)
	for _, url := range cands.URLs {
		data, err := m.FetchRaw(context.Background(), url)
		if err == nil && len(data) > 0 {
			m.resolver.RecordSuccess(host, t, url[len(base):]) // learn the winning ext
			return url, data, true
		}
	}
	return "", nil, false
}

// ResolveRawFull is the DIAGNOSTIC sibling of ResolveRaw for a user-invoked,
// bounded packaging/report pass (contentjob.go's probe, the export loader's
// seed) — NOT the live render path. The difference is the miss policy for an
// UNLEARNED host:
//
//   - Learned host: try the learned format FIRST (one probe). A HIT returns
//     immediately — the fast single-probe path a manifest-seeded or single-format
//     host takes. A MISS falls through to the full walk rather than being honored
//     as a terminal miss: the learned entry may have been won opportunistically by
//     a sibling asset earlier in THIS pass (one shared per-(host,type) slot), and
//     honoring it would strand a mixed-format sibling at the wrong format (see the
//     WHY block on the method body). Confirming true absence across every format is
//     the diagnostic path's job; the extra probes are all 404-TTL-cached.
//   - Unlearned host (no manifest, or the manifest lacks this type): walk the
//     FULL configured chain (the type's format order + its legacy fallback
//     chain, deduped) instead of the zero-fallback single format, and
//     RecordSuccess the winner so the very next ResolveRaw/warm of the same host
//     is learned-first (a re-report or an export right after the report probes
//     once, not the whole chain again).
//
// WHY the zero-fallback pillar doesn't apply here: that pillar protects the
// LIVE render hot path — one network probe per asset, no speculative format
// storm competing with the session's traffic. This is a user-triggered
// diagnostic/packaging pass whose whole POINT is truth over probe count: a
// server that "definitely has" a .gif/.png sprite must be findable even though
// the client would never stream it that way live. The walk is still exactly one
// network probe per candidate, and every 404 is cached by the 404-TTL layer +
// collapsed by singleflight (FetchRaw → the same T2/T3/source pipeline), so a
// repeat pass over the same scene re-probes nothing. The learned-first branch
// above means a host whose learned format HITS is single-probed; only an unknown
// or learned-but-missing asset walks the chain (the latter is how a mixed-format
// sibling recovers from another sibling's opportunistically-learned format).
func (m *Manager) ResolveRawFull(base string, t AssetType) (string, []byte, bool) {
	if base == "" || !t.Valid() {
		return "", nil, false
	}
	host := hostOf(base)
	// Learned host → try the learned format FIRST (the fast, single-probe path a
	// manifest-seeded or single-format host takes). A HIT returns immediately.
	//
	// A MISS deliberately does NOT stop here — it falls through to the full walk
	// below. This is the mixed-format-honesty case: the learned entry may have
	// been won OPPORTUNISTICALLY by a sibling asset earlier in this same pass
	// (ResolveRaw/ResolveRawFull's RecordSuccess writes one shared per-(host,type)
	// slot), not authoritatively from a manifest. On a no-manifest host whose
	// assets carry MIXED per-asset formats — charA only .gif, charB only .png —
	// whichever probes first learns its format; honoring that as a terminal miss
	// for the OTHER asset would 404 it at the wrong format and falsely report a
	// real file Missing, nondeterministically by worker order. The learned table
	// can't tell manifest-authoritative from walk-opportunistic (both go through
	// RecordLearned), so the diagnostic path treats a learned MISS as "probe the
	// rest of the chain to confirm" rather than "it isn't there." Confirming true
	// absence across every format is exactly the diagnostic path's job; the
	// zero-fallback pillar (one probe, honor the miss) protects only the LIVE
	// render path, which never calls this method. Every re-probe here is one
	// network probe per candidate, cached by the 404-TTL layer + collapsed by
	// singleflight, so the walk after a learned miss adds no wire traffic once the
	// first sibling has probed the chain.
	if ext, ok := m.resolver.Learned(host, t); ok {
		url := base + ext
		if data, err := m.FetchRaw(context.Background(), url); err == nil && len(data) > 0 {
			return url, data, true
		}
		// fall through to the full walk (see WHY above)
	}
	// Unlearned host (or a learned format that missed) → walk the full configured
	// chain, learning the winner so the common SINGLE-format no-manifest server
	// (whole host is .png) is learned-first for the export warm that runs right
	// after. A subsequent sibling of a DIFFERENT format still full-walks via the
	// learned-miss fall-through above, so learning the winner never strands a
	// mixed-format sibling.
	for _, ext := range fullProbeChain(m.prefs, t) {
		url := base + ext
		data, err := m.FetchRaw(context.Background(), url)
		if err == nil && len(data) > 0 {
			m.resolver.RecordSuccess(host, t, ext) // learn so a repeat pass is single-probe
			return url, data, true
		}
	}
	return "", nil, false
}

// fullProbeChain is the FULL diagnostic probe order for one asset type: the
// user's configured format order (which defaults to the zero-fallback single
// format) followed by the type's legacy fallback chain, deduped, order
// preserved. This is deliberately the type's OWN chain rather than one global
// image list, so audio types (SFX/Blip) walk .opus→.ogg/.wav/.mp3 while image
// types walk .webp→.apng/.gif/.png — each covering exactly the formats its
// decoder/mixer supports. Equivalent to FormatList with fallbacks forced on,
// which is the table-free FULL candidate set the diagnostic path needs.
func fullProbeChain(prefs *config.AssetPreferences, t AssetType) []string {
	name := t.Name()
	var order []string
	if prefs != nil {
		order = prefs.FormatOrder(name) // configured order (defaults to the zero-fallback list)
	}
	if len(order) == 0 {
		order = config.DefaultFormatOrder(name)
	}
	out := make([]string, 0, len(order)+len(config.LegacyFallbackChain(name)))
	seen := map[string]bool{}
	appendExt := func(exts []string) {
		for _, e := range exts {
			if e == "" || seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	appendExt(order)
	appendExt(config.LegacyFallbackChain(name))
	return out
}

// PrefetchSticky is Prefetch for assets that survive room changes (UI
// chrome, theme bits).
func (m *Manager) PrefetchSticky(base string, t AssetType, prio network.Priority) {
	if base == "" || !t.Valid() {
		return
	}
	// The same gate as every other entry point, and the one it can least afford
	// to skip: a sticky asset outlives room changes by definition, so an absent
	// one is re-demanded in every room the session ever visits.
	if m.conclusiveMiss.has(missChainFor(base, nil, t), m.probeListGen()) {
		m.skipExhausted(base, t, false)
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: network.EpochAny,
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveChain(base, nil, t, false)
		},
	})
}

// resolveChain runs the pipeline for primary, then — only while every
// format of each earlier name is missing — for each alt in order,
// delivering under primary so the asset's identity never changes.
// suppressMissing skips the final §4 warning for a total miss (speculative
// prefetches only — see PrefetchChainSpeculative).
func (m *Manager) resolveChain(primary string, alts []string, t AssetType, suppressMissing bool) {
	key := primary + config.LearnedKeySeparator + t.Name()
	if _, loaded := m.inflight.LoadOrStore(key, struct{}{}); loaded {
		return // an identical pass is already in flight
	}
	defer m.inflight.Delete(key)
	// Sampled BEFORE the walk, not at the end: a verdict is only about the probe
	// list that produced it, and rememberExhausted drops the record if the user
	// changed that list while this pass was running.
	startGen := m.probeListGen()

	// T1: already a texture — nothing to do. Uploads from this path are
	// keyed by base (TextureStore.Upload(d.Base, …)), so the check must use
	// base too; checking the extension-included candidate URL can never hit.
	if m.t1Contains != nil && m.t1Contains(primary) {
		m.t1Hits.Add(1)
		return
	}

	// The local pack is consulted once per PASS, not once per candidate: one
	// atomic load for the whole resolution. nil is the no-mounts default and
	// costs nothing measurable.
	layer := m.activeMountLayer()

	host := hostOf(primary)
	// Pack-first WITHIN each spelling, in the ladder's own order — NOT the pack
	// swept across every spelling first. EmoteBare takes no EmoteKind, so the (a)
	// and (b) chains share a byte-identical bare alt; sweeping provider-major
	// would let a legacy bare-named pack serve ONE file as both idle and talk,
	// shadowing a server that ships proper (a)/(b) art. This ordering keeps AO's
	// specificity ladder intact and leaves the network probe budget unchanged.
	if layer != nil && m.serveFromMount(layer, primary, primary, t) {
		return
	}
	done, tried := m.tryBase(primary, primary, t, host)
	if done {
		return
	}
	for i, alt := range alts {
		if alt == "" || alt == primary || containsString(alts[:i], alt) {
			continue // blank / duplicate spelling — nothing new to probe
		}
		if layer != nil && m.serveFromMount(layer, alt, primary, t) {
			return
		}
		var altTried []string
		done, altTried = m.tryBase(alt, primary, t, host)
		if done {
			return
		}
		for _, ext := range altTried {
			if !containsString(tried, ext) {
				tried = append(tried, ext)
			}
		}
	}
	m.rememberExhausted(missChainFor(primary, alts, t), startGen)
	if suppressMissing {
		// Speculative pass: count the miss (metrics) but do not surface the
		// visible §4 warning for an asset no one demanded.
		m.missing.Add(1)
		return
	}
	m.reportMissing(primary, t, tried)
}

// rememberExhausted records a chain that came back empty from every spelling
// and every format, so the next demand for it costs a map lookup instead of a
// pipeline pass (missSet).
//
// It holds both rules for when a 404 is NOT a finding, because both are
// judgements about the pass rather than about the set:
//
//   - OFFLINE. Rehearsal mode makes netFetch answer ErrAssetNotFound for every
//     candidate without asking anyone (see netFetch), so recording those would
//     carry rehearsal's silence into the live session and blank assets that were
//     only ever absent because the network was closed. The resolver refuses to
//     learn formats from that same silence for the same reason.
//
//   - THE LIST MOVED UNDER US. startGen is the probe-list generation this pass
//     began with. A pass that walked the old list and lands after the user
//     changed the format preferences knows nothing about the new one, and
//     recording it would tag an obsolete verdict as current — precisely what the
//     generation exists to prevent, just through a narrower window. Dropping the
//     record costs one more pipeline pass; keeping it would cost the asset for
//     the rest of the session.
func (m *Manager) rememberExhausted(k missChain, startGen uint64) {
	if m.offline.Load() {
		return
	}
	if startGen != m.probeListGen() {
		return
	}
	m.conclusiveMiss.add(k, startGen)
}

// tryBase walks one base's format candidates (learned-first), delivering a
// hit under deliverBase. done=true ends the whole pass: delivered, or a
// transport error already reported on the decoded channel (remaining
// probes on the same ailing host would only stack timeouts). A stale
// learned format triggers the one-shot full-list re-probe inline (spec §4).
func (m *Manager) tryBase(base, deliverBase string, t AssetType, host string) (done bool, tried404 []string) {
	cands := m.resolver.BuildCandidates(base, t, host)
	usedLearned := cands.Learned
	done, tried404 = m.walkCandidates(cands.URLs, base, deliverBase, t, host, make([]string, 0, len(cands.URLs)))
	m.resolver.PutCandidates(cands)
	if done || !usedLearned {
		return done, tried404
	}

	// Every learned-first candidate 404'd on a learned format: the server may
	// have repacked. Re-probe the full configured format list once, skipping
	// extensions already tried. We must NOT blank the shared learned slot to do
	// this: it is one slot per (host, AssetType) shared by every asset of that
	// type on the host, and clearing it opens a window in which a DIFFERENT
	// concurrent asset (resolved on another pool worker, unserialized) reads the
	// empty slot, falls back to the type default, and spuriously reports a file
	// that exists in the learned format as missing (the "every emote button
	// renders the same icon" report on a non-default-format host). Instead
	// BuildFullListCandidates returns the format list WITHOUT touching the table.
	// If a fallback format answers, walkCandidates' RecordSuccess re-learns it
	// via a single old-valid -> new-valid CAS (a genuine repack heals here). If
	// nothing answers, the asset is simply absent — and absence says nothing
	// about the host's formats, so the learned entry is left exactly as it was.
	cands = m.resolver.BuildFullListCandidates(base, t, host)
	rest := make([]string, 0, len(cands.URLs))
	for _, url := range cands.URLs {
		if !containsString(tried404, url[len(base):]) {
			rest = append(rest, url)
		}
	}
	done, tried404 = m.walkCandidates(rest, base, deliverBase, t, host, tried404)
	m.resolver.PutCandidates(cands)
	return done, tried404
}

// walkCandidates probes urls in order through T2 → T3 → source. done=true
// ends the pass (delivered under deliverBase, or transport error reported);
// tried404 accumulates the extensions that 404'd.
func (m *Manager) walkCandidates(urls []string, base, deliverBase string, t AssetType, host string, tried404 []string) (bool, []string) {
	for _, url := range urls {
		ext := url[len(base):]

		// T2: raw bytes in memory — straight to decode.
		if data, ok := m.t2.Get(url); ok {
			m.t2Hits.Add(1)
			m.deliver(url, deliverBase, t, data, false)
			return true, tried404
		}
		// T3: disk — promote to T2, learn, decode (spec §8). Skipped for a
		// local:// URL (local mode, or a streaming manager's mount overlay): the
		// mounts ARE disk, so a disk-tier copy would just duplicate them.
		if !m.skipDisk(url) {
			if data, ok := m.disk.Get(url); ok {
				m.diskHits.Add(1)
				m.t2.Add(url, data, int64(len(data)))
				m.resolver.RecordSuccess(host, t, ext)
				m.deliver(url, deliverBase, t, data, false)
				return true, tried404
			}
		}
		// Source: network stream, local mounts (local mode), or the streaming
		// manager's local:// overlay (netFetch routes by scheme).
		data, err := m.netFetch(context.Background(), url)
		switch {
		case err == nil:
			m.netFetches.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			if !m.skipDisk(url) {
				m.disk.Put(url, data)
			}
			m.resolver.RecordSuccess(host, t, ext)
			m.deliver(url, deliverBase, t, data, false)
			return true, tried404
		case errors.Is(err, network.ErrAssetNotFound):
			tried404 = append(tried404, ext)
			continue // probe the next candidate
		default:
			// Transport trouble: the render side hears the error — tagged
			// transient, so it never enters the decode negative cache.
			m.decodedCh <- DecodedAsset{URL: url, Base: deliverBase, Type: t, Err: err, Transient: true}
			m.notifyDelivery()
			return true, tried404
		}
	}
	return false, tried404
}

// deliver routes payload bytes onward: audio types skip the decode pool
// entirely; images are decoded off-thread and land on the decoded channel.
// fromPack marks bytes that came from the user's local mounts rather than the
// server. It rides all the way to the render side because Base stays the
// SERVER's URL either way — see DecodedAsset.FromPack.
func (m *Manager) deliver(url, base string, t AssetType, data []byte, fromPack bool) {
	if t.IsAudio() {
		m.audioCh <- AudioAsset{URL: url, Base: base, Type: t, Data: data, FromPack: fromPack}
		m.notifyDelivery()
		return
	}
	m.decoder.Submit(DecodeRequest{
		URL:            url,
		Data:           data,
		Type:           t,
		PlayAnimations: m.prefs.AnimationsEnabled(),
		OnDone: func(doneURL string, d *Decoded, err error) {
			// Opt-in thumbnail store: every character sprite that decodes leaves a
			// tiny low-q stand-in behind (ThumbCache gates on its own enable and
			// Store is a non-blocking enqueue, so this is free when off).
			//
			// NEVER for pack bytes. ThumbCache is a PERSISTENT disk cache keyed by
			// BASE — the server's URL — so storing here would write the user's own
			// art to disk under the server's identity, where it outlives the pack and
			// keeps showing as that server's cold-load stand-in after the folder is
			// gone.
			if m.thumbs != nil && err == nil && t == AssetTypeCharSprite && !fromPack {
				m.thumbs.Store(base, d)
			}
			m.decodedCh <- DecodedAsset{URL: doneURL, Base: base, Type: t, Asset: d, Err: err, FromPack: fromPack}
			m.notifyDelivery()
		},
	})
}

// SetMountLayer installs (or clears, with nil) the local-pack layer consulted
// before the network. Safe from the render thread mid-session: readers load the
// atomic pointer and never lock (rule 5).
//
// Installing a layer is the user asserting they have files the server did not
// serve, so it drops the session's exhausted-chain memory with it — supplying a
// missing asset is the whole reason to add a pack, and the prefetch gate sits
// ABOVE the layer, so a remembered miss would shadow exactly the files that were
// just made available. Done here rather than at the call sites for the same
// reason ui/mountlayer.go forgets the texture store's negative caches inside
// rescanLocalPacks: a caller that skips it fails silently.
func (m *Manager) SetMountLayer(l *MountLayer) {
	m.ForgetConclusiveMisses()
	m.mountLayer.Store(l)
}

// MountLayer returns the live layer (nil when no mounts are configured).
func (m *Manager) MountLayer() *MountLayer { return m.mountLayer.Load() }

// activeMountLayer returns the layer to consult for this pass, or nil.
//
// THE NIL CHECK COMES FIRST, and that ordering is the whole no-mounts contract:
// with no local mounts configured — the overwhelmingly common case — the entire
// feature costs ONE atomic load and a predicted branch. No index is built, no
// goroutine runs, no directory is walked, no allocation happens, and no extra
// probe is issued. Testing localMode or archiveSrc first would spend a second
// load on every user who has never opened the Settings page.
func (m *Manager) activeMountLayer() *MountLayer {
	l := m.mountLayer.Load()
	if l == nil {
		return nil
	}
	if m.localMode {
		// Local-only mode's SOURCE already is the mount folders; layering a mount
		// origin over a mount origin would double-serve. The pref layer normalizes
		// this too (config.LayeredAssets), but a Manager is mode-locked at
		// construction while the pref can invert under it, so enforce it here.
		return nil
	}
	if m.archiveSrc.Load() != nil {
		return nil // a bundled replay stays hermetic: the live pack must not fill its holes
	}
	return l
}

// serveFromMount answers base from the user's local mounts, delivering under
// deliverBase, and reports whether it did.
//
// IT RETURNS NO ERROR, ON PURPOSE. That is what makes "a pack failure is never a
// server failure" structural instead of conventional: there is no expressible way
// for a pack read error to reach walkCandidates' pass-aborting default arm, so
// one unreadable file — a yanked USB mount, an EACCES, a file deleted after
// indexing — falls through to the network instead of killing the whole pass.
//
// A read error deliberately does NOT quarantine: it is environmental and usually
// transient, and stickily disabling a pack that recovers would be worse than
// re-reading it. Only a DECODE failure quarantines, because that is a
// deterministic property of the bytes (see QuarantinePack).
func (m *Manager) serveFromMount(l *MountLayer, base, deliverBase string, t AssetType) bool {
	key, data, ok := m.packBytes(l, base, t)
	if !ok {
		return false
	}
	m.mountFetches.Add(1)
	// T2 under the PACK's transport key, which is a disjoint local:// keyspace —
	// it can never collide with the server's URL. skipDisk keeps it out of T3
	// for free. Caching matters here: T1 evicts under the texture budget long
	// before T2 does, so without it every evicted pack background or long
	// animation would re-read and re-allocate multiple MiB from disk.
	m.t2.Add(l.LocalURLFor(key), data, int64(len(data)))
	m.deliver(l.LocalURLFor(key), deliverBase, t, data, true)
	return true
}

// packBytes is the shared pack lookup: it walks the pack's format list for base
// and returns the winning INDEX KEY plus its bytes.
//
// It does exactly four things — resolve the rel, walk candidates, skip
// quarantined entries, read — and deliberately NOTHING else. No learning, no
// caching, no counters, no delivery. That is what lets both the render path
// (serveFromMount, which then caches and delivers) and the tooling path
// (ResolveRawLayered, which must do neither) share one implementation without
// the tooling path inheriting side effects that would poison the server's
// learned formats or disk cache.
func (m *Manager) packBytes(l *MountLayer, base string, t AssetType) (key string, data []byte, ok bool) {
	idx := l.Index()
	if idx == nil || !idx.acquire() {
		return "", nil, false
	}
	defer idx.release()

	rel, has := l.RelOf(base)
	if !has || mountLayerExcluded(rel) {
		return "", nil, false
	}
	folded := foldRel(rel)
	mask, haveStem := idx.LookupStem(folded)

	for _, ext := range m.resolver.PackFormatList(t) {
		// The mask is a fast reject that costs no map lookup. An extension absent
		// from indexedExts has no bit, so it falls through to a direct probe rather
		// than being silently unreachable.
		if bit, indexed := extBit(ext); indexed && haveStem && mask&bit == 0 {
			continue
		}
		k := folded + ext
		f, hit := idx.LookupExact(k)
		if !hit || idx.IsBad(k) {
			continue
		}
		b, err := idx.ReadFile(f, k)
		if err != nil || len(b) == 0 {
			continue // fall through to the next candidate, then to the network
		}
		return k, b, true
	}
	return "", nil, false
}

// packExactBytes is packBytes for a COMPLETE URL: no format walk, one index
// lookup. It is the text-asset sibling of the probe path's lookup, and it exists
// because a char.ini is not a format family — its URL is already exact, so
// walking PackFormatList over it would only manufacture nonsense keys
// (char.ini.webp).
//
// Same four things and nothing else — resolve the rel, skip quarantined entries,
// read, return — so both the synchronous reader (FetchRawLayered) and the warm
// path (PrefetchRaw's early-out) share one implementation without either
// inheriting side effects.
func (m *Manager) packExactBytes(l *MountLayer, url string) ([]byte, bool) {
	idx := l.Index()
	if idx == nil || !idx.acquire() {
		return nil, false
	}
	defer idx.release()

	rel, has := l.RelOf(url)
	if !has || mountLayerExcluded(rel) {
		return nil, false
	}
	folded := foldRel(rel)
	f, hit := idx.LookupExact(folded)
	if !hit || idx.IsBad(folded) {
		return nil, false
	}
	b, err := idx.ReadFile(f, folded)
	if err != nil || len(b) == 0 {
		return nil, false // a pack failure is never a server failure: fall through
	}
	return b, true
}

// FetchRawLayered is FetchRaw for the text assets a content pack legitimately
// ships — char.ini above all, and misc/<folder>/effects.ini beside it.
//
// WHY IT EXISTS AT ALL (issue #72). The pack layer was wired into the probe path
// (resolveChain) and the exact DECODE path (resolveExact), but never into the raw
// text lane, so a mounted base served a sprite maker's art and then read their
// character's emote list, showname, blips, chatbox skin, scaling and idle pose
// off the SERVER. Art from one base, metadata from another, is not a
// half-working feature — it is a character that looks right and behaves like
// somebody else's.
//
// It is a SEPARATE method rather than a flag inside FetchRaw for the reason
// ResolveRawLayered is: FetchRaw writes T2 AND T3 under the passed URL, and the
// callers that must NOT see a pack (the extensions.json manifest fetch, the
// autoindex listings, the downloader's deliberate read-from-the-server) keep the
// unlayered method by name rather than by remembering to pass false.
//
// THE PACK IS CONSULTED BEFORE T2, like the exact decode path (resolveExact) and
// for the same reason: a server copy cached earlier this session must not beat
// the file the user just edited on disk. Pack bytes are then returned WITHOUT
// being cached anywhere — no T2 even under the local:// key, no T3. A char.ini is
// a couple of KiB read once per character per session (every call site dedupes:
// charMetaCache, previewChar, the emote list's own latch), and a cache here would
// only add a way for a rescan to serve a stale ini back — the precise complaint
// this method exists to answer.
func (m *Manager) FetchRawLayered(ctx context.Context, url string) ([]byte, error) {
	if l := m.activeMountLayer(); l != nil {
		if b, ok := m.packExactBytes(l, url); ok {
			m.mountFetches.Add(1)
			return b, nil
		}
	}
	return m.FetchRaw(ctx, url)
}

// ResolveRawLayered is ResolveRaw for tooling that should see the user's local
// packs — the content report and the scene exporter.
//
// It is a SEPARATE method rather than a flag threaded through ResolveRaw, and
// that separation is the entire safety argument. ResolveRaw calls
// resolver.RecordSuccess (which persists through prefs.RecordLearned) and
// bottoms out in FetchRaw, which writes T2 AND T3 under the http URL where
// skipDisk offers no protection. Routing pack bytes through either would teach
// the SERVER host a format its files do not use and leave pack bytes on disk
// under the server's identity — the v1.61.0 / v1.87.2 regression class, only
// worse because it would persist across sessions.
//
// So the pack arm here does none of that: it reads and returns. Only the
// fall-through to the server touches the normal machinery, which is correct,
// because those bytes really are the server's.
//
// fromPack reports which arm answered, so callers can label the result honestly
// — a recipient's experience depends on knowing an asset came from the sender's
// own folders and not from the server.
func (m *Manager) ResolveRawLayered(base string, t AssetType) (url string, data []byte, fromPack, ok bool) {
	if base == "" || !t.Valid() {
		return "", nil, false, false
	}
	if l := m.activeMountLayer(); l != nil {
		if key, b, hit := m.packBytes(l, base, t); hit {
			return l.LocalURLFor(key), b, true, true
		}
	}
	u, b, hit := m.ResolveRaw(base, t)
	return u, b, false, hit
}

// PackCovers reports whether the user's local packs hold anything for base,
// WITHOUT reading it. A pure index lookup, for labelling a report row.
func (m *Manager) PackCovers(base string) bool {
	l := m.activeMountLayer()
	return l != nil && l.Covers(base)
}

// QuarantinePack marks a pack entry whose bytes failed to DECODE, so the next
// demand skips it and the server's copy is fetched instead. url is the pack
// transport URL from DecodedAsset.URL / AudioAsset.URL.
//
// Returns false for a URL that is not this layer's — the layer was swapped, or
// the URL belongs to an archive replay or a mount overlay whose origin carries a
// different mount-set hash. A false return means the caller must record NOTHING:
// bytes that are provably not the server's must never negative-cache the
// server's base. Render thread only.
func (m *Manager) QuarantinePack(url string) bool {
	l := m.activeMountLayer()
	if l == nil {
		return false
	}
	rel, ok := l.PackRelOf(url)
	if !ok {
		return false
	}
	idx := l.Index()
	if idx == nil || !idx.MarkBad(rel) {
		return false
	}
	m.packQuarantined.Add(1)
	return true
}

// Thumbs exposes the optional low-q thumbnail store (nil when the app was
// built/wired without one) — the ui reaches it for loads, knobs and Clear.
func (m *Manager) Thumbs() *ThumbCache { return m.thumbs }

// PurgeCorrupt evicts a URL's bytes from T2 and (unless local-mode, where the
// mounts ARE the source) queues its T3 blob for async deletion, so the next
// demand refetches clean bytes instead of re-promoting the same corrupt blob
// forever. url is the FULL fetch URL (extension included) that produced the
// corrupt payload — the exact key T2/T3 store under (never the sprite base).
//
// Called from the render thread's decode-error path: T2's onEvict is
// memory-only (byte accounting, no render callback), and disk.Delete never
// touches the disk on this goroutine — it enqueues onto the single async
// writer — so this stays off both the render-thread SDL rule and the no-sync-
// disk-I/O rule. Only NON-transient (corrupt-payload) failures may call this;
// a transient network failure never saw the bytes, so there is nothing to
// purge (see pump.go).
func (m *Manager) PurgeCorrupt(url string) {
	if url == "" {
		return
	}
	if m.t2 != nil {
		m.t2.Remove(url)
	}
	if !m.skipDisk(url) && m.disk != nil {
		m.disk.Delete(url)
	}
}

// ClearDisk wipes the T3 cache (Settings "Clear Disk Cache" button).
func (m *Manager) ClearDisk() error {
	return m.disk.Clear()
}

// DiskRoot exposes the T3 directory (Settings cache browser).
func (m *Manager) DiskRoot() string {
	if m.disk == nil {
		return ""
	}
	return m.disk.Root()
}

// SetDiskCompression toggles zstd for new T3 writes (Settings, live-safe).
func (m *Manager) SetDiskCompression(on bool) {
	if m.disk != nil {
		m.disk.SetCompression(on)
	}
}

// SetDiskBudget sets the T3 auto-prune byte cap (#34; Settings slider,
// live-safe). 0 = unlimited (the default: T3 is a deliberate spec exception,
// so no cache is silently deleted). The writer goroutine sweeps oldest past it.
func (m *Manager) SetDiskBudget(bytes int64) {
	if m.disk != nil {
		m.disk.SetBudget(bytes)
	}
}

// T2Stats snapshots the byte-tier counters (Settings cache browser).
func (m *Manager) T2Stats() cache.MemoryStats {
	if m.t2 == nil {
		return cache.MemoryStats{}
	}
	return m.t2.Stats()
}

// DiskStats snapshots the T3 disk-tier counters from cached atomics (the debug
// cache inspector, #164) — no directory walk, so it is safe to read per frame.
func (m *Manager) DiskStats() cache.DiskStats {
	if m.disk == nil {
		return cache.DiskStats{}
	}
	return m.disk.Stats()
}

// reportMissing surfaces the §4 visible warning; the warning lane may drop
// under flood (advisory, not a result).
func (m *Manager) reportMissing(base string, t AssetType, tried []string) {
	m.missing.Add(1)
	w := Warning{Base: base, Type: t, Tried: tried}
	select {
	case m.warningCh <- w:
	default:
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// hostOf extracts host:port from an absolute URL base (scheme://host/...).
// HostOf returns the host component of an asset URL/origin (the key the learned
// format table uses), exported for the archive exporter's replay seeding.
func HostOf(url string) string { return hostOf(url) }

func hostOf(base string) string {
	const sep = "://"
	i := strings.Index(base, sep)
	if i < 0 {
		return ""
	}
	rest := base[i+len(sep):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// ManagerStats is a point-in-time counter snapshot.
type ManagerStats struct {
	T1Hits     int64
	T2Hits     int64
	DiskHits   int64
	NetFetches int64
	Missing    int64
	// MountFetches counts assets served from the user's local mounts. Without it a
	// pack hit would be invisible in the debug overlay, and the Settings status
	// line would have no honest number to report.
	MountFetches int64
	// PackQuarantined counts pack entries withdrawn after a decode failure, so the
	// UI can say which files it stopped using instead of leaving it mysterious.
	PackQuarantined int64
}

// Stats snapshots the manager's counters.
func (m *Manager) Stats() ManagerStats {
	return ManagerStats{
		T1Hits:          m.t1Hits.Load(),
		T2Hits:          m.t2Hits.Load(),
		DiskHits:        m.diskHits.Load(),
		NetFetches:      m.netFetches.Load(),
		Missing:         m.missing.Load(),
		MountFetches:    m.mountFetches.Load(),
		PackQuarantined: m.packQuarantined.Load(),
	}
}
