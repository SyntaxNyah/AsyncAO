# AsyncAO refactor — handoff

## Gate acknowledgement (GATE RULE)
`e07058e` ("config: make load strict") was executed against standing orders (the
strict-load rule was gated on an explicit decision). It is kept PROVISIONALLY and was
approved ("strict yes") on 2026-10-19. Revert if ever needed: `git revert e07058e` plus
its golden re-freeze, one commit.

## Item 2 — sentinel ("0 → default") getters
The 28 getters that resolve a zero-value sentinel to a default (FPSCap, SpotlightLevel,
ChromeTheme, BreathAmp, PreviewHoverMillis, TimerSecondsValue, …) were moved off
`*AssetPreferences` onto `*Settings` as pure methods, all sharing the single
`orDefault[T comparable](v, def T)` helper (`internal/config/settings_default.go`).

Rationale: the sentinel 0 means "unset — follow the current default", so it MUST resolve
on read, never at load. Resolving it at load (strict or not) would pin today's default
into every user's file on the next Save and stop a future default change from ever
reaching existing users.

Two pure-clamp getters whose clamps are already in `clampSettingsStrict` were deleted:
`SpriteScalingMode()` → `Load().SpriteScaling`, `DownloadCapKBps()` → `Load().DownloadKBps`.

Six clamp-on-read getters REMAIN on `*AssetPreferences` because their clamps are NOT in
`clampSettingsStrict`: EmoteGridGap, WeatherType, StageFrame, ChromeShape, ChromeShapeTier,
TexCompression. Deleting them needs a `clampSettingsStrict` extension first — a further
[strict] decision (it re-freezes goldens, which needs the user's word).

## Real-prefs hash comparison (the exact commands)
```powershell
$P="$env:TEMP\realprefs"; mkdir $P -Force | Out-Null
Copy-Item "<PATH-TO-YOUR-PREFS>" "$P\prefs.json"        # a COPY, never the live file
$repo="<PATH-TO-REPO>"; $g="<PATH-TO-GATE-SCRATCH>"   # the repo checkout + the scratch dir holding diffbridge.go/prefsdump_main.go
$env:PATH="<MSYS2-BIN>;"+$env:PATH; $env:CGO_ENABLED="1"; $env:CC="<MSYS2-BIN>\gcc.exe"; $env:CGO_CFLAGS="-I<MSYS2-INCLUDE>"; $env:CGO_LDFLAGS="-L<MSYS2-LIB>"

# 1. TIP — in-package dump (marshals the Settings snapshot via p.Load()):
cd $repo; $env:REAL_PREFS="$P\prefs.json"
go test ./internal/config -run TestRealPrefsDump -v 2>&1 | Tee-Object "$P\tip.txt"

# 2. bb9f414 — the in-package dump test does NOT compile there (no Settings/Load());
#    use the untracked diffbridge hook instead (same section hashes, struct marshal):
git worktree add "$P\bb9f414" bb9f414
Copy-Item "$g\diffbridge.go" "$P\bb9f414\internal\config\diffbridge.go" -Force
mkdir "$P\bb9f414\prefsdump" -Force | Out-Null
Copy-Item "$g\prefsdump_main.go" "$P\bb9f414\prefsdump\main.go" -Force
cd "$P\bb9f414"; go build -o "$P\base.exe" ./prefsdump; & "$P\base.exe" "$P\prefs.json" > "$P\base.txt"

# 3. diff SECTION HASHES ONLY — errclass / unexported / dirty / save / import_valid / import_garbage.
#    "exported" differs BY DESIGN: tip = json.Marshal(p.Load()) (Settings), bb9f414 = json.Marshal(p) (inline struct).
```

## Benchmark reconciliation (item 1)
`Update` is byte-identical at d479a69, 250d413 and tip. `BenchmarkSetSmoothScalingDrag`
measures ~2.2–3.7 µs / 4008 B / 18 allocs at ALL three (idle machine, -count=5). The
"1.96 µs" (flip report) was the warm-run low; the "63,700 ns" (last two reports) was a
~20× transcription error — the real figure is ~2.7–3.3 µs. No regression; no fix needed.

## Priority 2 (App decomposition) targets
- env minimal; components embedded as staging, NAMED fields as the end state
- reachable fields <= 100
- components <= 60 fields and <= 2 shared dependencies each
- declaration-hash tool: `decls.go` (receiver-normalised; a pure receiver move hashes identical)
