package assets

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

func newTestPrefs(t *testing.T) *config.AssetPreferences {
	t.Helper()
	p, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestTypeNamesMatchConfig pins the enum ↔ config-name correspondence both
// ways, including declaration order.
func TestTypeNamesMatchConfig(t *testing.T) {
	if len(config.TypeNames) != int(AssetTypeCount) {
		t.Fatalf("config.TypeNames has %d entries, AssetTypeCount = %d", len(config.TypeNames), AssetTypeCount)
	}
	for i, name := range config.TypeNames {
		at := AssetType(i)
		if at.Name() != name {
			t.Errorf("AssetType(%d).Name() = %q, config.TypeNames[%d] = %q", i, at.Name(), i, name)
		}
		back, ok := TypeFromName(name)
		if !ok || back != at {
			t.Errorf("TypeFromName(%q) = %v,%v, want %v", name, back, ok, at)
		}
	}
}

func TestBuildCandidatesZeroFallbackDefaults(t *testing.T) {
	r := NewResolver(newTestPrefs(t))
	const host = "assets.example.com"
	const base = "http://assets.example.com/characters/phoenix/(a)normal"

	c := r.BuildCandidates(base, AssetTypeCharSprite, host)
	defer r.PutCandidates(c)
	if len(c.URLs) != 1 || c.URLs[0] != base+config.ExtWebP {
		t.Errorf("sprite candidates = %v, want exactly [%s]", c.URLs, base+config.ExtWebP)
	}
	if c.Learned {
		t.Error("fresh resolver claims learned")
	}

	icon := r.BuildCandidates(base, AssetTypeCharIcon, host)
	defer r.PutCandidates(icon)
	if len(icon.URLs) != 1 || icon.URLs[0] != base+config.ExtPNG {
		t.Errorf("icon candidates = %v, want exactly [%s] (PNG only, no webp fallback)", icon.URLs, base+config.ExtPNG)
	}
}

func TestBuildCandidatesLearnedSingleProbe(t *testing.T) {
	prefs := newTestPrefs(t)
	prefs.SetGlobalFallbacks(true) // 4-format list when unlearned
	r := NewResolver(prefs)
	const host = "assets.example.com"
	const base = "http://assets.example.com/characters/edgeworth/(b)normal"

	before := r.BuildCandidates(base, AssetTypeCharSprite, host)
	if len(before.URLs) != 4 {
		t.Fatalf("unlearned candidates = %v, want 4 (webp+legacy chain)", before.URLs)
	}
	r.PutCandidates(before)

	r.RecordSuccess(host, AssetTypeCharSprite, config.ExtGIF)
	after := r.BuildCandidates(base, AssetTypeCharSprite, host)
	defer r.PutCandidates(after)
	if len(after.URLs) != 1 || after.URLs[0] != base+config.ExtGIF || !after.Learned {
		t.Errorf("learned candidates = %+v, want single learned %s", after, base+config.ExtGIF)
	}
}

func TestLearnedIsPerHostAndPerType(t *testing.T) {
	r := NewResolver(newTestPrefs(t))
	r.RecordSuccess("host-a.example.com", AssetTypeCharSprite, config.ExtWebP)

	if _, ok := r.Learned("host-b.example.com", AssetTypeCharSprite); ok {
		t.Error("learned format leaked across hosts")
	}
	if _, ok := r.Learned("host-a.example.com", AssetTypeBackground); ok {
		t.Error("learned format leaked across types")
	}
	if ext, ok := r.Learned("host-a.example.com", AssetTypeCharSprite); !ok || ext != config.ExtWebP {
		t.Errorf("Learned = %q,%v", ext, ok)
	}
}

func TestInvalidate(t *testing.T) {
	r := NewResolver(newTestPrefs(t))
	const host = "h.example.com"
	r.RecordSuccess(host, AssetTypeCharSprite, config.ExtWebP)
	r.RecordSuccess(host, AssetTypeBackground, config.ExtWebP)

	r.Invalidate(host, AssetTypeCharSprite)
	if _, ok := r.Learned(host, AssetTypeCharSprite); ok {
		t.Error("Invalidate left the entry")
	}
	if _, ok := r.Learned(host, AssetTypeBackground); !ok {
		t.Error("Invalidate hit an unrelated type")
	}

	r.InvalidateAll()
	if _, ok := r.Learned(host, AssetTypeBackground); ok {
		t.Error("InvalidateAll left an entry")
	}
}

func TestWarmFromPrefsRoundTrip(t *testing.T) {
	prefs := newTestPrefs(t)
	r1 := NewResolver(prefs)
	r1.RecordSuccess("cdn.example.com:8080", AssetTypeMusic, config.ExtOpus)
	r1.RecordSuccess("cdn.example.com:8080", AssetTypeCharSprite, config.ExtWebP)

	// A second resolver over the same prefs (fresh session) must wake up
	// already knowing the formats.
	r2 := NewResolver(prefs)
	if ext, ok := r2.Learned("cdn.example.com:8080", AssetTypeMusic); !ok || ext != config.ExtOpus {
		t.Errorf("warm start lost music format: %q,%v", ext, ok)
	}
	if ext, ok := r2.Learned("cdn.example.com:8080", AssetTypeCharSprite); !ok || ext != config.ExtWebP {
		t.Errorf("warm start lost sprite format: %q,%v", ext, ok)
	}
}

// TestRecordSuccessCASRace is the §15 CAS race test: concurrent learns on
// distinct (host, type) pairs must all survive the copy-on-write loop.
func TestRecordSuccessCASRace(t *testing.T) {
	r := NewResolver(newTestPrefs(t))
	const hosts = 8
	var wg sync.WaitGroup
	for h := 0; h < hosts; h++ {
		for tt := AssetType(0); tt < AssetTypeCount; tt++ {
			wg.Add(1)
			go func(h int, tt AssetType) {
				defer wg.Done()
				r.RecordSuccess(fmt.Sprintf("host%d.example.com", h), tt, config.ExtWebP)
			}(h, tt)
		}
	}
	wg.Wait()

	for h := 0; h < hosts; h++ {
		for tt := AssetType(0); tt < AssetTypeCount; tt++ {
			if _, ok := r.Learned(fmt.Sprintf("host%d.example.com", h), tt); !ok {
				t.Fatalf("lost learn for host%d/%v in CAS race", h, tt)
			}
		}
	}
}

// TestBuildCandidatesLearnedAllocGate enforces the ≤1 alloc budget in plain
// `go test`, not just benchmarks (PROMPT.md §6).
func TestBuildCandidatesLearnedAllocGate(t *testing.T) {
	r := NewResolver(newTestPrefs(t))
	const host = "assets.example.com"
	const base = "http://assets.example.com/characters/maya/(a)normal"
	r.RecordSuccess(host, AssetTypeCharSprite, config.ExtWebP)

	allocs := testing.AllocsPerRun(2000, func() {
		c := r.BuildCandidates(base, AssetTypeCharSprite, host)
		r.PutCandidates(c)
	})
	if allocs > 1 {
		t.Errorf("learned BuildCandidates allocates %.1f objects/op, budget ≤ 1 (the URL string)", allocs)
	}
}

func TestSplitLearnedKey(t *testing.T) {
	host, typeName, ok := splitLearnedKey("cdn.example.com:8080" + config.LearnedKeySeparator + config.TypeCharSprite)
	if !ok || host != "cdn.example.com:8080" || typeName != config.TypeCharSprite {
		t.Errorf("splitLearnedKey = %q,%q,%v", host, typeName, ok)
	}
	if _, _, ok := splitLearnedKey("no-separator"); ok {
		t.Error("splitLearnedKey accepted a key without separator")
	}
}

// BenchmarkBuildCandidates_Learned is the §1 gate: < 100 ns, ≤ 1 alloc/op.
func BenchmarkBuildCandidates_Learned(b *testing.B) {
	prefs, err := config.New(filepath.Join(b.TempDir(), config.PrefsFileName))
	if err != nil {
		b.Fatal(err)
	}
	defer prefs.Close()
	r := NewResolver(prefs)
	const host = "assets.example.com"
	const base = "http://assets.example.com/characters/phoenix/(a)normal"
	r.RecordSuccess(host, AssetTypeCharSprite, config.ExtWebP)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := r.BuildCandidates(base, AssetTypeCharSprite, host)
		r.PutCandidates(c)
	}
}

// BenchmarkResolveAssets approximates a full char-select resolution pass
// (200 assets): must stay far under 1 ms (PROMPT.md §15).
func BenchmarkResolveAssets(b *testing.B) {
	prefs, err := config.New(filepath.Join(b.TempDir(), config.PrefsFileName))
	if err != nil {
		b.Fatal(err)
	}
	defer prefs.Close()
	r := NewResolver(prefs)
	const host = "assets.example.com"
	const assetCount = 200
	bases := make([]string, assetCount)
	for i := range bases {
		bases[i] = fmt.Sprintf("http://assets.example.com/characters/char%03d/char_icon", i)
	}
	r.RecordSuccess(host, AssetTypeCharIcon, config.ExtPNG)

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		for _, base := range bases {
			c := r.BuildCandidates(base, AssetTypeCharIcon, host)
			r.PutCandidates(c)
		}
	}
	if b.N > 0 {
		perPass := time.Since(start) / time.Duration(b.N)
		b.ReportMetric(float64(perPass.Nanoseconds()), "ns/200assets")
	}
}
