package videoenc

import (
	"strings"
	"testing"
)

// TestCRFMapsQualityToWindow pins the quality→CRF mapping: higher quality must
// mean a LOWER crf (better/bigger), the result must stay inside each codec's
// window, and the endpoints must hit the window bounds — so the one "quality %"
// slider behaves sensibly for both formats.
func TestCRFMapsQualityToWindow(t *testing.T) {
	for _, f := range []Format{FormatMP4, FormatWebM} {
		best, worst := x264CRFBest, x264CRFWorst
		if f == FormatWebM {
			best, worst = vp9CRFBest, vp9CRFWorst
		}
		if got := crfFor(f, 100); got != best {
			t.Errorf("format %d quality 100 → crf %d, want best %d", f, got, best)
		}
		if got := crfFor(f, 0); got != worst {
			t.Errorf("format %d quality 0 → crf %d, want worst %d", f, got, worst)
		}
		// Monotonic: more quality never raises the CRF.
		prev := worst + 1
		for q := 0; q <= 100; q += 5 {
			c := crfFor(f, q)
			if c > prev {
				t.Errorf("format %d crf not monotonic: q=%d crf=%d > prev %d", f, q, c, prev)
			}
			if c < best || c > worst {
				t.Errorf("format %d q=%d crf=%d outside window [%d,%d]", f, q, c, best, worst)
			}
			prev = c
		}
		// Out-of-range quality clamps, never panics.
		if c := crfFor(f, 999); c != best {
			t.Errorf("format %d q=999 → crf %d, want best %d (clamped)", f, c, best)
		}
		if c := crfFor(f, -50); c != worst {
			t.Errorf("format %d q=-50 → crf %d, want worst %d (clamped)", f, c, worst)
		}
	}
}

func TestFormatFromStringAndExt(t *testing.T) {
	cases := []struct {
		in   string
		want Format
		ext  string
	}{
		{"mp4", FormatMP4, "mp4"},
		{"webm", FormatWebM, "webm"},
		{"WEBM", FormatWebM, "webm"}, // case-insensitive
		{"", FormatMP4, "mp4"},       // empty → default MP4
		{"avi", FormatMP4, "mp4"},    // unknown → default MP4
	}
	for _, c := range cases {
		if got := FormatFromString(c.in); got != c.want {
			t.Errorf("FormatFromString(%q) = %d, want %d", c.in, got, c.want)
		}
		if got := FormatExt(c.want); got != c.ext {
			t.Errorf("FormatExt(%d) = %q, want %q", c.want, got, c.ext)
		}
	}
}

// TestFFmpegArgsShape pins the command line: rawvideo+rgba on the INPUT (matches
// the capture byte order), yuv420p on the OUTPUT (broad playback), the right
// codec per format, and the output path last.
func TestFFmpegArgsShape(t *testing.T) {
	mp4 := strings.Join(ffmpegArgs("out.mp4", 480, 360, 12, 80, FormatMP4), " ")
	for _, want := range []string{"-f rawvideo", "-pix_fmt rgba", "-s 480x360", "-r 12", "-i -", "libx264", "-pix_fmt yuv420p"} {
		if !strings.Contains(mp4, want) {
			t.Errorf("mp4 args missing %q in %q", want, mp4)
		}
	}
	if !strings.HasSuffix(mp4, "out.mp4") {
		t.Errorf("mp4 args must end with the output path, got %q", mp4)
	}
	webm := strings.Join(ffmpegArgs("out.webm", 320, 240, 24, 50, FormatWebM), " ")
	if !strings.Contains(webm, "libvpx-vp9") {
		t.Errorf("webm args missing the VP9 codec: %q", webm)
	}
	if !strings.Contains(webm, "-pix_fmt yuv420p") {
		t.Errorf("webm args missing yuv420p output: %q", webm)
	}
}

// TestAvailableConsistent just checks Available() agrees with FFmpegPath() and
// neither panics — a missing ffmpeg is a supported state, so this never fails.
func TestAvailableConsistent(t *testing.T) {
	if Available() != (FFmpegPath() != "") {
		t.Errorf("Available()=%v disagrees with FFmpegPath()=%q", Available(), FFmpegPath())
	}
}

// TestMuxArgsShape pins the audio-mux command line: copy the video, encode the
// audio to the container codec, delay+pad it, stop at the video length. The pad is
// BOUNDED to the probed video duration (whole_dur) — an infinite apad races the mux
// sync queue under load; plain apad remains only for the probe-failure fallback.
func TestMuxArgsShape(t *testing.T) {
	mp4 := strings.Join(muxArgs("v.mp4", "a.wav", "out.mp4", 0, FormatMP4, 1.0), " ")
	for _, want := range []string{"-i v.mp4", "-i a.wav", "-map 0:v:0", "-c:v copy", "-map 1:a:0", "-c:a aac", "-af apad=whole_dur=1.000", "-shortest"} {
		if !strings.Contains(mp4, want) {
			t.Errorf("mp4 mux args missing %q in %q", want, mp4)
		}
	}
	if !strings.HasSuffix(mp4, "out.mp4") {
		t.Errorf("mux args must end with the output path: %q", mp4)
	}
	// A start delay inserts an adelay filter before apad.
	delayed := strings.Join(muxArgs("v.mp4", "a.wav", "o.mp4", 1500, FormatMP4, 1.0), " ")
	if !strings.Contains(delayed, "adelay=delays=1500:all=1,apad=whole_dur=1.000") {
		t.Errorf("delayed mux args missing the adelay filter: %q", delayed)
	}
	// WebM uses Opus, not AAC.
	webm := strings.Join(muxArgs("v.webm", "a.wav", "o.webm", 0, FormatWebM, 1.0), " ")
	if !strings.Contains(webm, "-c:a libopus") {
		t.Errorf("webm mux args missing libopus: %q", webm)
	}
	// Probe failure (padDur 0): the unbounded-apad fallback.
	fb := strings.Join(muxArgs("v.mp4", "a.wav", "o.mp4", 0, FormatMP4, 0), " ")
	if !strings.Contains(fb, "-af apad -shortest") {
		t.Errorf("fallback mux args must keep the plain apad: %q", fb)
	}
}

// TestLastStderrLines pins the two-line error surfacing: the real cause often sits
// one line above ffmpeg's generic "Terminating thread" line, which alone reads like
// a disk problem.
func TestLastStderrLines(t *testing.T) {
	in := "noise\nToo many packets buffered for output stream 0:1\n[fc#0] Terminating thread with return code -28 (No space left on device)\n"
	got := lastStderrLines(in)
	want := "Too many packets buffered for output stream 0:1 | [fc#0] Terminating thread with return code -28 (No space left on device)"
	if got != want {
		t.Errorf("lastStderrLines = %q, want %q", got, want)
	}
	if got := lastStderrLines("single line"); got != "single line" {
		t.Errorf("single-line dump = %q", got)
	}
	if got := lastStderrLines("  \n\n"); got != "" {
		t.Errorf("blank dump = %q, want empty", got)
	}
}

// TestMuxMixArgsShape pins the multi-input filter graph: one adelay per clip, the
// summed full-volume amix, the POST-mix apad (not per-input), and the mapped output.
func TestMuxMixArgsShape(t *testing.T) {
	// A trimmed music segment (ends at 30 s when the next song starts) + an untrimmed
	// one-shot SFX.
	clips := []AudioClip{{Path: "song.opus", DelayMs: 0, TrimMs: 30000}, {Path: "obj.wav", DelayMs: 1500}}
	got := strings.Join(muxMixArgs("v.mp4", clips, "out.mp4", FormatMP4, 60.5), " ")
	for _, want := range []string{
		"-i v.mp4", "-i song.opus", "-i obj.wav",
		"[1:a]atrim=end=30.000,adelay=delays=0:all=1[a0]",               // trimmed segment
		"[2:a]adelay=delays=1500:all=1[a1]",                             // untrimmed SFX (no atrim)
		"[a0][a1]amix=inputs=2:normalize=0,apad=whole_dur=60.500[aout]", // pad bounded to the video
		"-map 0:v:0", "-c:v copy", "-map [aout]", "-c:a aac", "-shortest",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mix args missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "all=1,apad") { // apad must NOT be per-input
		t.Errorf("apad must follow amix, never each input: %q", got)
	}
	// Probe failure: the mix keeps the unbounded-apad fallback.
	fb := strings.Join(muxMixArgs("v.mp4", clips, "out.mp4", FormatMP4, 0), " ")
	if !strings.Contains(fb, "normalize=0,apad[aout]") {
		t.Errorf("fallback mix args must keep the plain apad: %q", fb)
	}
}
