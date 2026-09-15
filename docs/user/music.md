# Music Features — AsyncAO

How fade effects, position sync, and custom loop points work in AsyncAO.

## Fade and Sync Effects

When you switch tracks, AsyncAO can apply smooth transitions instead of abruptly cutting the music. These effects are controlled by checkboxes in the music menu (right-click the track name in the top-right corner):

- **Fade in** — The new track starts at zero volume and ramps up over one second
- **Fade out previous** — The outgoing track ramps down over four seconds *while* the new track downloads, so there's no added delay
- **Synchronize position** — The new track starts at the same position the previous track was at (useful for alternate versions of the same song)
- **Loop** — The track repeats when it reaches the end

**Note:** Fade out and synchronize position only apply when you're *replacing* one track with another. Stopping music doesn't fade, and starting music when nothing is playing has nothing to sync from.

The default setting (checked when you first install) is **fade out previous**, matching Attorney Online 2's own default.

## Custom Loop Points

Some music tracks have custom loop points that let them loop seamlessly without restarting from the beginning. AsyncAO looks for two types of loop-point files:

### AO-style loop points (`.txt` sidecar)

If a track is `sounds/music/trial.opus`, AsyncAO will check for `sounds/music/trial.opus.txt` containing loop point data in **samples** (not seconds):

```
loop_start = 123456
loop_end = 654321
```

Or in seconds (add `seconds = true` before the loop lines):

```
seconds = true
loop_start = 5.5
loop_end = 30.2
```

You can also use `_sec` keys for unambiguous seconds:

```
loop_start_sec = 5.5
loop_end_sec = 30.2
```

Or add `s` or `sec` to the value:

```
loop_start = 5.5s
loop_end = 30.2sec
```

**Important:** The `seconds = true` flag is order-dependent — it only affects lines that come *after* it in the file. This matches Attorney Online 2's own behavior exactly.

### DRO-style loop points (folder `.ini`)

If tracks are organized in a folder like `sounds/music/trial/01.opus`, AsyncAO will check for `sounds/music/trial/trial.ini` containing:

```ini
[01.opus]
loop_start = 123456
loop_end = 654321
filename = 01.opus
```

Loop values are in samples. The `filename` key is case-insensitive matched against the track filename.

DRO metadata also supports `play_once = 1` to force a track to play once even when the server says to loop.

### How accurate are the loop points?

Loop points are checked roughly once per frame. On an idle client (no typing, no animations), frames run about every 500ms, so a loop can overshoot by up to half a second. While typing or during active courtroom animations, frames run much faster (~60 FPS), so overshoot is under 20ms.

To avoid loops that are too tight to hit reliably, AsyncAO refuses to arm any loop window narrower than one second. For sub-second precision loops (electronic music stabs, rhythm game tracks), export your track with seamless looping baked in using your DAW — don't rely on sidecar files.

### What if a track has no loop points?

Most tracks don't have loop points, and that's fine. Tracks marked to loop will restart from the beginning when they reach the end, exactly as they always have. Sidecar files are completely optional.

### What about tracks loaded from direct URLs?

Direct http(s) URLs (like Discord /play links) are never checked for loop points — they're off-domain redirects, not part of the server's asset folder. Only server-hosted tracks in `sounds/music/` can have loop points.

## Technical Notes

- **Sample rate sniffing:** AsyncAO reads the sample rate directly from your track's audio header (WAV, Ogg Vorbis, Ogg Opus, FLAC, or MP3). Opus files are always treated as 48000 Hz regardless of what the header says, per the Opus spec.
- **Seeking precision:** Loop-back seeks use sub-second precision (fractions of a second), so a loop point at 5.25 seconds will seek to exactly that position if the codec supports it. Ogg and MP3 support precise seeking; some formats (MOD, MIDI) don't and will degrade to restarting from the top.
- **Memory:** Loop point metadata is cached for 5 minutes after first fetch. A 10-second area loop won't spam the network — at most 30 fetches across that 5-minute window, and most of those will be 404s cached by the browser.
- **Missing sidecars:** If a `.txt` or `.ini` file doesn't exist, AsyncAO silently continues without loop points. There's no warning or error — missing sidecars are normal, not a problem.

## Limitations

- **Title metadata:** DRO `.ini` files can include a `title` field to override the displayed track name. AsyncAO doesn't use it yet — the Now Playing text always comes from the server's track name. This may be added in a future release.
- **SDL_mixer single stream:** AsyncAO can't do true overlapping crossfades like Attorney Online 2 (which uses BASS and can play two tracks at once). Fade out ramps the old track down, then stops it and starts the new one. Both tracks are never audible simultaneously.

## Troubleshooting

**Q: I set "fade out previous" but I don't hear any fade.**

A: Fade out only applies when *replacing* one track with another while music is already playing. If you start music when nothing is playing, there's no previous track to fade. Also check that your music volume isn't at zero — the fade is skipped at zero volume.

**Q: My loop points don't work.**

A: Make sure the sidecar file is in the same folder as the track, with `.txt` appended to the exact track URL. If the track is `https://example.com/sounds/music/trial.opus`, the sidecar must be at `https://example.com/sounds/music/trial.opus.txt` (note the `.txt` is added to the end, it doesn't replace `.opus`). Also verify the loop points are reasonable — the loop window must be at least 1 second wide.

**Q: The loop timing feels loose / overshoots.**

A: This is expected. Loop deadlines are checked once per frame, and frames run at different rates depending on what you're doing. While idle, frames are infrequent (~500ms gaps), so overshoots are noticeable. For tight loops, bake the loop into your audio file instead of using sidecar points.
