// Package config persists user-tunable asset preferences.
//
// Concurrency model (spec §5): mutators take the write lock, mutate in
// memory, and non-blockingly signal a single saver goroutine — they never
// touch the disk. The saver debounces (DefaultSaveDebounce after the last
// signal), marshals under the read lock, writes a temp file, and renames it
// over the real file so a crash never corrupts preferences. SaveNow is the
// only synchronous flush and exists for shutdown and Settings-Apply.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultSaveDebounce is how long the saver waits after the most recent
	// mutation before flushing preferences to disk.
	DefaultSaveDebounce = 250 * time.Millisecond

	// PrefsDirName is the directory under os.UserConfigDir holding all
	// AsyncAO configuration.
	PrefsDirName = "AsyncAO"
	// PrefsFileName is the preferences file name inside PrefsDirName.
	PrefsFileName = "asset_preferences.json"

	// PairOffsetMin and PairOffsetMax bound pair offsets, in percent of the
	// viewport dimension (mirrors AO2-Client's slider range).
	PairOffsetMin = -100
	PairOffsetMax = 100

	// LearnedKeySeparator joins host and asset-type name in learned-format
	// map keys: "<host>|<type name>".
	LearnedKeySeparator = "|"

	prefsFilePerm   = 0o644
	prefsDirPerm    = 0o755
	prefsTmpPattern = PrefsFileName + ".*.tmp"

	jsonMarshalPrefix = ""
	jsonMarshalIndent = "  "
)

// defaultPreferAnimated is the out-of-the-box value for PreferAnimated.
// PreferAnimated is a decode/render toggle (play animation frames vs. first
// frame only) — never an extra network probe (spec §4).
const defaultPreferAnimated = true

// defaultEmoteButtonImages ships the courtroom emote picker as image
// buttons (characters/<char>/emotions/button<N>) rather than text chips.
const defaultEmoteButtonImages = true

// AssetTypePrefs holds the per-asset-type format preferences.
type AssetTypePrefs struct {
	// FormatOrder is the ordered probe list for this type. An empty or
	// missing list means "use the built-in default order".
	FormatOrder []string `json:"formatOrder"`
	// FallbacksEnabled appends this type's legacy chain to FormatOrder.
	FallbacksEnabled bool `json:"fallbacksEnabled"`
}

// AssetPreferences is the persisted user configuration for asset resolution
// and pairing. All exported methods are safe for concurrent use.
type AssetPreferences struct {
	GlobalFallbacksEnabled bool                      `json:"globalFallbacksEnabled"`
	PreferAnimated         bool                      `json:"preferAnimated"`
	EmoteButtonImages      bool                      `json:"emoteButtonImages"`
	ThemeName              string                    `json:"themeName"`
	ThemeDir               string                    `json:"themeDir"`
	AssetTypes             map[string]AssetTypePrefs `json:"assetTypes"`
	LearnedFormats         map[string][]string       `json:"learnedFormats"`
	PairOffsetX            int                       `json:"pairOffsetX"`
	PairOffsetY            int                       `json:"pairOffsetY"`
	PairFlip               bool                      `json:"pairFlip"`
	Showname               string                    `json:"showname"`
	LocalAssetsEnabled     bool                      `json:"localAssetsEnabled"`
	LocalAssetsPaths       []string                  `json:"localAssetsPaths"`
	Favorites              []FavoriteServer          `json:"favorites"`

	mu        sync.RWMutex
	path      string
	dirty     chan struct{} // buffered 1: wake-up signal for the saver
	stop      chan struct{}
	done      chan struct{}
	pending   atomic.Bool // a mutation is awaiting flush
	saveDelay time.Duration
	closeOnce sync.Once
	onSaveErr atomic.Pointer[func(error)]

	// formatGen increments on every mutation that changes any effective
	// probe list (format orders, fallback toggles). Consumers cache derived
	// format tables keyed by this generation — see Resolver's miss path.
	formatGen atomic.Uint64
}

// prefsJSON mirrors the on-disk shape for loading. Pointer fields distinguish
// "absent" from the zero value where the default is not the zero value.
type prefsJSON struct {
	GlobalFallbacksEnabled bool                      `json:"globalFallbacksEnabled"`
	PreferAnimated         *bool                     `json:"preferAnimated"`
	EmoteButtonImages      *bool                     `json:"emoteButtonImages"`
	ThemeName              string                    `json:"themeName"`
	ThemeDir               string                    `json:"themeDir"`
	AssetTypes             map[string]AssetTypePrefs `json:"assetTypes"`
	LearnedFormats         map[string][]string       `json:"learnedFormats"`
	PairOffsetX            int                       `json:"pairOffsetX"`
	PairOffsetY            int                       `json:"pairOffsetY"`
	PairFlip               bool                      `json:"pairFlip"`
	Showname               string                    `json:"showname"`
	LocalAssetsEnabled     bool                      `json:"localAssetsEnabled"`
	LocalAssetsPaths       []string                  `json:"localAssetsPaths"`
	Favorites              []FavoriteServer          `json:"favorites"`
}

// FavoriteServer is a starred or direct-connect server entry (the server
// phone book). URL is the full ws:// or wss:// connection address, which
// also works for private servers that never appear on the master list. The
// description is kept so starred servers stay informative even when the
// master list is unreachable.
type FavoriteServer struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// DefaultPath returns the standard preferences file location:
// <os.UserConfigDir>/AsyncAO/asset_preferences.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating user config dir: %w", err)
	}
	return filepath.Join(dir, PrefsDirName, PrefsFileName), nil
}

// New loads preferences from path (built-in defaults when the file is absent
// or unreadable) and starts the debounced saver goroutine. The returned
// preferences are always usable; a non-nil error reports a malformed or
// unreadable existing file that was replaced by defaults in memory.
// Call Close to flush pending changes and stop the saver.
func New(path string) (*AssetPreferences, error) {
	return newWithDebounce(path, DefaultSaveDebounce)
}

func newWithDebounce(path string, debounce time.Duration) (*AssetPreferences, error) {
	p, err := load(path)
	p.saveDelay = debounce
	p.dirty = make(chan struct{}, 1)
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.saverLoop()
	return p, err
}

// load reads and normalizes the preferences file without starting the saver.
func load(path string) (*AssetPreferences, error) {
	p := &AssetPreferences{
		PreferAnimated:    defaultPreferAnimated,
		EmoteButtonImages: defaultEmoteButtonImages,
		AssetTypes:        defaultAssetTypes(),
		LearnedFormats:    map[string][]string{},
		path:              path,
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil // first run
	}
	if err != nil {
		return p, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var onDisk prefsJSON
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return p, fmt.Errorf("config: parsing %s (using defaults): %w", path, err)
	}

	p.GlobalFallbacksEnabled = onDisk.GlobalFallbacksEnabled
	if onDisk.PreferAnimated != nil {
		p.PreferAnimated = *onDisk.PreferAnimated
	}
	if onDisk.EmoteButtonImages != nil {
		p.EmoteButtonImages = *onDisk.EmoteButtonImages
	}
	p.ThemeName = onDisk.ThemeName
	p.ThemeDir = onDisk.ThemeDir
	for name, tp := range onDisk.AssetTypes {
		if len(tp.FormatOrder) == 0 {
			tp.FormatOrder = DefaultFormatOrder(name)
		}
		p.AssetTypes[name] = tp
	}
	if onDisk.LearnedFormats != nil {
		p.LearnedFormats = onDisk.LearnedFormats
	}
	p.PairOffsetX = clampPairOffset(onDisk.PairOffsetX)
	p.PairOffsetY = clampPairOffset(onDisk.PairOffsetY)
	p.PairFlip = onDisk.PairFlip
	p.Showname = onDisk.Showname
	p.LocalAssetsEnabled = onDisk.LocalAssetsEnabled
	p.LocalAssetsPaths = onDisk.LocalAssetsPaths
	p.Favorites = onDisk.Favorites
	return p, nil
}

// SetSaveErrorHook installs fn to receive asynchronous save failures from the
// saver goroutine. The default hook logs via the standard logger.
func (p *AssetPreferences) SetSaveErrorHook(fn func(error)) {
	p.onSaveErr.Store(&fn)
}

func (p *AssetPreferences) reportSaveError(err error) {
	if fn := p.onSaveErr.Load(); fn != nil {
		(*fn)(err)
		return
	}
	log.Printf("config: async save failed: %v", err)
}

// markDirty records a pending change and wakes the saver without blocking,
// regardless of how many signals are already queued.
func (p *AssetPreferences) markDirty() {
	p.pending.Store(true)
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

// saverLoop debounces dirty signals and flushes preferences to disk. It owns
// no locks while idle and never blocks mutators.
func (p *AssetPreferences) saverLoop() {
	defer close(p.done)
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-p.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-p.dirty:
			// Restart the debounce window on every new mutation.
			if timer == nil {
				timer = time.NewTimer(p.saveDelay)
				timerC = timer.C
			} else {
				timer.Reset(p.saveDelay)
			}
		case <-timerC:
			if err := p.SaveNow(); err != nil {
				p.reportSaveError(err)
			}
		}
	}
}

// SaveNow synchronously marshals current preferences and atomically replaces
// the preferences file (temp file + rename). It is intended for shutdown and
// Settings-Apply; routine mutations rely on the debounced saver instead.
func (p *AssetPreferences) SaveNow() error {
	// Clear before marshaling: a concurrent mutation re-marks pending and is
	// picked up by the next flush even if this marshal misses it.
	p.pending.Store(false)

	p.mu.RLock()
	data, err := json.MarshalIndent(p, jsonMarshalPrefix, jsonMarshalIndent)
	p.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("config: marshaling preferences: %w", err)
	}
	return atomicWriteFile(p.path, data, prefsFilePerm)
}

// Close stops the saver goroutine and flushes any pending change. It is safe
// to call multiple times; only the first call does work.
func (p *AssetPreferences) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.stop)
		<-p.done
		if p.pending.Load() {
			err = p.SaveNow()
		}
	})
	return err
}

// atomicWriteFile writes data to a temp file in path's directory, syncs it,
// and renames it over path so readers never observe a partial file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, prefsDirPerm); err != nil {
		return fmt.Errorf("config: creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, prefsTmpPattern)
	if err != nil {
		return fmt.Errorf("config: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(fmt.Errorf("config: writing %s: %w", tmpName, err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("config: syncing %s: %w", tmpName, err))
	}
	if err := tmp.Close(); err != nil {
		return cleanup(fmt.Errorf("config: closing %s: %w", tmpName, err))
	}
	// Best effort: CreateTemp uses 0600; widen to perm where supported.
	_ = os.Chmod(tmpName, perm)
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// --- Format lists -----------------------------------------------------------

// FormatGeneration returns the probe-list generation: it changes whenever
// any SetFormatOrder/SetTypeFallbacks/SetGlobalFallbacks mutation lands, so
// derived caches know when to rebuild without holding locks.
func (p *AssetPreferences) FormatGeneration() uint64 {
	return p.formatGen.Load()
}

// FormatList implements spec §4: with fallbacks OFF it returns exactly
// the configured probe list for the type; with fallbacks ON (globally or for
// this type) it returns the configured list plus the type's legacy chain,
// deduplicated, order preserved. The result is a fresh slice.
func (p *AssetPreferences) FormatList(typeName string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tp, ok := p.AssetTypes[typeName]
	order := tp.FormatOrder
	if !ok || len(order) == 0 {
		order = defaultFormatOrders[typeName]
	}

	withFallbacks := p.GlobalFallbacksEnabled || tp.FallbacksEnabled
	capacity := len(order)
	if withFallbacks {
		capacity += len(legacyFallbackChains[typeName])
	}
	list := make([]string, 0, capacity)
	for _, ext := range order {
		if !slices.Contains(list, ext) {
			list = append(list, ext)
		}
	}
	if withFallbacks {
		for _, ext := range legacyFallbackChains[typeName] {
			if !slices.Contains(list, ext) {
				list = append(list, ext)
			}
		}
	}
	return list
}

// FormatOrder returns the configured (pre-fallback) probe order for a type.
func (p *AssetPreferences) FormatOrder(typeName string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if tp, ok := p.AssetTypes[typeName]; ok && len(tp.FormatOrder) > 0 {
		return cloneStrings(tp.FormatOrder)
	}
	return DefaultFormatOrder(typeName)
}

// SetFormatOrder replaces the probe order for a type and invalidates learned
// formats for that type on every host (the learned format may no longer be
// first preference). No-op when the order is unchanged.
func (p *AssetPreferences) SetFormatOrder(typeName string, order []string) {
	p.mu.Lock()
	tp := p.AssetTypes[typeName]
	if slices.Equal(tp.FormatOrder, order) {
		p.mu.Unlock()
		return
	}
	tp.FormatOrder = cloneStrings(order)
	if p.AssetTypes == nil {
		p.AssetTypes = map[string]AssetTypePrefs{}
	}
	p.AssetTypes[typeName] = tp
	p.dropLearnedTypeLocked(typeName)
	p.mu.Unlock()
	p.formatGen.Add(1)
	p.markDirty()
}

// ResetFormatOrder restores the built-in default order for a type and
// invalidates learned formats for that type.
func (p *AssetPreferences) ResetFormatOrder(typeName string) {
	p.SetFormatOrder(typeName, DefaultFormatOrder(typeName))
}

// TypeFallbacksEnabled reports whether the legacy chain is enabled for the
// type specifically (the global toggle is separate).
func (p *AssetPreferences) TypeFallbacksEnabled(typeName string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AssetTypes[typeName].FallbacksEnabled
}

// SetTypeFallbacks toggles the legacy chain for one type and invalidates
// learned formats for that type. No-op when unchanged.
func (p *AssetPreferences) SetTypeFallbacks(typeName string, enabled bool) {
	p.mu.Lock()
	tp := p.AssetTypes[typeName]
	if tp.FallbacksEnabled == enabled {
		p.mu.Unlock()
		return
	}
	tp.FallbacksEnabled = enabled
	if len(tp.FormatOrder) == 0 {
		tp.FormatOrder = DefaultFormatOrder(typeName)
	}
	if p.AssetTypes == nil {
		p.AssetTypes = map[string]AssetTypePrefs{}
	}
	p.AssetTypes[typeName] = tp
	p.dropLearnedTypeLocked(typeName)
	p.mu.Unlock()
	p.formatGen.Add(1)
	p.markDirty()
}

// GlobalFallbacks reports the global fallback toggle.
func (p *AssetPreferences) GlobalFallbacks() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.GlobalFallbacksEnabled
}

// SetGlobalFallbacks toggles fallbacks for every type and invalidates all
// learned formats (every type's effective probe list changed).
func (p *AssetPreferences) SetGlobalFallbacks(enabled bool) {
	p.mu.Lock()
	if p.GlobalFallbacksEnabled == enabled {
		p.mu.Unlock()
		return
	}
	p.GlobalFallbacksEnabled = enabled
	p.LearnedFormats = map[string][]string{}
	p.mu.Unlock()
	p.formatGen.Add(1)
	p.markDirty()
}

// --- Animation toggle -------------------------------------------------------

// AnimationsEnabled reports the PreferAnimated decode/render toggle.
func (p *AssetPreferences) AnimationsEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PreferAnimated
}

// SetAnimationsEnabled toggles animation playback (ON plays animation frames,
// OFF renders first frames only). Purely decode/render-level: it never
// changes the probe list.
func (p *AssetPreferences) SetAnimationsEnabled(enabled bool) {
	p.mu.Lock()
	if p.PreferAnimated == enabled {
		p.mu.Unlock()
		return
	}
	p.PreferAnimated = enabled
	p.mu.Unlock()
	p.markDirty()
}

// --- Emote button images ----------------------------------------------------

// EmoteButtonImagesEnabled reports whether the courtroom emote picker draws
// the character's emotions/button<N> art (text chips when off).
func (p *AssetPreferences) EmoteButtonImagesEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.EmoteButtonImages
}

// SetEmoteButtonImages toggles image emote buttons. Render-level only: the
// probe list for the EmoteButton type is configured separately.
func (p *AssetPreferences) SetEmoteButtonImages(enabled bool) {
	p.mu.Lock()
	if p.EmoteButtonImages == enabled {
		p.mu.Unlock()
		return
	}
	p.EmoteButtonImages = enabled
	p.mu.Unlock()
	p.markDirty()
}

// --- Theme -------------------------------------------------------------------

// Theme reports the selected theme name ("" = default) and the custom theme
// root directory ("" = no custom root configured).
func (p *AssetPreferences) Theme() (name, dir string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ThemeName, p.ThemeDir
}

// SetTheme stores the selected theme name and custom theme root.
func (p *AssetPreferences) SetTheme(name, dir string) {
	p.mu.Lock()
	if p.ThemeName == name && p.ThemeDir == dir {
		p.mu.Unlock()
		return
	}
	p.ThemeName = name
	p.ThemeDir = dir
	p.mu.Unlock()
	p.markDirty()
}

// --- Learned formats --------------------------------------------------------

// LearnedKey builds the learned-format map key for a host and type name.
func LearnedKey(host, typeName string) string {
	return host + LearnedKeySeparator + typeName
}

// RecordLearned persists ext as the known-working format for (host, type).
// The resolver calls this on the first successful probe; persistence is lazy
// via the debounced saver.
func (p *AssetPreferences) RecordLearned(host, typeName, ext string) {
	key := LearnedKey(host, typeName)
	p.mu.Lock()
	if existing, ok := p.LearnedFormats[key]; ok && len(existing) == 1 && existing[0] == ext {
		p.mu.Unlock()
		return
	}
	if p.LearnedFormats == nil {
		p.LearnedFormats = map[string][]string{}
	}
	p.LearnedFormats[key] = []string{ext}
	p.mu.Unlock()
	p.markDirty()
}

// LearnedSnapshot returns a deep copy of the learned-format table, used to
// warm the resolver's atomic snapshot at startup and on server connect.
func (p *AssetPreferences) LearnedSnapshot() map[string][]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string][]string, len(p.LearnedFormats))
	for k, v := range p.LearnedFormats {
		out[k] = cloneStrings(v)
	}
	return out
}

// ClearLearned wipes every learned format ("Clear Learned Formats" button).
func (p *AssetPreferences) ClearLearned() {
	p.mu.Lock()
	if len(p.LearnedFormats) == 0 {
		p.mu.Unlock()
		return
	}
	p.LearnedFormats = map[string][]string{}
	p.mu.Unlock()
	p.markDirty()
}

// dropLearnedTypeLocked removes learned entries for one type across all
// hosts. Caller holds the write lock.
func (p *AssetPreferences) dropLearnedTypeLocked(typeName string) {
	suffix := LearnedKeySeparator + typeName
	for key := range p.LearnedFormats {
		if strings.HasSuffix(key, suffix) {
			delete(p.LearnedFormats, key)
		}
	}
}

// --- Pairing ----------------------------------------------------------------

// PairOffsets returns the last-used pair offsets in percent (−100..100).
func (p *AssetPreferences) PairOffsets() (x, y int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PairOffsetX, p.PairOffsetY
}

// SetPairOffsets stores the last-used pair offsets, clamped to
// [PairOffsetMin, PairOffsetMax].
func (p *AssetPreferences) SetPairOffsets(x, y int) {
	x, y = clampPairOffset(x), clampPairOffset(y)
	p.mu.Lock()
	if p.PairOffsetX == x && p.PairOffsetY == y {
		p.mu.Unlock()
		return
	}
	p.PairOffsetX, p.PairOffsetY = x, y
	p.mu.Unlock()
	p.markDirty()
}

// PairFlipped reports the persisted pair flip toggle.
func (p *AssetPreferences) PairFlipped() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PairFlip
}

// SetPairFlipped stores the pair flip toggle.
func (p *AssetPreferences) SetPairFlipped(flip bool) {
	p.mu.Lock()
	if p.PairFlip == flip {
		p.mu.Unlock()
		return
	}
	p.PairFlip = flip
	p.mu.Unlock()
	p.markDirty()
}

// --- Local assets (no-streaming legacy mode) ----------------------------------

// LocalAssets reports the no-streaming mode: read assets from user-chosen
// local mount folders instead of the server's asset URL (legacy support for
// servers without an asset server). Mounts are searched in order, first hit
// wins — mirroring AO2-Client mount paths, so any folder layout works, not
// just a default /base.
func (p *AssetPreferences) LocalAssets() (enabled bool, mounts []string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.LocalAssetsPaths))
	copy(out, p.LocalAssetsPaths)
	return p.LocalAssetsEnabled, out
}

// SetLocalAssets toggles no-streaming mode and stores the ordered mount
// folder list.
func (p *AssetPreferences) SetLocalAssets(enabled bool, mounts []string) {
	p.mu.Lock()
	if p.LocalAssetsEnabled == enabled && slices.Equal(p.LocalAssetsPaths, mounts) {
		p.mu.Unlock()
		return
	}
	p.LocalAssetsEnabled = enabled
	p.LocalAssetsPaths = slices.Clone(mounts)
	p.mu.Unlock()
	p.markDirty()
}

// --- Favorites -----------------------------------------------------------------

// FavoriteServers returns the starred server list, in pin order.
func (p *AssetPreferences) FavoriteServers() []FavoriteServer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]FavoriteServer, len(p.Favorites))
	copy(out, p.Favorites)
	return out
}

// AddFavorite stars a server (or updates an existing favorite with the same
// URL). URL must be the full ws://host:port or wss://host:port address, so
// private servers off the master list work identically; the description is
// retained for the phone book.
func (p *AssetPreferences) AddFavorite(name, url, description string) {
	if url == "" {
		return
	}
	p.mu.Lock()
	for i, f := range p.Favorites {
		if f.URL == url {
			if f.Name == name && f.Description == description {
				p.mu.Unlock()
				return
			}
			p.Favorites[i].Name = name
			p.Favorites[i].Description = description
			p.mu.Unlock()
			p.markDirty()
			return
		}
	}
	p.Favorites = append(p.Favorites, FavoriteServer{Name: name, URL: url, Description: description})
	p.mu.Unlock()
	p.markDirty()
}

// RemoveFavorite unstars the server with the given URL.
func (p *AssetPreferences) RemoveFavorite(url string) {
	p.mu.Lock()
	kept := p.Favorites[:0]
	removed := false
	for _, f := range p.Favorites {
		if f.URL == url {
			removed = true
			continue
		}
		kept = append(kept, f)
	}
	p.Favorites = kept
	p.mu.Unlock()
	if removed {
		p.markDirty()
	}
}

// IsFavorite reports whether the URL is starred.
func (p *AssetPreferences) IsFavorite(url string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, f := range p.Favorites {
		if f.URL == url {
			return true
		}
	}
	return false
}

// --- Showname ----------------------------------------------------------------

// SavedShowname returns the persisted in-character showname.
func (p *AssetPreferences) SavedShowname() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Showname
}

// SetShowname persists the in-character showname so it survives restarts and
// is prefilled on the next session.
func (p *AssetPreferences) SetShowname(name string) {
	p.mu.Lock()
	if p.Showname == name {
		p.mu.Unlock()
		return
	}
	p.Showname = name
	p.mu.Unlock()
	p.markDirty()
}

func clampPairOffset(v int) int {
	if v < PairOffsetMin {
		return PairOffsetMin
	}
	if v > PairOffsetMax {
		return PairOffsetMax
	}
	return v
}
