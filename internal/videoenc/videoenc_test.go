package videoenc

import (
	"bytes"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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

// TestEncodeRealVideo is the load-bearing end-to-end proof: stream a few RGBA
// frames through a real ffmpeg and confirm a non-empty video lands on disk. It
// SKIPS (not fails) when ffmpeg isn't installed, so CI without ffmpeg stays green
// while the dev box (ffmpeg present) actually exercises the pipe.
func TestEncodeRealVideo(t *testing.T) {
	if !Available() {
		t.Skip("ffmpeg not on PATH — video encode path can't be exercised here")
	}
	const w, h = 64, 48
	for _, f := range []Format{FormatMP4, FormatWebM} {
		f := f
		t.Run(FormatExt(f), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "clip."+FormatExt(f))
			enc, err := New(out, w, h, 10, 70, f)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			// A handful of solid-colour frames (changing colour each frame).
			for i := 0; i < 6; i++ {
				img := image.NewRGBA(image.Rect(0, 0, w, h))
				for p := 0; p+3 < len(img.Pix); p += 4 {
					img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = byte(i*40), 80, 200, 255
				}
				if err := enc.AddFrame(img); err != nil {
					enc.Close()
					t.Fatalf("AddFrame %d: %v", i, err)
				}
			}
			if enc.Frames() != 6 {
				t.Errorf("Frames() = %d, want 6", enc.Frames())
			}
			if err := enc.Finish(); err != nil {
				t.Fatalf("Finish: %v", err)
			}
			st, err := os.Stat(out)
			if err != nil {
				t.Fatalf("stat output: %v", err)
			}
			if st.Size() == 0 {
				t.Errorf("encoded %s is empty", FormatExt(f))
			}
		})
	}
}

// TestMuxArgsShape pins the audio-mux command line: copy the video, encode the
// audio to the container codec, delay+pad it, stop at the video length.
func TestMuxArgsShape(t *testing.T) {
	mp4 := strings.Join(muxArgs("v.mp4", "a.wav", "out.mp4", 0, FormatMP4), " ")
	for _, want := range []string{"-i v.mp4", "-i a.wav", "-map 0:v:0", "-c:v copy", "-map 1:a:0", "-c:a aac", "-af apad", "-shortest"} {
		if !strings.Contains(mp4, want) {
			t.Errorf("mp4 mux args missing %q in %q", want, mp4)
		}
	}
	if !strings.HasSuffix(mp4, "out.mp4") {
		t.Errorf("mux args must end with the output path: %q", mp4)
	}
	// A start delay inserts an adelay filter before apad.
	delayed := strings.Join(muxArgs("v.mp4", "a.wav", "o.mp4", 1500, FormatMP4), " ")
	if !strings.Contains(delayed, "adelay=delays=1500:all=1,apad") {
		t.Errorf("delayed mux args missing the adelay filter: %q", delayed)
	}
	// WebM uses Opus, not AAC.
	webm := strings.Join(muxArgs("v.webm", "a.wav", "o.webm", 0, FormatWebM), " ")
	if !strings.Contains(webm, "-c:a libopus") {
		t.Errorf("webm mux args missing libopus: %q", webm)
	}
}

// minimalWAV writes a tiny 16-bit mono PCM WAV (≈0.5 s of a quiet tone) so the mux
// test has a REAL audio file without any Go audio decoding.
func minimalWAV(t *testing.T, path string) {
	t.Helper()
	const rate, samples = 8000, 4000 // 0.5 s
	pcm := make([]byte, 0, samples*2)
	for i := 0; i < samples; i++ {
		v := int16(3000 * math.Sin(2*math.Pi*440*float64(i)/rate))
		pcm = append(pcm, byte(v), byte(v>>8))
	}
	var b []byte
	put4 := func(s string) { b = append(b, s...) }
	put32 := func(v int) { b = append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
	put16 := func(v int) { b = append(b, byte(v), byte(v>>8)) }
	put4("RIFF")
	put32(36 + len(pcm))
	put4("WAVE")
	put4("fmt ")
	put32(16)
	put16(1)
	put16(1)
	put32(rate)
	put32(rate * 2)
	put16(2)
	put16(16)
	put4("data")
	put32(len(pcm))
	b = append(b, pcm...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

// TestMuxAudioBedReal is the end-to-end proof: encode a real silent clip, mux a real
// WAV in, and confirm the output actually carries an audio stream. Skips without
// ffmpeg (CI stays green; the dev box exercises it).
func TestMuxAudioBedReal(t *testing.T) {
	if !Available() {
		t.Skip("ffmpeg not on PATH — mux path can't be exercised here")
	}
	const w, h = 64, 48
	dir := t.TempDir()
	silent := filepath.Join(dir, "silent.mp4")
	enc, err := New(silent, w, h, 10, 70, FormatMP4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 10; i++ { // ~1 s of video
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for p := 0; p+3 < len(img.Pix); p += 4 {
			img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 120, byte(i*20), 60, 255
		}
		if err := enc.AddFrame(img); err != nil {
			enc.Close()
			t.Fatalf("AddFrame: %v", err)
		}
	}
	if err := enc.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wav := filepath.Join(dir, "song.wav")
	minimalWAV(t, wav)
	out := filepath.Join(dir, "withaudio.mp4")
	if err := MuxAudioBed(silent, wav, out, 0, FormatMP4); err != nil {
		t.Fatalf("MuxAudioBed: %v", err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("muxed output missing/empty: %v", err)
	}
	// Confirm an audio stream is present (ffmpeg -i prints stream info to stderr and
	// exits non-zero with no output file — that's expected for a probe).
	probe := exec.Command(FFmpegPath(), "-i", out)
	var pst bytes.Buffer
	probe.Stderr = &pst
	_ = probe.Run()
	if !strings.Contains(pst.String(), "Audio:") {
		t.Errorf("muxed file has no audio stream; ffmpeg -i said:\n%s", pst.String())
	}
}

// TestSizeMismatchRejected confirms a wrong-sized frame is rejected, not piped to
// ffmpeg as corrupt rawvideo.
func TestSizeMismatchRejected(t *testing.T) {
	if !Available() {
		t.Skip("ffmpeg not on PATH")
	}
	out := filepath.Join(t.TempDir(), "clip.mp4")
	enc, err := New(out, 64, 48, 10, 70, FormatMP4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enc.Close()
	bad := image.NewRGBA(image.Rect(0, 0, 32, 32))
	if err := enc.AddFrame(bad); err == nil {
		t.Error("AddFrame accepted a 32x32 frame for a 64x48 encoder")
	}
}
