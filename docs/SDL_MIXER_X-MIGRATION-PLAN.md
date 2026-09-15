# SDL Mixer X Migration Plan

## Goal

Replace SDL_mixer with **SDL Mixer X** to achieve seamless, sample-accurate loop points like bass.dll in KFO-Client.

## Why SDL Mixer X?

SDL Mixer X is an extended fork of SDL_mixer specifically designed to address loop point limitations:
- https://github.com/WohlSoft/SDL-Mixer-X
- https://wohlsoft.github.io/SDL-Mixer-X/SDL_mixer_ext.html

### Key Features
1. **Native loop point parsing** from WAV, OGG, FLAC, OPUS metadata
2. **Sample-accurate looping** without audible cuts
3. **Mix-time callbacks** (equivalent to bass.dll's `BASS_SYNC_MIXTIME`)
4. **API compatibility** with SDL_mixer (mostly drop-in)
5. **Extended API** with `Mix_GetMusicLoopStartTime()`, `Mix_SetMusicLoopStartTime()`, etc.

## How bass.dll Achieved Seamless Looping (KFO-Client)

From the KFO-Client source code:

```cpp
// Parse .txt sidecar
loop_start[channel] = bytes_from_sample_count;
loop_end[channel] = bytes_from_sample_count;

// Set up sync callback that fires IN THE MIXER THREAD
loop_sync[channel] = BASS_ChannelSetSync(
    m_stream_list[channel], 
    BASS_SYNC_POS | BASS_SYNC_MIXTIME,  // ← MIXTIME is the key
    loop_end[channel], 
    loopProc,                           // callback
    &loop_start[channel]
);

// Callback fires inside the audio callback, not main loop
void CALLBACK loopProc(HSYNC handle, DWORD channel, DWORD data, void *user) {
    QWORD loop_start = *(static_cast<unsigned *>(user));
    BASS_ChannelLock(channel, true);
    BASS_ChannelSetPosition(channel, loop_start, BASS_POS_BYTE);
    BASS_ChannelLock(channel, false);
}
```

**The critical part**: `BASS_SYNC_MIXTIME` means the callback fires **inside the mixer thread**, checking the position on **every audio buffer fill**. This is sample-accurate and zero-latency.

SDL_mixer does not expose this - you can only poll from the main loop (~16ms granularity), which creates the audible cut.

## SDL Mixer X Equivalent

SDL Mixer X provides similar functionality:

### Option 1: Use Built-in Loop Metadata
SDL Mixer X can parse loop points from:
- OGG Vorbis comments (`LOOPSTART`, `LOOPEND`, `LOOPLENGTH`)
- FLAC tags
- WAV `smpl` chunk

If we embed loop points in the audio files themselves, SDL Mixer X handles them automatically.

### Option 2: Manual Loop Point API
```c
// Get current loop points
double start = Mix_GetMusicLoopStartTime(music);
double end = Mix_GetMusicLoopEndTime(music);

// Set custom loop points (in seconds)
Mix_SetMusicLoopStartTime(music, 3.19);
Mix_SetMusicLoopEndTime(music, 55.44);
```

SDL Mixer X handles the actual looping internally at the decoder level.

### Option 3: Position Callbacks (if available)
Check if SDL Mixer X exposes position-based callbacks similar to `BASS_SYNC_POS`.

## Migration Steps

### Phase 1: Evaluate SDL Mixer X
1. Download/build SDL Mixer X for Windows (MSYS2)
2. Create minimal test program:
   - Load an OGG with loop points in `.txt` sidecar
   - Parse the `.txt` file (same as current code)
   - Call `Mix_SetMusicLoopStartTime()` / `Mix_SetMusicLoopEndTime()`
   - Play and verify seamless looping
3. Document findings: Is there an audible cut? How accurate is the loop?

### Phase 2: AsyncAO Integration (if Phase 1 succeeds)
1. Update `go.mod` to use go-sdl2 bindings for SDL Mixer X (or create custom bindings)
2. Replace `internal/render/audio.go` SDL_mixer calls with SDL Mixer X equivalents
3. Update `armLoopFromCache()` to call `Mix_SetMusicLoopStartTime()` instead of deadline-based seek
4. Remove `advanceLoopBack()` (no longer needed - SDL Mixer X handles it)
5. Test with multiple tracks, verify no regressions

### Phase 3: Build System Updates
1. Update `scripts/setup-deps.ps1` to install SDL Mixer X instead of SDL_mixer
2. Update `scripts/build.ps1` to copy SDL Mixer X DLLs
3. Update CLAUDE.md with new dependency
4. Test clean build on fresh system

### Phase 4: Release
1. Test suite: verify loop points on local mode, streaming mode, network mode
2. Test with DRO `.ini` manifests (existing code path)
3. Update changelog
4. Merge to main and tag release

## Risks & Mitigations

### Risk 1: go-sdl2 Binding Gaps
**Mitigation**: SDL Mixer X is mostly API-compatible. We may need to use CGO directly for `Mix_SetMusicLoopStartTime()` if go-sdl2 doesn't expose it yet.

### Risk 2: Windows DLL Distribution
**Mitigation**: SDL Mixer X provides prebuilt Windows DLLs. Test with release builds.

### Risk 3: Cross-Platform Compatibility
**Mitigation**: Test on Windows first (user's primary platform). macOS/Linux testing can follow.

### Risk 4: Performance Regression
**Mitigation**: SDL Mixer X is based on SDL_mixer, so performance should be equivalent. Benchmark before/after.

## Fallback Plan

If SDL Mixer X doesn't work:
1. Consider SDL3 (audio subsystem rewrite)
2. Consider custom audio backend (dr_libs + manual loop logic)
3. Accept the SDL2 cut and document it as a known limitation

## Current Status

- Branch: `SDL_Mixer_X-test`
- Status: **Implemented & building** (pending independent gapless listening test)
- SDL Mixer X 2.8.0 built from source for MSYS2 UCRT64 (gcc 16.1.0, cmake+ninja),
  ZLib-licensed, with OGG/stb_vorbis, Opus, FLAC/dr_flac, MP3/dr_mp3, WAV, MIDI.
- **Key finding:** SDL Mixer X's loop API is *get-only* (`Mix_GetMusicLoopStartTime/EndTime`),
  sourced from embedded tags only — there is NO `Mix_SetMusicLoopStartTime` upstream.
  So the fork was patched to add `Mix_SetMusicLoopStartTime`/`Mix_SetMusicLoopEndTime`
  (runtime setters writing the codec's `loop_start`/`loop_end` fields the decode loop
  already reads), implemented for stb_vorbis, Opus, and dr_flac.
- AsyncAO integration: `internal/render/mixerx` cgo wrapper (drop-in replacement for
  go-sdl2's `mix`, plus the loop setters), `audio.go`/`musicclock.go`/tests swapped,
  and `applyLoopPoints` now sets native loop points instead of the old frame-polled
  `Mix_SetMusicPosition` seek. Full binary links `SDL2_mixer_ext.dll`.
- Next action: independent gapless listening test (KFO), then merge.

## References

- SDL Mixer X GitHub: https://github.com/WohlSoft/SDL-Mixer-X
- SDL Mixer X docs: https://wohlsoft.github.io/SDL-Mixer-X/SDL_mixer_ext.html
- Loop implementation guide: https://wohlsoft.ru/wiki/index.php?title=How_To:_Looping_music_files
- KFO-Client bass.dll implementation: (source code above)
