package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// streamTailBytes is fetched apart from the download, as decoders read a file's end when they open it: an Opus
	// file's last page holds its length, an MP3's last bytes its tags.
	streamTailBytes = 64 << 10
	// streamChunkBytes is how much of a download is written per read.
	streamChunkBytes = 32 << 10
	// MaxDownloads bounds the downloads running at once; Open waits on a Network worker for a free one.
	MaxDownloads = 4
)

// ErrStreamClosed is what a read of a closed Stream returns.
var ErrStreamClosed = errors.New("media: stream closed")

// Stream is a file opened for reading, possibly still arriving: ReadAt waits for bytes that haven't come yet.
type Stream interface {
	io.ReaderAt
	io.Closer
	// Size is the file's length, or -1 while it isn't known.
	Size() int64
	// Path is the file on disk when it is complete there, or "" for one still arriving or held in memory.
	Path() string
}

// Opener is a source that opens a file as a Stream for KindStream keys, instead of reading all its bytes.
type Opener interface {
	Open(File) (Stream, error)
}

// Downloader is what a RemoteSource needs to hand out a file as it downloads: the body as it arrives, one span of it,
// and a disk file to keep it in, which Adopt gives the disk cache once complete.
type Downloader interface {
	Open(ctx context.Context, url string) (body io.ReadCloser, size int64, err error)
	ReadRange(ctx context.Context, url string, from, n int64) ([]byte, error)
	PartialFile() (*os.File, error)
	Adopt(url, path string)
}

// BytesStream is a Stream over bytes already in memory.
func BytesStream(data []byte) Stream { return bytesStream{bytes.NewReader(data)} }

type bytesStream struct{ *bytes.Reader }

func (s bytesStream) Close() error { return nil }
func (s bytesStream) Path() string { return "" }

// FileStream opens a complete file on disk as a Stream.
func FileStream(path string) (Stream, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return fileStream{f, info.Size()}, nil
}

type fileStream struct {
	*os.File
	size int64
}

func (s fileStream) Size() int64  { return s.size }
func (s fileStream) Path() string { return s.Name() }

// download is a file arriving into a partial file on disk; one goroutine writes it, readers wait on cond.
type download struct {
	file *os.File
	size int64
	body io.ReadCloser
	mu   sync.Mutex
	cond *sync.Cond
	// have is how much of the file, from its start, is written; tail is the file's end once fetched apart.
	have   int64
	tail   []byte
	tailAt int64
	done   bool
	err    error
	closed bool
}

// newDownload starts copying body into file, and fetches the file's end apart when readRange can.
func newDownload(url string, body io.ReadCloser, size int64, file *os.File, d Downloader, slots chan struct{}) *download {
	s := &download{file: file, size: size, body: body}
	s.cond = sync.NewCond(&s.mu)
	go s.copy(url, d, slots)
	if size > streamTailBytes {
		go s.fetchTail(url, d)
	}
	return s
}

func (s *download) copy(url string, d Downloader, slots chan struct{}) {
	defer func() { <-slots }()
	buf := make([]byte, streamChunkBytes)
	var err error
	for {
		n, readErr := s.body.Read(buf)
		if n > 0 {
			s.mu.Lock()
			at := s.have
			s.mu.Unlock()
			if _, err = s.file.WriteAt(buf[:n], at); err != nil {
				break
			}
			s.mu.Lock()
			s.have += int64(n)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				err = readErr
			}
			break
		}
	}
	s.body.Close()
	s.mu.Lock()
	s.done, s.err = true, err
	if err == nil && s.size < 0 {
		s.size = s.have
	}
	if err == nil && s.size >= 0 && s.have != s.size {
		s.err = fmt.Errorf("media: %s ended at %d of %d bytes", url, s.have, s.size)
	}
	ok := s.err == nil
	s.cond.Broadcast()
	s.mu.Unlock()
	if ok {
		d.Adopt(url, s.file.Name())
	}
}

func (s *download) fetchTail(url string, d Downloader) {
	at := s.size - streamTailBytes
	data, err := d.ReadRange(context.Background(), url, at, streamTailBytes)
	if err != nil || int64(len(data)) != streamTailBytes {
		return // reads of the end wait for the download, as with a host that has no ranges
	}
	s.mu.Lock()
	s.tail, s.tailAt = data, at
	s.cond.Broadcast()
	s.mu.Unlock()
}

// ReadAt answers from the written start or the fetched end, and waits for the download otherwise.
func (s *download) ReadAt(p []byte, off int64) (int, error) {
	end := off + int64(len(p))
	s.mu.Lock()
	for {
		switch {
		case s.closed:
			s.mu.Unlock()
			return 0, ErrStreamClosed
		case s.tail != nil && off >= s.tailAt:
			n := copy(p, s.tail[off-s.tailAt:])
			s.mu.Unlock()
			if n < len(p) {
				return n, io.EOF
			}
			return n, nil
		case end <= s.have || s.done:
			failed := s.done && s.err != nil && end > s.have
			err := s.err
			s.mu.Unlock()
			if failed {
				return 0, err
			}
			return s.file.ReadAt(p, off)
		}
		s.cond.Wait()
	}
}

func (s *download) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// Path is the partial file once the download is complete, readable until Close.
func (s *download) Path() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done && s.err == nil {
		return s.file.Name()
	}
	return ""
}

// Close stops the download if it is still running and deletes the partial file; a complete one is already adopted.
func (s *download) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	s.body.Close()
	name := s.file.Name()
	err := s.file.Close()
	_ = os.Remove(name)
	return err
}

// openDownload starts a download of f, waiting for a free slot first.
func (s *RemoteSource) openDownload(d Downloader, f File) (Stream, error) {
	select {
	case s.slots <- struct{}{}:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	release := func() { <-s.slots }
	body, size, err := d.Open(s.ctx, string(f))
	if err != nil {
		release()
		if errors.Is(err, network.ErrAssetNotFound) {
			return nil, fmt.Errorf("%w: %w", ErrAbsent, err)
		}
		return nil, err
	}
	file, err := d.PartialFile()
	if err != nil {
		body.Close()
		release()
		return nil, err
	}
	return newDownload(string(f), body, size, file, d, s.slots), nil
}

// openStream opens f through a source that can, and wraps its bytes otherwise.
func openStream(source Source, f File) (Stream, error) {
	if o, ok := source.(Opener); ok {
		return o.Open(f)
	}
	return wholeStream(source.Data(f))
}

// wholeStream wraps a source's answer of whole bytes.
func wholeStream(data []byte, err error) (Stream, error) {
	if err != nil {
		return nil, err
	}
	return BytesStream(data), nil
}
