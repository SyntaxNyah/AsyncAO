package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testDebounce keeps saver tests fast while remaining far above scheduler
// jitter.
const testDebounce = 25 * time.Millisecond

// testFlushWait comfortably exceeds testDebounce so a flush must have
// happened (or provably not happened) by the time it elapses.
const testFlushWait = 10 * testDebounce

func newTestPrefs(t *testing.T) (*AssetPreferences, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return p, path
}

// TestSetFormatOrderCompletes is the deadlock regression test demanded by
// spec §5: the drafts held the write lock while calling a Save that took
// the read lock — a guaranteed self-deadlock on Go's non-reentrant RWMutex.
// Mutators must never write disk; this must return almost immediately.
func TestSetFormatOrderCompletes(t *testing.T) {
	p, _ := newTestPrefs(t)

	finished := make(chan struct{})
	go func() {
		p.SetFormatOrder(TypeCharSprite, []string{ExtAPNG, ExtWebP})
		p.SetGlobalFallbacks(true)
		p.SetTypeFallbacks(TypeSFX, true)
		p.RecordLearned("example.com", TypeCharSprite, ExtWebP)
		p.SetPairOffsets(25, -10)
		p.SetPairFlipped(true)
		p.SetAnimationsEnabled(false)
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("mutators did not complete: probable lock-ordering deadlock (mutator writing disk under lock?)")
	}
}

func TestMutatorsDoNotWriteSynchronously(t *testing.T) {
	p, path := newTestPrefs(t)

	p.SetFormatOrder(TypeCharSprite, []string{ExtAPNG})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preferences file exists immediately after mutation: mutators must never write disk (stat err: %v)", err)
	}

	// After the debounce window the saver must have flushed.
	waitForFile(t, path)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(testFlushWait)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("saver never flushed %s within %v", path, testFlushWait)
}

func TestDebounceCoalescesBurst(t *testing.T) {
	p, path := newTestPrefs(t)

	const burst = 50
	for i := 0; i < burst; i++ {
		p.SetPairOffsets(i%(PairOffsetMax+1), 0)
	}
	waitForFile(t, path)
	// Allow any straggler flush to settle, then verify final state on disk.
	time.Sleep(testFlushWait)

	loaded, err := load(path)
	if err != nil {
		t.Fatalf("load after burst: %v", err)
	}
	wantX := (burst - 1) % (PairOffsetMax + 1)
	if loaded.PairOffsetX != wantX {
		t.Errorf("PairOffsetX on disk = %d, want %d (last write must win)", loaded.PairOffsetX, wantX)
	}
}

func TestFormatListZeroFallbackIsExactConfiguredList(t *testing.T) {
	p, _ := newTestPrefs(t)

	got := p.FormatList(TypeCharSprite)
	want := []string{ExtWebP}
	if !slices.Equal(got, want) {
		t.Errorf("FormatList(CharSprite) fallbacks-off = %v, want %v (exactly one probe)", got, want)
	}

	if got := p.FormatList(TypeCharIcon); !slices.Equal(got, []string{ExtPNG}) {
		t.Errorf("FormatList(CharIcon) = %v, want [%s]", got, ExtPNG)
	}
}

func TestFormatListGlobalFallbacksAppendLegacyChain(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.SetGlobalFallbacks(true)

	got := p.FormatList(TypeCharSprite)
	want := []string{ExtWebP, ExtAPNG, ExtGIF, ExtPNG}
	if !slices.Equal(got, want) {
		t.Errorf("FormatList(CharSprite) global fallbacks = %v, want %v", got, want)
	}

	got = p.FormatList(TypeMusic)
	want = []string{ExtOpus, ExtOgg, ExtMP3}
	if !slices.Equal(got, want) {
		t.Errorf("FormatList(Music) global fallbacks = %v, want %v", got, want)
	}
}

func TestFormatListPerTypeFallbackOnlyAffectsThatType(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.SetTypeFallbacks(TypeBackground, true)

	if got, want := p.FormatList(TypeBackground), []string{ExtWebP, ExtAPNG, ExtGIF, ExtPNG}; !slices.Equal(got, want) {
		t.Errorf("FormatList(Background) per-type fallback = %v, want %v", got, want)
	}
	if got, want := p.FormatList(TypeCharSprite), []string{ExtWebP}; !slices.Equal(got, want) {
		t.Errorf("FormatList(CharSprite) must stay zero-fallback, got %v want %v", got, want)
	}
}

func TestFormatListDeduplicatesPreservingOrder(t *testing.T) {
	p, _ := newTestPrefs(t)
	// User order already contains a legacy-chain member and a duplicate.
	p.SetFormatOrder(TypeCharSprite, []string{ExtPNG, ExtWebP, ExtPNG})
	p.SetTypeFallbacks(TypeCharSprite, true)

	got := p.FormatList(TypeCharSprite)
	want := []string{ExtPNG, ExtWebP, ExtAPNG, ExtGIF}
	if !slices.Equal(got, want) {
		t.Errorf("FormatList dedup = %v, want %v", got, want)
	}
}

func TestFormatListUnknownTypeWithFallbacksIsEmpty(t *testing.T) {
	p, _ := newTestPrefs(t)
	if got := p.FormatList("NoSuchType"); len(got) != 0 {
		t.Errorf("FormatList(unknown) = %v, want empty", got)
	}
}

func TestEveryTypeHasDefaultsAndLegacyChain(t *testing.T) {
	for _, name := range TypeNames {
		if len(DefaultFormatOrder(name)) == 0 {
			t.Errorf("type %s has no default format order", name)
		}
		if len(LegacyFallbackChain(name)) == 0 {
			t.Errorf("type %s has no legacy fallback chain", name)
		}
	}
	if len(defaultFormatOrders) != len(TypeNames) {
		t.Errorf("defaultFormatOrders has %d entries, want %d", len(defaultFormatOrders), len(TypeNames))
	}
	if len(legacyFallbackChains) != len(TypeNames) {
		t.Errorf("legacyFallbackChains has %d entries, want %d", len(legacyFallbackChains), len(TypeNames))
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}

	p.SetGlobalFallbacks(true)
	p.SetAnimationsEnabled(false)
	p.SetFormatOrder(TypeCharIcon, []string{ExtWebP, ExtPNG})
	p.SetTypeFallbacks(TypeBlip, true)
	p.RecordLearned("assets.example.com", TypeCharSprite, ExtWebP)
	p.SetPairOffsets(15, -20)
	p.SetPairFlipped(true)
	p.SetShowname("Nyah")
	p.SetDebugOverlay(true)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.GlobalFallbacksEnabled {
		t.Error("GlobalFallbacksEnabled lost")
	}
	if q.PreferAnimated {
		t.Error("PreferAnimated=false lost (absent-field default must not clobber explicit false)")
	}
	if got, want := q.FormatOrder(TypeCharIcon), []string{ExtWebP, ExtPNG}; !slices.Equal(got, want) {
		t.Errorf("FormatOrder(CharIcon) = %v, want %v", got, want)
	}
	if !q.TypeFallbacksEnabled(TypeBlip) {
		t.Error("TypeFallbacks(Blip) lost")
	}
	learned := q.LearnedSnapshot()
	if got := learned[LearnedKey("assets.example.com", TypeCharSprite)]; !slices.Equal(got, []string{ExtWebP}) {
		t.Errorf("learned format lost, snapshot=%v", learned)
	}
	x, y := q.PairOffsets()
	if x != 15 || y != -20 {
		t.Errorf("PairOffsets = (%d,%d), want (15,-20)", x, y)
	}
	if !q.PairFlipped() {
		t.Error("PairFlip lost")
	}
	if got := q.SavedShowname(); got != "Nyah" {
		t.Errorf("Showname = %q, want %q", got, "Nyah")
	}
	if !q.DebugOverlayEnabled() {
		t.Error("DebugOverlay lost")
	}
}

// TestLearnedExportImport pins the warm-state portability path: export →
// clear → import restores the table, and FormatAutoDetect defaults ON
// with an explicit false surviving the round trip.
func TestLearnedExportImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatal(err)
	}
	if !p.FormatAutoDetect() {
		t.Error("FormatAutoDetect should default ON")
	}
	p.SetFormatAutoDetect(false)
	p.RecordLearned("miku.pizza", TypeCharSprite, ExtWebP)
	p.RecordLearned("other.example", TypeCharIcon, ExtPNG)

	data, err := p.ExportLearnedJSON()
	if err != nil {
		t.Fatal(err)
	}
	p.ClearLearned()
	if len(p.LearnedSnapshot()) != 0 {
		t.Fatal("clear did not clear")
	}
	n, err := p.ImportLearnedJSON(data)
	if err != nil || n != 2 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if got := p.LearnedSnapshot()[LearnedKey("miku.pizza", TypeCharSprite)]; len(got) != 1 || got[0] != ExtWebP {
		t.Errorf("imported entry = %v", got)
	}
	if _, err := p.ImportLearnedJSON([]byte("junk")); err == nil {
		t.Error("junk import accepted")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if q.FormatAutoDetect() {
		t.Error("explicit FormatAutoDetect=false lost on reload")
	}
}

// TestExportSettingsRedactsPassword pins the security rule: the new-PC
// bundle carries everything EXCEPT saved passwords — the username and the
// auto-login choice survive (one retype restores the flow), the password
// never leaves the machine, and the redacted bundle still imports cleanly.
func TestExportSettingsRedactsPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatal(err)
	}
	const (
		key  = "wss://miku.pizza:2095"
		user = "nyah"
		pass = "hunter2-secret"
	)
	p.SetServerLogin(key, user, pass, true)

	dest := filepath.Join(dir, "bundle.json")
	if err := p.ExportSettings(dest); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pass) {
		t.Fatal("exported bundle still contains the plaintext password")
	}
	var snap AssetPreferences
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	w := snap.ServerWarm[key]
	if w.LoginPass != "" {
		t.Errorf("exported LoginPass = %q, want empty", w.LoginPass)
	}
	if w.LoginUser != user {
		t.Errorf("exported LoginUser = %q, want preserved %q", w.LoginUser, user)
	}
	if !w.AutoLogin {
		t.Error("AutoLogin choice should survive export (only the password is stripped)")
	}

	// The redacted bundle is still a loadable preferences file.
	path2 := filepath.Join(t.TempDir(), PrefsFileName)
	q, err := newWithDebounce(path2, testDebounce)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.ImportSettings(dest); err != nil {
		t.Fatalf("re-import of redacted bundle failed: %v", err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestWardrobeFolders pins wardrobe organization: folders are keyed by
// lowercased character, clear on empty, and a character leaving the wardrobe
// drops its folder too.
func TestWardrobeFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatal(err)
	}
	const key = "wss://s:1/"
	p.AddWardrobe(key, "Maya")
	p.AddWardrobe(key, "Phoenix")
	p.SetWardrobeFolder(key, "Maya", "Ace Attorney")
	p.SetWardrobeFolder(key, "phoenix", "Ace Attorney") // case-insensitive key
	if m := p.WardrobeFolderMap(key); m["maya"] != "Ace Attorney" || m["phoenix"] != "Ace Attorney" {
		t.Fatalf("folders = %v, want both filed under Ace Attorney", m)
	}
	p.SetWardrobeFolder(key, "Maya", "") // clear
	if p.WardrobeFolderMap(key)["maya"] != "" {
		t.Error("empty folder must clear the entry")
	}
	p.RemoveWardrobe(key, "Phoenix") // leaving the wardrobe drops the folder
	if p.WardrobeFolderMap(key)["phoenix"] != "" {
		t.Error("removing a wardrobe character must drop its folder")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestFavBackgrounds pins the per-server starred-background list: add with
// case-insensitive dedupe, isolation between servers, and remove.
func TestFavBackgrounds(t *testing.T) {
	p, _ := newTestPrefs(t)
	const a, b = "wss://a.example:1/", "wss://b.example:1/"
	if !p.AddFavBackground(a, "courtroom") {
		t.Fatal("first AddFavBackground must report a change")
	}
	if p.AddFavBackground(a, "Courtroom") { // case-insensitive dedupe
		t.Error("a case-insensitive duplicate must not report a change")
	}
	p.AddFavBackground(a, "lobby")
	p.AddFavBackground(b, "gaming") // a different server keeps its own list
	if got := p.FavBackgroundList(a); len(got) != 2 || got[0] != "courtroom" || got[1] != "lobby" {
		t.Fatalf("server a favorites = %v, want [courtroom lobby]", got)
	}
	if got := p.FavBackgroundList(b); len(got) != 1 || got[0] != "gaming" {
		t.Fatalf("server b favorites = %v, want [gaming] (per-server isolation)", got)
	}
	if !p.RemoveFavBackground(a, "COURTROOM") { // remove is case-insensitive
		t.Error("RemoveFavBackground must report a change")
	}
	if got := p.FavBackgroundList(a); len(got) != 1 || got[0] != "lobby" {
		t.Fatalf("after remove, server a = %v, want [lobby]", got)
	}
	if p.RemoveFavBackground(a, "not-there") {
		t.Error("removing an absent favorite must report no change")
	}
}

// TestFavBackgroundFolders pins the per-server background→folder map: file with
// a case-insensitive key, clear, and that unstarring drops the folder entry.
func TestFavBackgroundFolders(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://s:1/"
	p.AddFavBackground(key, "courtroom")
	p.AddFavBackground(key, "lobby")
	p.SetFavBackgroundFolder(key, "Courtroom", "Trials") // case-insensitive key
	p.SetFavBackgroundFolder(key, "lobby", "Trials")
	if m := p.FavBackgroundFolderMap(key); m["courtroom"] != "Trials" || m["lobby"] != "Trials" {
		t.Fatalf("folders = %v, want both filed under Trials", m)
	}
	p.SetFavBackgroundFolder(key, "courtroom", "") // clear
	if p.FavBackgroundFolderMap(key)["courtroom"] != "" {
		t.Error("empty folder must clear the entry")
	}
	p.RemoveFavBackground(key, "Lobby") // unstarring drops the folder
	if p.FavBackgroundFolderMap(key)["lobby"] != "" {
		t.Error("removing a favourite background must drop its folder")
	}
}

// TestDeleteWardrobeFolder pins whole-folder deletion: keepMembers ungroups
// (tags cleared, characters stay), !keepMembers removes the members entirely.
func TestDeleteWardrobeFolder(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://s:1/"
	p.AddWardrobe(key, "Maya")
	p.AddWardrobe(key, "Phoenix")
	p.AddWardrobe(key, "Edgeworth")
	p.SetWardrobeFolder(key, "Maya", "AA")
	p.SetWardrobeFolder(key, "Phoenix", "AA")

	p.DeleteWardrobeFolder(key, "AA", true) // ungroup
	if m := p.WardrobeFolderMap(key); len(m) != 0 {
		t.Fatalf("keepMembers must clear the folder tags, got %v", m)
	}
	if got := p.WardrobeList(key); len(got) != 3 {
		t.Fatalf("keepMembers must keep the characters, got %v", got)
	}

	p.SetWardrobeFolder(key, "Maya", "AA")
	p.SetWardrobeFolder(key, "Phoenix", "AA")
	p.DeleteWardrobeFolder(key, "aa", false) // delete + items, case-insensitive folder match
	if got := p.WardrobeList(key); len(got) != 1 || got[0] != "Edgeworth" {
		t.Fatalf("delete+items must remove the folder's members, got %v", got)
	}
	if len(p.WardrobeFolderMap(key)) != 0 {
		t.Error("delete must drop the folder tags too")
	}
}

// TestDeleteFavBackgroundFolder mirrors TestDeleteWardrobeFolder for backgrounds.
func TestDeleteFavBackgroundFolder(t *testing.T) {
	p, _ := newTestPrefs(t)
	const key = "wss://s:1/"
	p.AddFavBackground(key, "court")
	p.AddFavBackground(key, "lobby")
	p.AddFavBackground(key, "gaming")
	p.SetFavBackgroundFolder(key, "court", "Trials")
	p.SetFavBackgroundFolder(key, "lobby", "Trials")

	p.DeleteFavBackgroundFolder(key, "Trials", true) // ungroup
	if len(p.FavBackgroundFolderMap(key)) != 0 {
		t.Fatal("keepMembers must clear the folder tags")
	}
	if len(p.FavBackgroundList(key)) != 3 {
		t.Fatal("keepMembers must keep the favourites")
	}

	p.SetFavBackgroundFolder(key, "court", "Trials")
	p.SetFavBackgroundFolder(key, "lobby", "Trials")
	p.DeleteFavBackgroundFolder(key, "trials", false) // delete + items, case-insensitive
	if got := p.FavBackgroundList(key); len(got) != 1 || got[0] != "gaming" {
		t.Fatalf("delete+items must unstar the folder's members, got %v", got)
	}
}

// TestMultiTabCap pins the configurable tab cap: default, set, and clamp.
func TestMultiTabCap(t *testing.T) {
	p, _ := newTestPrefs(t)
	if p.TabCap() != DefaultMultiTabCap {
		t.Fatalf("default tab cap = %d, want %d", p.TabCap(), DefaultMultiTabCap)
	}
	p.SetTabCap(5)
	if p.TabCap() != 5 {
		t.Errorf("after SetTabCap(5), TabCap = %d", p.TabCap())
	}
	p.SetTabCap(99) // above the hard max
	if p.TabCap() != maxMultiTabCap {
		t.Errorf("SetTabCap(99) = %d, want clamp to %d", p.TabCap(), maxMultiTabCap)
	}
	p.SetTabCap(0) // below the min
	if p.TabCap() != minMultiTabCap {
		t.Errorf("SetTabCap(0) = %d, want clamp to %d", p.TabCap(), minMultiTabCap)
	}
}

func TestLoadDefaults(t *testing.T) {
	p, err := load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("load(absent) returned error: %v", err)
	}
	if p.GlobalFallbacksEnabled {
		t.Error("GlobalFallbacksEnabled default must be false (zero fallbacks by default)")
	}
	if !p.PreferAnimated {
		t.Error("PreferAnimated default must be true")
	}
	for _, name := range TypeNames {
		if _, ok := p.AssetTypes[name]; !ok {
			t.Errorf("default AssetTypes missing %s", name)
		}
	}
}

func TestLoadCorruptFileFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	if err := os.WriteFile(path, []byte("{not json"), prefsFilePerm); err != nil {
		t.Fatal(err)
	}
	p, err := load(path)
	if err == nil {
		t.Error("load(corrupt) must report the parse error")
	}
	if p == nil || !p.PreferAnimated || len(p.AssetTypes) != len(TypeNames) {
		t.Error("load(corrupt) must still return usable defaults")
	}
}

func TestSaveNowLeavesNoTempFiles(t *testing.T) {
	p, path := newTestPrefs(t)
	p.SetPairOffsets(5, 5)
	if err := p.SaveNow(); err != nil {
		t.Fatalf("SaveNow: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stray temp file left behind: %s", e.Name())
		}
	}
	// And the file must be valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("saved preferences are not valid JSON: %v", err)
	}
}

func TestCloseFlushesPendingChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	// Long debounce: Close must not wait for it, yet must still flush.
	p, err := newWithDebounce(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p.SetPairFlipped(true)

	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Minute {
		t.Fatalf("Close blocked for %v; must not wait out the debounce window", elapsed)
	}

	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.PairFlipped() {
		t.Error("pending change lost at Close")
	}
}

func TestLearnedInvalidation(t *testing.T) {
	p, _ := newTestPrefs(t)
	const host = "cdn.example.com"
	otherKey := LearnedKey(host, TypeBackground)

	seed := func() {
		p.RecordLearned(host, TypeCharSprite, ExtWebP)
		p.RecordLearned(host, TypeBackground, ExtWebP)
	}

	seed()
	p.SetFormatOrder(TypeCharSprite, []string{ExtAPNG})
	learned := p.LearnedSnapshot()
	if _, ok := learned[LearnedKey(host, TypeCharSprite)]; ok {
		t.Error("SetFormatOrder must invalidate learned formats for that type")
	}
	if _, ok := learned[otherKey]; !ok {
		t.Error("SetFormatOrder must not invalidate other types")
	}

	seed()
	p.SetTypeFallbacks(TypeCharSprite, true)
	learned = p.LearnedSnapshot()
	if _, ok := learned[LearnedKey(host, TypeCharSprite)]; ok {
		t.Error("SetTypeFallbacks must invalidate learned formats for that type")
	}

	seed()
	p.SetGlobalFallbacks(true)
	if n := len(p.LearnedSnapshot()); n != 0 {
		t.Errorf("SetGlobalFallbacks must invalidate all learned formats, %d left", n)
	}

	seed()
	p.ClearLearned()
	if n := len(p.LearnedSnapshot()); n != 0 {
		t.Errorf("ClearLearned left %d entries", n)
	}
}

func TestPairOffsetClamping(t *testing.T) {
	p, _ := newTestPrefs(t)
	p.SetPairOffsets(PairOffsetMax*3, PairOffsetMin*3)
	x, y := p.PairOffsets()
	if x != PairOffsetMax || y != PairOffsetMin {
		t.Errorf("PairOffsets = (%d,%d), want clamped (%d,%d)", x, y, PairOffsetMax, PairOffsetMin)
	}
}

// TestConcurrentAccess hammers every mutator and reader at once; run under
// -race this is the §17.8 "race-detector clean" gate for this package.
func TestConcurrentAccess(t *testing.T) {
	p, _ := newTestPrefs(t)

	const (
		goroutines = 8
		iterations = 200
	)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (seed + i) % 6 {
				case 0:
					p.SetFormatOrder(TypeCharSprite, []string{ExtWebP, ExtAPNG})
				case 1:
					p.RecordLearned("h.example.com", TypeNames[i%len(TypeNames)], ExtWebP)
				case 2:
					_ = p.FormatList(TypeNames[i%len(TypeNames)])
				case 3:
					_ = p.LearnedSnapshot()
				case 4:
					p.SetPairOffsets(i%PairOffsetMax, -i%PairOffsetMax)
				case 5:
					p.SetGlobalFallbacks(i%2 == 0)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := p.SaveNow(); err != nil {
		t.Fatalf("SaveNow after hammer: %v", err)
	}
}

func TestDefaultPathShape(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Skipf("no user config dir on this system: %v", err)
	}
	if filepath.Base(path) != PrefsFileName {
		t.Errorf("DefaultPath file = %s, want %s", filepath.Base(path), PrefsFileName)
	}
	if filepath.Base(filepath.Dir(path)) != PrefsDirName {
		t.Errorf("DefaultPath dir = %s, want %s", filepath.Base(filepath.Dir(path)), PrefsDirName)
	}
}

// TestLayoutAudioAndOOCNamePrefs pins the courtroom knobs: defaults match
// the original fixed layout, sets clamp, volume zero (mute) round-trips,
// and everything persists.
func TestLayoutAudioAndOOCNamePrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	vp, chat, box, logT, in := p.LayoutScales()
	if vp != DefaultViewportPercent || chat != DefaultScalePercent || box != DefaultScalePercent ||
		logT != DefaultScalePercent || in != DefaultScalePercent {
		t.Fatalf("defaults = %d/%d/%d/%d/%d", vp, chat, box, logT, in)
	}
	if m, s, b := p.AudioVolumes(); m != 100 || s != 100 || b != 100 {
		t.Fatalf("default volumes = %d/%d/%d, want 100s", m, s, b)
	}
	if crawl, stay, rate := p.Timing(); crawl != DefaultTextCrawlMs || stay != DefaultTextStayMs || rate != DefaultChatRateLimitMs {
		t.Fatalf("default timing = %d/%d/%d", crawl, stay, rate)
	}

	p.SetLayoutScales(10, 999, 10, 10, 999) // wildly out of range → clamped
	vp, chat, box, logT, in = p.LayoutScales()
	if vp != MinViewportPercent || chat != MaxChatScalePercent || box != MinChatBoxPercent ||
		logT != MinLogScalePercent || in != MaxInputPercent {
		t.Fatalf("clamped = %d/%d/%d/%d/%d", vp, chat, box, logT, in)
	}
	p.SetTiming(1, 99999, 99999) // crawl floors, stay/rate ceil
	if crawl, stay, rate := p.Timing(); crawl != MinTextCrawlMs || stay != MaxTextStayMs || rate != MaxChatRateLimitMs {
		t.Fatalf("clamped timing = %d/%d/%d", crawl, stay, rate)
	}
	p.SetMasterList("  https://alt.example/servers  ")
	if got := p.MasterList(); got != "https://alt.example/servers" {
		t.Fatalf("master list = %q", got)
	}

	p.SetAudioVolumes(0, 55, 200) // mute is a real value; 200 clamps
	p.SetOOCName("arbok")
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m, s, b := q.AudioVolumes(); m != 0 || s != 55 || b != 100 {
		t.Fatalf("reloaded volumes = %d/%d/%d, want 0/55/100 (0 must survive)", m, s, b)
	}
	if q.SavedOOCName() != "arbok" {
		t.Fatalf("reloaded OOC name = %q", q.SavedOOCName())
	}
	if v, _, _, _, _ := q.LayoutScales(); v != MinViewportPercent {
		t.Fatalf("reloaded viewport = %d", v)
	}
	if _, stay, _ := q.Timing(); stay != MaxTextStayMs {
		t.Fatalf("reloaded stay = %d", stay)
	}
	if got := q.MasterList(); got != "https://alt.example/servers" {
		t.Fatalf("reloaded master list = %q", got)
	}
}

// TestNameColorClamp pins the name-colour slider bounds: saturation clamps to
// 0..100 and brightness is floored at minNameColorVal so a name can't go dark.
func TestNameColorClamp(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.SetNameColorSatVal(200, 0) // saturation over the top, brightness under the floor
	if got := p.NameColorSat(); got != 100 {
		t.Errorf("saturation = %d, want 100 (clamped)", got)
	}
	if got := p.NameColorVal(); got != minNameColorVal {
		t.Errorf("brightness = %d, want %d (floor)", got, minNameColorVal)
	}
}

// TestResetSettings pins the settings-only reset: tunables revert to defaults
// while user content (callwords, favourites) is preserved.
func TestResetSettings(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	p.SetAudioVolumes(0, 10, 20)
	p.SetForceCharNames(true)
	p.SetTabCap(20)
	p.SetCallWords([]string{"myname"})
	p.AddFavorite("Home", "wss://home:2096", "")

	p.ResetSettings()

	if m, s, b := p.AudioVolumes(); m != 100 || s != 100 || b != 100 {
		t.Errorf("volumes not reset: %d/%d/%d", m, s, b)
	}
	if p.ForceCharNamesOn() {
		t.Error("ForceCharNames not reset")
	}
	if p.TabCap() != DefaultMultiTabCap {
		t.Errorf("TabCap = %d, want %d", p.TabCap(), DefaultMultiTabCap)
	}
	if got := p.CallWords(); len(got) != 1 || got[0] != "myname" {
		t.Errorf("callwords (content) must be preserved, got %v", got)
	}
	if len(p.FavoriteServers()) != 1 {
		t.Error("favourites (content) must be preserved")
	}
}

// TestCallWordAddRemove pins the callword manager helpers: Add splits a pasted
// "a, b, c" into separate words, lowercases + dedups (case-insensitive), reports
// the count actually added, and Remove drops one regardless of case.
func TestCallWordAddRemove(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if n := p.AddCallWord("Alice, bob , ALICE,  "); n != 2 {
		t.Fatalf("AddCallWord bulk = %d, want 2 (alice, bob; dup + blanks skipped)", n)
	}
	if got := p.CallWords(); len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("CallWords = %v, want [alice bob]", got)
	}
	if n := p.AddCallWord("bob"); n != 0 {
		t.Errorf("re-adding an existing word = %d, want 0", n)
	}
	if !p.RemoveCallWord("ALICE") { // case-insensitive remove
		t.Error("RemoveCallWord(ALICE) should drop the lowercased entry")
	}
	if got := p.CallWords(); len(got) != 1 || got[0] != "bob" {
		t.Errorf("after remove, CallWords = %v, want [bob]", got)
	}
}

// TestDNDPersistRoundTrip pins the OPTIONAL Do Not Disturb persistence: both
// flags default OFF (DND is session-only, clears each launch), and when the user
// opts in the saved state survives save→load.
func TestDNDPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if p.DNDPersistOn() || p.DNDSavedOn() {
		t.Fatal("DND persistence + state must default OFF (session-only)")
	}
	p.SetDNDPersist(true)
	p.SetDNDSaved(true)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.DNDPersistOn() || !q.DNDSavedOn() {
		t.Errorf("DND persist/state lost across reload: persist=%v saved=%v", q.DNDPersistOn(), q.DNDSavedOn())
	}
}

// TestAlertVolumeRoundTrip pins the callword/alert volume (separate from SFX):
// it defaults to full, clamps to 0–100, and survives save→load.
func TestAlertVolumeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	if p.AlertVolume() != defaultAudioVolume {
		t.Fatalf("AlertVolume default = %d, want %d", p.AlertVolume(), defaultAudioVolume)
	}
	p.SetAlertVolume(150) // over-max clamps to 100
	if p.AlertVolume() != 100 {
		t.Errorf("AlertVolume(150) = %d, want clamp to 100", p.AlertVolume())
	}
	p.SetAlertVolume(40)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if q.AlertVolume() != 40 {
		t.Errorf("AlertVolume lost across reload: got %d, want 40", q.AlertVolume())
	}
}

// TestResetAll pins the full wipe: tunables AND content both revert.
func TestResetAll(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	p.SetForceCharNames(true)
	p.SetCallWords([]string{"myname"})
	p.AddFavorite("Home", "wss://home:2096", "")

	p.ResetAll()

	if p.ForceCharNamesOn() {
		t.Error("ForceCharNames not reset")
	}
	if got := p.CallWords(); len(got) != 0 {
		t.Errorf("callwords must be wiped, got %v", got)
	}
	if got := p.FavoriteServers(); len(got) != 0 {
		t.Errorf("favourites must be wiped, got %d", len(got))
	}
}

// TestResetContentFieldsExist guards resetContentFields against a struct rename:
// every preserved name must be a real exported field of AssetPreferences.
func TestResetContentFieldsExist(t *testing.T) {
	tp := reflect.TypeOf(AssetPreferences{})
	for name := range resetContentFields {
		if f, ok := tp.FieldByName(name); !ok || f.PkgPath != "" {
			t.Errorf("resetContentFields[%q] is not an exported field of AssetPreferences", name)
		}
	}
}

// TestRestoreTabsRoundTrip pins the restore-on-launch prefs (M7): OFF + empty by
// default, and the toggle + remembered-tab list survive a save/reload.
func TestRestoreTabsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if p.RestoreTabsOn() {
		t.Error("RestoreTabs must default OFF")
	}
	if len(p.OpenTabList()) != 0 {
		t.Error("OpenTabs must default empty")
	}

	p.SetRestoreTabs(true)
	p.SetOpenTabs([]OpenTab{
		{Name: "Alpha", URL: "wss://a.example:2096"},
		{Name: "Beta", URL: "wss://b.example:2096"},
	})
	if err := p.SaveNow(); err != nil {
		t.Fatal(err)
	}

	q, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !q.RestoreTabsOn() {
		t.Error("reloaded RestoreTabs must be ON")
	}
	got := q.OpenTabList()
	if len(got) != 2 || got[0].Name != "Alpha" || got[0].URL != "wss://a.example:2096" || got[1].URL != "wss://b.example:2096" {
		t.Fatalf("reloaded OpenTabs = %v", got)
	}
}
