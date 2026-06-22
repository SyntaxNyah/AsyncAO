// Package videoenc is the MP4/WebM side of the scene exporter: it turns the
// captured RGBA frames into a real video file by streaming them into a SYSTEM
// ffmpeg over a stdin pipe (rawvideo → H.264/VP9).
//
// Unlike internal/webpenc this package is PURE GO — no CGO, no build-time
// dependency — because ffmpeg is invoked as an external process, not linked.
// That is deliberate and load-bearing: the app must boot and run fully WITHOUT
// ffmpeg installed (only the video-export action is then disabled), so the
// encoder can't be a linked library (user constraint: "it still boots even
// without em"). SDL-free and off the render hot path; the capture side that
// feeds it lives in the UI layer. It mirrors webpenc's method SURFACE
// (New / AddFrame / Frames / Finish / Close) plus Available()/FFmpegPath() so
// the UI can gate on a runtime ffmpeg.
package videoenc

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Format selects the container/codec pair.
type Format int

const (
	// FormatMP4 is H.264 in MP4 — plays in every browser, editor, and phone; the
	// safe default for "free content creation".
	FormatMP4 Format = iota
	// FormatWebM is VP9 in WebM — smaller and fully open, but slower to encode.
	FormatWebM
)

// FormatFromString maps a persisted preference string to a Format ("webm" →
// WebM, anything else → the MP4 default).
func FormatFromString(s string) Format {
	if strings.EqualFold(s, "webm") {
		return FormatWebM
	}
	return FormatMP4
}

// FormatExt is the output file extension (no dot) for a format.
func FormatExt(f Format) string {
	if f == FormatWebM {
		return "webm"
	}
	return "mp4"
}

// ffmpeg quality is a CRF (constant rate factor): LOWER = better + bigger. The UI
// exposes a friendly 0..100 "quality %" and we map it per-codec into a sane CRF
// window, so the one slider means the same thing for both formats. The windows
// are clamped well inside each codec's legal range so the slider never produces a
// uselessly soft or absurdly huge file.
const (
	// H.264 CRF window. 16 ≈ visually lossless, 32 ≈ "fine for a clip".
	x264CRFBest  = 16
	x264CRFWorst = 32
	// VP9 CRF window (VP9 CRFs run higher than x264 for a comparable look).
	vp9CRFBest  = 18
	vp9CRFWorst = 40
)

// crfFor maps a 0..100 quality (higher = better) to a codec CRF (lower = better).
func crfFor(format Format, quality int) int {
	if quality < 0 {
		quality = 0
	}
	if quality > 100 {
		quality = 100
	}
	best, worst := x264CRFBest, x264CRFWorst
	if format == FormatWebM {
		best, worst = vp9CRFBest, vp9CRFWorst
	}
	// quality 100 → best (low CRF); quality 0 → worst (high CRF).
	return worst - (worst-best)*quality/100
}

var (
	ffmpegOnce sync.Once
	ffmpegPath string
)

// FFmpegPath returns the resolved system ffmpeg executable path ("" if none).
// Looked up once and cached — a missing ffmpeg is the normal, supported state,
// not an error.
func FFmpegPath() string {
	ffmpegOnce.Do(func() {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpegPath = p
		}
	})
	return ffmpegPath
}

// Available reports whether a system ffmpeg was found on PATH. Callers gate the
// video-export action on this and degrade gracefully (the app still boots and
// runs fully without ffmpeg).
func Available() bool { return FFmpegPath() != "" }

// stderrTailBytes bounds how much of ffmpeg's stderr we keep for error reporting
// (the tail holds the real failure reason; we never need the whole log).
const stderrTailBytes = 600

// Encoder streams captured RGBA frames into a running ffmpeg process. Not safe
// for concurrent use — drive it from ONE goroutine (the render thread, during
// capture). Call Finish OFF the render thread: it closes stdin and waits for
// ffmpeg to flush the file, which can take a beat (mirrors webpenc's off-thread
// Assemble).
type Encoder struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stderr   *bytes.Buffer
	w, h     int
	rowBytes int
	frames   int
	writeErr error // sticky: a broken pipe (ffmpeg died) stops further writes
	done     bool
}

// New starts an ffmpeg that reads rawvideo (RGBA, w×h, fps) from stdin and writes
// an encoded video to outPath in the chosen format at the given quality (0..100).
// ffmpeg must exist — guard with Available first. The process runs until
// Finish/Close. Returns an error if ffmpeg can't be started.
func New(outPath string, w, h, fps, quality int, format Format) (*Encoder, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("videoenc: bad size %dx%d", w, h)
	}
	bin := FFmpegPath()
	if bin == "" {
		return nil, fmt.Errorf("videoenc: ffmpeg not found on PATH")
	}
	if fps < 1 {
		fps = 1
	}
	cmd := exec.Command(bin, ffmpegArgs(outPath, w, h, fps, quality, format)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr // Go drains this on its own goroutine — no full-pipe deadlock
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("videoenc: start ffmpeg: %w", err)
	}
	return &Encoder{cmd: cmd, stdin: stdin, stderr: &stderr, w: w, h: h, rowBytes: w * 4}, nil
}

// ffmpegArgs builds the encode command line. Input: rawvideo rgba from stdin —
// "rgba" matches the capture's image.RGBA byte order (R,G,B,A), so colours are
// correct. Output: H.264/yuv420p in MP4 (plays everywhere) or VP9/yuv420p in
// WebM. yuv420p on the OUTPUT is mandatory for broad playback (browsers/QuickTime
// reject yuv444). The encoder presets favour speed because the capture loop owns
// the window during export — a slower preset would just stutter the progress bar.
func ffmpegArgs(outPath string, w, h, fps, quality int, format Format) []string {
	size := strconv.Itoa(w) + "x" + strconv.Itoa(h)
	args := []string{
		"-y",                 // overwrite an existing file
		"-loglevel", "error", // keep stderr to real errors (still captured for reporting)
		"-f", "rawvideo",
		"-pix_fmt", "rgba", // input byte order = capture's image.RGBA (R,G,B,A)
		"-s", size,
		"-r", strconv.Itoa(fps),
		"-i", "-", // read frames from stdin
		"-an", // no audio in this pass (audio is muxed in a later phase)
	}
	crf := strconv.Itoa(crfFor(format, quality))
	switch format {
	case FormatWebM:
		args = append(args,
			"-c:v", "libvpx-vp9",
			"-b:v", "0", // CRF mode = constant quality
			"-crf", crf,
			"-deadline", "good", // a usable speed/quality balance (realtime is too soft)
			"-cpu-used", "5", // faster VP9 so the export stays responsive
			"-row-mt", "1", // multi-threaded rows
			"-pix_fmt", "yuv420p",
		)
	default: // FormatMP4 / H.264
		args = append(args,
			"-c:v", "libx264",
			"-preset", "veryfast", // fast encode; the export already owns the window
			"-crf", crf,
			"-pix_fmt", "yuv420p",
			"-movflags", "+faststart", // moov atom up front → web-streamable
		)
	}
	return append(args, outPath)
}

// MuxAudioBed remuxes an existing (silent) video with a single audio file as its
// soundtrack bed: it COPIES the video stream (no re-encode — fast) and encodes the
// audio to the container's codec. The audio is delayed by delayMs (when the song
// started in the scene) and padded with silence so the OUTPUT is exactly the
// video's length — apad makes the audio infinite, -shortest then trims it to the
// video, so a song shorter than the video can't truncate it and a longer one is cut
// to fit. ffmpeg decodes the source audio (any codec it supports) — there is no Go
// audio decoding (spec §8); ffmpeg is an external process. MUST run off the render
// thread (it blocks until ffmpeg exits). Guard with Available() first.
func MuxAudioBed(videoPath, audioPath, outPath string, delayMs int, format Format) error {
	bin := FFmpegPath()
	if bin == "" {
		return fmt.Errorf("videoenc: ffmpeg not found on PATH")
	}
	cmd := exec.Command(bin, muxArgs(videoPath, audioPath, outPath, delayMs, format)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		s := strings.TrimSpace(stderr.String())
		if len(s) > stderrTailBytes {
			s = s[len(s)-stderrTailBytes:]
		}
		if i := strings.LastIndexByte(s, '\n'); i >= 0 { // the cause is the last line
			s = strings.TrimSpace(s[i+1:])
		}
		if s != "" {
			return fmt.Errorf("ffmpeg mux: %s", s)
		}
		return fmt.Errorf("videoenc: mux failed: %w", err)
	}
	return nil
}

// muxArgs builds the audio-mux command line: copy the video stream, encode the
// audio (AAC for MP4, Opus for WebM — both standard for their container), optionally
// delay the audio to its scene start, pad it to the video length, and stop at the
// video's end. Pure so the argument shape is unit-tested.
func muxArgs(videoPath, audioPath, outPath string, delayMs int, format Format) []string {
	af := "apad" // pad the audio with trailing silence; -shortest trims to the video
	if delayMs > 0 {
		// adelay's all=1 delays every channel by delayMs regardless of channel count
		// (mono or stereo source), so the song lands where it started in the scene.
		af = "adelay=delays=" + strconv.Itoa(delayMs) + ":all=1,apad"
	}
	acodec := "aac"
	if format == FormatWebM {
		acodec = "libopus"
	}
	return []string{
		"-y",
		"-loglevel", "error",
		"-i", videoPath, // 0: the silent video
		"-i", audioPath, // 1: the song
		"-map", "0:v:0", "-c:v", "copy", // keep the encoded video as-is
		"-map", "1:a:0", "-c:a", acodec, // encode the song to the container's codec
		"-af", af,
		"-shortest", // output length = the video (apad keeps audio from cutting it short)
		outPath,
	}
}

// AddFrame writes one RGBA frame to ffmpeg's stdin. Synchronous, like webpenc's
// AddFrame: the OS pipe buffers the small frame and the export owns the window,
// so a brief stall while ffmpeg encodes only stutters the progress overlay, never
// the live client. A broken pipe (ffmpeg exited on bad input) is recorded as a
// sticky error and returned so the caller can stop and surface the reason.
func (e *Encoder) AddFrame(img *image.RGBA) error {
	if e.done {
		return fmt.Errorf("videoenc: encoder already finished")
	}
	if e.writeErr != nil {
		return e.writeErr
	}
	if img.Rect.Dx() != e.w || img.Rect.Dy() != e.h {
		return fmt.Errorf("videoenc: frame %dx%d != encoder %dx%d", img.Rect.Dx(), img.Rect.Dy(), e.w, e.h)
	}
	if len(img.Pix) == 0 {
		return fmt.Errorf("videoenc: empty frame")
	}
	// rawvideo wants tightly packed rows of exactly w*4 bytes. The capture's
	// stride is already w*4 (fast path: one write of the whole buffer); the
	// row-by-row branch keeps us correct if a stride-padded image is ever passed.
	if img.Stride == e.rowBytes {
		if _, err := e.stdin.Write(img.Pix); err != nil {
			e.writeErr = e.pipeErr(err)
			return e.writeErr
		}
	} else {
		for y := 0; y < e.h; y++ {
			off := y * img.Stride
			if _, err := e.stdin.Write(img.Pix[off : off+e.rowBytes]); err != nil {
				e.writeErr = e.pipeErr(err)
				return e.writeErr
			}
		}
	}
	e.frames++
	return nil
}

// pipeErr enriches a write error with the tail of ffmpeg's stderr (the real
// reason it died) — a raw "broken pipe" is useless to the user.
func (e *Encoder) pipeErr(err error) error {
	if tail := e.stderrTail(); tail != "" {
		return fmt.Errorf("ffmpeg failed: %s", tail)
	}
	return fmt.Errorf("videoenc: ffmpeg write failed: %w", err)
}

// Frames reports how many frames have been written.
func (e *Encoder) Frames() int { return e.frames }

// Finish closes ffmpeg's stdin (signalling end-of-stream) and waits for it to
// flush and write the file. MUST be called off the render thread — Wait blocks
// until ffmpeg exits. Returns an error (with the stderr tail) if ffmpeg failed,
// or if nothing was written.
func (e *Encoder) Finish() error {
	if e.done {
		return fmt.Errorf("videoenc: already finished")
	}
	e.done = true
	// Close stdin FIRST (EOF lets ffmpeg finalize the container), THEN Wait —
	// the reverse order would hang ffmpeg waiting for input.
	_ = e.stdin.Close()
	waitErr := e.cmd.Wait()
	if e.frames == 0 {
		return fmt.Errorf("videoenc: no frames written")
	}
	if waitErr != nil {
		if tail := e.stderrTail(); tail != "" {
			return fmt.Errorf("ffmpeg: %s", tail)
		}
		return waitErr
	}
	return e.writeErr
}

// Close kills ffmpeg if it's still running (the abort path) and reaps it. Safe to
// call after Finish (no-op) and more than once.
func (e *Encoder) Close() {
	if e.done {
		return
	}
	e.done = true
	_ = e.stdin.Close()
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	_ = e.cmd.Wait()
}

// stderrTail returns the trimmed tail of ffmpeg's captured stderr.
func (e *Encoder) stderrTail() string {
	if e.stderr == nil {
		return ""
	}
	s := strings.TrimSpace(e.stderr.String())
	if len(s) > stderrTailBytes {
		s = s[len(s)-stderrTailBytes:]
	}
	// Collapse to the last non-empty line — that's where ffmpeg prints the cause.
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		if last := strings.TrimSpace(s[i+1:]); last != "" {
			return last
		}
		return strings.TrimSpace(s[:i])
	}
	return s
}
