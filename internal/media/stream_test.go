package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	streamTestURL   = testOrigin + "sounds/music/a.opus"
	streamTestStart = 1000
	streamTestQuiet = 50 * time.Millisecond
	streamTestWait  = 5 * time.Second
)

// streamTestTrack is long enough that its end is fetched apart.
var streamTestTrack = bytes.Repeat([]byte("opus-page-bytes!"), 3*streamTailBytes/len("opus-page-bytes!"))

// fakeDownloader serves one track whose body the test writes by hand.
type fakeDownloader struct {
	dir     string
	body    *io.PipeReader
	write   *io.PipeWriter
	ranges  bool
	missing bool
	adopted chan string
}

func newFakeDownloader(t *testing.T, ranges bool) *fakeDownloader {
	r, w := io.Pipe()
	t.Cleanup(func() { w.Close() })
	return &fakeDownloader{dir: t.TempDir(), body: r, write: w, ranges: ranges, adopted: make(chan string, 1)}
}

func (d *fakeDownloader) Fetch(context.Context, string) ([]byte, error) { return streamTestTrack, nil }

func (d *fakeDownloader) Open(_ context.Context, url string) (io.ReadCloser, int64, error) {
	if d.missing {
		return nil, 0, fmt.Errorf("%w: %s", network.ErrAssetNotFound, url)
	}
	return d.body, int64(len(streamTestTrack)), nil
}

func (d *fakeDownloader) ReadRange(_ context.Context, _ string, from, n int64) ([]byte, error) {
	if !d.ranges {
		return nil, network.ErrNoRanges
	}
	return streamTestTrack[from : from+n], nil
}

func (d *fakeDownloader) PartialFile() (*os.File, error) { return os.CreateTemp(d.dir, "*.tmp") }
func (d *fakeDownloader) Adopt(_, path string)           { d.adopted <- path }

// send writes part of the body from a goroutine, as the pipe blocks until the download reads it.
func (d *fakeDownloader) send(part []byte, last bool) {
	go func() {
		_, _ = d.write.Write(part)
		if last {
			d.write.Close()
		}
	}()
}

func openTestStream(t *testing.T, d *fakeDownloader) Stream {
	t.Helper()
	st, err := NewRemoteSource(context.Background(), d, testShortTTL, time.Now).Open(File(streamTestURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// readWithin reads at off, failing the test if the read doesn't return within wait.
func readWithin(t *testing.T, st Stream, off int64, n int, wait time.Duration) ([]byte, bool) {
	t.Helper()
	got := make(chan []byte, 1)
	go func() {
		p := make([]byte, n)
		k, _ := st.ReadAt(p, off)
		got <- p[:k]
	}()
	select {
	case p := <-got:
		return p, true
	case <-time.After(wait):
		return nil, false
	}
}

func TestADownloadReadsItsStartBeforeItEnds(t *testing.T) {
	d := newFakeDownloader(t, false)
	st := openTestStream(t, d)
	d.send(streamTestTrack[:streamTestStart], false)
	p, ok := readWithin(t, st, 0, streamTestStart, streamTestWait)
	if !ok || !bytes.Equal(p, streamTestTrack[:streamTestStart]) {
		t.Fatalf("read %d bytes of the start, ok %v", len(p), ok)
	}
	if st.Path() != "" {
		t.Fatal("an unfinished download names a complete file")
	}
}

func TestADownloadAnswersItsEndFromARange(t *testing.T) {
	d := newFakeDownloader(t, true)
	st := openTestStream(t, d)
	off := int64(len(streamTestTrack) - streamTestStart)
	p, ok := readWithin(t, st, off, streamTestStart, streamTestWait)
	if !ok || !bytes.Equal(p, streamTestTrack[off:]) {
		t.Fatalf("read %d bytes of the end before the download reached it, ok %v", len(p), ok)
	}
}

func TestWithoutRangesTheEndWaitsForTheDownload(t *testing.T) {
	d := newFakeDownloader(t, false)
	st := openTestStream(t, d)
	off := int64(len(streamTestTrack) - streamTestStart)
	got := make(chan []byte, 1)
	go func() {
		p := make([]byte, streamTestStart)
		k, _ := st.ReadAt(p, off)
		got <- p[:k]
	}()
	select {
	case <-got:
		t.Fatal("the end read returned before the download reached it")
	case <-time.After(streamTestQuiet):
	}
	d.send(streamTestTrack, true)
	select {
	case p := <-got:
		if !bytes.Equal(p, streamTestTrack[off:]) {
			t.Fatalf("read %d wrong bytes", len(p))
		}
	case <-time.After(streamTestWait):
		t.Fatal("the end read never returned")
	}
}

func TestAFinishedDownloadIsAdopted(t *testing.T) {
	d := newFakeDownloader(t, false)
	st := openTestStream(t, d)
	d.send(streamTestTrack, true)
	select {
	case path := <-d.adopted:
		if st.Path() != path {
			t.Fatalf("Path %q, adopted %q", st.Path(), path)
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, streamTestTrack) {
			t.Fatalf("adopted file differs: %v", err)
		}
	case <-time.After(streamTestWait):
		t.Fatal("a finished download was never adopted")
	}
}

func TestClosingStopsADownloadAndDeletesIt(t *testing.T) {
	d := newFakeDownloader(t, false)
	st := openTestStream(t, d)
	d.send(streamTestTrack[:streamTestStart], false)
	if _, ok := readWithin(t, st, 0, streamTestStart, streamTestWait); !ok {
		t.Fatal("the start never arrived")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadAt(make([]byte, 1), 0); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if left, _ := os.ReadDir(d.dir); len(left) != 0 {
		t.Fatalf("partial files left: %v", left)
	}
}

func TestAMissingTrackIsAbsent(t *testing.T) {
	d := newFakeDownloader(t, false)
	d.missing = true
	if _, err := NewRemoteSource(context.Background(), d, testShortTTL, time.Now).Open(File(streamTestURL)); !IsAbsent(err) {
		t.Fatalf("err %v, want absent", err)
	}
}

func TestAStreamKeyIsReadyWithAStream(t *testing.T) {
	r := newRemoteRig(t, map[string][]byte{streamTestURL: streamTestTrack})
	a := r.m.Get(Key{Place: PlaceServer, Path: streamTestURL, Kind: KindStream, Class: ClassExact}, Now, 0)
	waitFinal(t, a)
	st, ok := a.Result().(Stream)
	if !a.Ready() || !ok {
		t.Fatalf("state %v, result %T", a.State(), a.Result())
	}
	defer st.Close()
	p := make([]byte, streamTestStart)
	if _, err := st.ReadAt(p, 0); err != nil || !bytes.Equal(p, streamTestTrack[:streamTestStart]) {
		t.Fatalf("stream reads %v", err)
	}
}
