# SDL2 Loop Points Implementation - Known Limitations

## Summary

This branch implements loop point support for `.ogg.txt` sidecar files using SDL2's `Mix_SetMusicPosition()`. **The implementation works functionally but produces an audible "cut" at the loop point**, making it unsuitable for production use.

## What Works

- ✅ Parsing `.ogg.txt` sidecar files with `loop_start` and `loop_end` sample counts
- ✅ Converting samples to seconds using sniffed sample rates
- ✅ Reading sidecar files in local-only mode
- ✅ Arming loop points on first playthrough (synchronous fetch)
- ✅ Seeking to loop_start when playback reaches loop_end
- ✅ Repeating the loop indefinitely

## The Problem: Audible Cut

When the loop fires, there's a noticeable audio gap/cut because:

1. **Frame-based polling**: The main loop checks every ~16ms (at 60fps) whether the loop deadline has arrived
2. **Software seek latency**: `Mix_SetMusicPosition()` must:
   - Stop the current decode stream
   - Reposition to the new sample offset
   - Restart decoding and playback
3. **Timing inaccuracy**: The deadline is checked on the render thread, not in the audio callback, so there's inherent jitter

This creates a perceptible interruption that sounds bad, especially for music designed to loop seamlessly.

## Why bass.dll Worked (KFO-Client)

bass.dll handles loop points at the **decoder level** inside the audio callback:

- Loop points are set in **exact sample positions**
- The decoder checks the position **every audio frame** (not every render frame)
- When `loop_end` is reached, it seamlessly jumps to `loop_start` **without stopping playback**
- Zero-latency, sample-accurate looping

SDL_mixer (SDL2) does **not** expose this level of control. It only supports:
- Full-track looping (loop forever from 0 to end)
- Manual seeking via `Mix_SetMusicPosition()` (which stops/restarts)

## Solutions

### Option 1: SDL Mixer X (RECOMMENDED)

**SDL Mixer X** is an extended fork of SDL_mixer that natively supports loop points:
- https://github.com/WohlSoft/SDL-Mixer-X
- https://wohlsoft.github.io/SDL-Mixer-X/SDL_mixer_ext.html

Features:
- Parses loop metadata from WAV, OGG, FLAC, OPUS headers
- API functions like `Mix_GetMusicLoopStartTime()` and `Mix_SetMusicLoopStartTime()`
- Sample-accurate looping without audible cuts
- Drop-in replacement for SDL_mixer

**This is the best path forward** - it gives you BASS.dll-like behavior without proprietary licensing.

### Option 2: SDL3

SDL3's audio subsystem has been completely rewritten with better stream control. It may offer better loop point support, but:
- SDL3 is still in development
- Migration effort is significant
- Loop point support needs verification

### Option 3: Custom Audio Backend

Implement a custom decoder using:
- **libopenmpt** (for module formats with native loop support)
- **dr_libs** (single-header WAV/FLAC/MP3 decoders) + custom loop logic
- **stb_vorbis** for OGG with manual loop handling

This gives full control but requires significant engineering effort.

### Option 4: Accept the Cut (NOT RECOMMENDED)

Document the limitation and ship it as-is. Users will hear the cut on every loop.

## Current State

- Branch: `looping-points-with-sdl2-suck`
- Status: **Proof of concept - DO NOT RELEASE**
- All code is functional and tested
- Debug logging included to trace loop arming and firing

## Next Steps

1. Evaluate **SDL Mixer X** as a drop-in replacement
2. Test loop point behavior with SDL Mixer X
3. If satisfactory, migrate to SDL Mixer X and merge to main
4. If not, investigate SDL3 or custom decoder options

## References

- SDL_mixer loop limitations: https://github.com/libsdl-org/SDL_mixer/issues/117
- SDL Mixer X documentation: https://wohlsoft.github.io/SDL-Mixer-X/SDL_mixer_ext.html
- How to loop music files with SDL Mixer X: https://wohlsoft.ru/wiki/index.php?title=How_To:_Looping_music_files
