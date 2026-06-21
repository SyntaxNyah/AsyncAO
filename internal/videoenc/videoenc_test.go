package videoenc

import (
	"image"
	"os"
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
