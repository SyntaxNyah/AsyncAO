// Package videoenc streams captured RGBA frames into a system ffmpeg over a stdin pipe to make MP4 or WebM files.
// It is pure Go on purpose: the app must boot and run without ffmpeg, so the encoder can't be linked.
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

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// Format selects the container/codec pair.
type Format int

const (
	// FormatMP4 is H.264 in MP4, which plays everywhere; the default.
	FormatMP4 Format = iota
	// FormatWebM is VP9 in WebM: smaller and open, but slower to encode.
	FormatWebM
)

// FormatFromString maps a preference string to a Format; anything but "webm" is MP4.
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

// ffmpeg CRF: lower is better and bigger. The UI's 0..100 quality maps into these per-codec windows, clamped
// inside the legal range so the slider means the same for both formats.
const (
	// H.264 window: 16 is about visually lossless, 32 is fine for a clip.
	x264CRFBest  = 16
	x264CRFWorst = 32
	// VP9 CRFs run higher than x264 for a comparable look.
	vp9CRFBest  = 18
	vp9CRFWorst = 40
)

func crfFor(format Format, quality int) int {
	quality = value.Clamp(quality, 0, 100)
	best, worst := x264CRFBest, x264CRFWorst
	if format == FormatWebM {
		best, worst = vp9CRFBest, vp9CRFWorst
	}
	return worst - (worst-best)*quality/100
}

var (
	ffmpegOnce sync.Once
	ffmpegPath string
)

// FFmpegPath returns the system ffmpeg path, looked up once; "" (a supported state, not an error) if none.
func FFmpegPath() string {
	ffmpegOnce.Do(func() {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpegPath = p
		}
	})
	return ffmpegPath
}

// Available reports whether a system ffmpeg was found; callers must check it before video export.
func Available() bool { return FFmpegPath() != "" }

// stderrTailBytes bounds the ffmpeg stderr kept for errors; the tail holds the real failure reason.
const stderrTailBytes = 600

// Encoder is driven from one goroutine; call Finish off the render thread, as it waits for ffmpeg to flush.
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

// New starts ffmpeg reading rawvideo from stdin at quality 0..100; check Available first.
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
	winexec.Hide(cmd) // GUI-subsystem build: no ffmpeg console window on export
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

// ffmpegArgs always outputs yuv420p because browsers and QuickTime reject yuv444; "rgba" matches image.RGBA,
// and presets favour speed because the capture loop owns the window during export.
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

// MuxAudioBed copies the silent video and adds one audio file delayed by delayMs, padded to the video's length.
// It blocks until ffmpeg exits, so it must run off the render thread; check Available first.
func MuxAudioBed(videoPath, audioPath, outPath string, delayMs int, format Format) error {
	dur, _ := probeDurationSec(videoPath) // 0 on failure → the unbounded-apad fallback
	return runMux(muxArgs(videoPath, audioPath, outPath, delayMs, format, dur))
}

// AudioClip is one timed audio file; TrimMs > 0 cuts it (music ending when the next song starts), 0 plays it all.
type AudioClip struct {
	Path    string
	DelayMs int
	TrimMs  int
}

// MuxAudioMix copies the video and amixes clips at full volume: normalize would duck the music on every SFX,
// so occasional clipping is accepted. Run it off the render thread and check Available first.
func MuxAudioMix(videoPath string, clips []AudioClip, outPath string, format Format) error {
	if len(clips) == 0 {
		return fmt.Errorf("videoenc: no audio clips")
	}
	dur, _ := probeDurationSec(videoPath) // 0 on failure → the unbounded-apad fallback
	return runMux(muxMixArgs(videoPath, clips, outPath, format, dur))
}

// muxMixArgs: input 0 is the video, each clip is input i+1 delayed to its start, and the sum is padded.
func muxMixArgs(videoPath string, clips []AudioClip, outPath string, format Format, padDurSec float64) []string {
	args := []string{"-y", "-loglevel", "error", "-i", videoPath}
	var fc, labels strings.Builder
	for i, c := range clips {
		args = append(args, "-i", c.Path)
		// Only a music segment is trimmed; adelay then shifts the clip to its time.
		if c.TrimMs > 0 {
			fmt.Fprintf(&fc, "[%d:a]atrim=end=%s,adelay=delays=%d:all=1[a%d];", i+1, secs(c.TrimMs), c.DelayMs, i)
		} else {
			fmt.Fprintf(&fc, "[%d:a]adelay=delays=%d:all=1[a%d];", i+1, c.DelayMs, i)
		}
		fmt.Fprintf(&labels, "[a%d]", i)
	}
	// apad must follow amix, never per input: a SFX-only mix would end early and -shortest would truncate the video.
	fmt.Fprintf(&fc, "%samix=inputs=%d:normalize=0,%s[aout]", labels.String(), len(clips), padFilter(padDurSec))
	acodec := "aac"
	if format == FormatWebM {
		acodec = "libopus"
	}
	return append(args,
		"-filter_complex", fc.String(),
		"-map", "0:v:0", "-c:v", "copy",
		"-map", "[aout]", "-c:a", acodec,
		"-shortest",
		outPath,
	)
}

func secs(ms int) string { return fmt.Sprintf("%d.%03d", ms/1000, ms%1000) }

func runMux(args []string) error {
	bin := FFmpegPath()
	if bin == "" {
		return fmt.Errorf("videoenc: ffmpeg not found on PATH")
	}
	cmd := exec.Command(bin, args...)
	winexec.Hide(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if s := lastStderrLines(stderr.String()); s != "" {
			return fmt.Errorf("ffmpeg mux: %s", s)
		}
		return fmt.Errorf("videoenc: mux failed: %w", err)
	}
	return nil
}

// lastStderrLines keeps two lines, as ffmpeg often prints the real cause above a misleading final line.
func lastStderrLines(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > stderrTailBytes {
		s = s[len(s)-stderrTailBytes:]
	}
	var kept []string
	for i := len(s); i > 0 && len(kept) < 2; {
		j := strings.LastIndexByte(s[:i], '\n')
		if l := strings.TrimSpace(s[j+1 : i]); l != "" {
			kept = append([]string{l}, kept...)
		}
		i = j
		if j < 0 {
			break
		}
	}
	return strings.Join(kept, " | ")
}

// probeDurationSec parses "Duration: HH:MM:SS.ss" from `ffmpeg -i`, which exits non-zero as expected.
func probeDurationSec(path string) (secs float64, ok bool) {
	bin := FFmpegPath()
	if bin == "" {
		return 0, false
	}
	cmd := exec.Command(bin, "-i", path)
	winexec.Hide(cmd)
	var st bytes.Buffer
	cmd.Stderr = &st
	_ = cmd.Run() // non-zero exit expected
	info := st.String()
	i := strings.Index(info, "Duration: ")
	if i < 0 {
		return 0, false
	}
	ts := info[i+len("Duration: "):]
	if j := strings.IndexByte(ts, ','); j >= 0 {
		ts = ts[:j] // "00:00:01.00"
	}
	var hh, mm int
	var ss float64
	if _, err := fmt.Sscanf(strings.TrimSpace(ts), "%d:%d:%f", &hh, &mm, &ss); err != nil {
		return 0, false
	}
	return float64(hh*3600+mm*60) + ss, true
}

// padFilter uses finite apad=whole_dur=D: unbounded apad with -shortest races ffmpeg's mux sync queue, and the
// mux thread can die with a false ENOSPC. Plain apad is only the fallback when the probe fails.
func padFilter(padDurSec float64) string {
	if padDurSec > 0 {
		return fmt.Sprintf("apad=whole_dur=%.3f", padDurSec)
	}
	return "apad"
}

// muxArgs copies the video, encodes audio (AAC for MP4, Opus for WebM), and pads it (see padFilter).
func muxArgs(videoPath, audioPath, outPath string, delayMs int, format Format, padDurSec float64) []string {
	af := padFilter(padDurSec) // pad the audio with trailing silence; -shortest trims to the video
	if delayMs > 0 {
		// all=1 delays every channel, whether the source is mono or stereo.
		af = "adelay=delays=" + strconv.Itoa(delayMs) + ":all=1," + af
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

// AddFrame writes synchronously, since the export owns the window; a broken pipe is a sticky error.
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
	// rawvideo needs tightly packed rows of w*4 bytes; the row loop handles a padded stride.
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

// pipeErr adds ffmpeg's stderr tail, because a raw "broken pipe" tells the user nothing.
func (e *Encoder) pipeErr(err error) error {
	if tail := e.stderrTail(); tail != "" {
		return fmt.Errorf("ffmpeg failed: %s", tail)
	}
	return fmt.Errorf("videoenc: ffmpeg write failed: %w", err)
}

func (e *Encoder) Frames() int { return e.frames }

// Finish waits for ffmpeg to write the file, so it must run off the render thread.
func (e *Encoder) Finish() error {
	if e.done {
		return fmt.Errorf("videoenc: already finished")
	}
	e.done = true
	// Close stdin first: EOF lets ffmpeg finalize, and waiting first would hang it.
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

// Close kills and reaps ffmpeg if still running; safe after Finish and more than once.
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

func (e *Encoder) stderrTail() string {
	if e.stderr == nil {
		return ""
	}
	s := strings.TrimSpace(e.stderr.String())
	if len(s) > stderrTailBytes {
		s = s[len(s)-stderrTailBytes:]
	}
	// ffmpeg prints the cause on the last non-empty line.
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		if last := strings.TrimSpace(s[i+1:]); last != "" {
			return last
		}
		return strings.TrimSpace(s[:i])
	}
	return s
}
