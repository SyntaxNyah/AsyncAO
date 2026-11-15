package network

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

const (
	streamTestHalf  = 4096
	streamTestStall = 50 * time.Millisecond
	streamTestFrom  = 100
	streamTestSpan  = 64
)

var streamTestFile = bytes.Repeat([]byte("0123456789abcdef"), 2*streamTestHalf/len("0123456789abcdef"))

func TestOpenHandsOverBytesBeforeTheHostFinishes(t *testing.T) {
	rest := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(streamTestFile)))
		_, _ = w.Write(streamTestFile[:streamTestHalf])
		w.(http.Flusher).Flush()
		<-rest
		_, _ = w.Write(streamTestFile[streamTestHalf:])
	}))
	defer srv.Close()
	body, err := NewClientNotFoundTTL(0, netproxy.Direct()).Open(context.Background(), srv.URL+"/music/a.opus")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if body.Size != int64(len(streamTestFile)) {
		t.Fatalf("size %d, want %d", body.Size, len(streamTestFile))
	}
	first := make([]byte, streamTestHalf)
	if _, err := io.ReadFull(body, first); err != nil {
		t.Fatal(err)
	}
	close(rest)
	all, err := io.ReadAll(body)
	if err != nil || !bytes.Equal(append(first, all...), streamTestFile) {
		t.Fatalf("body differs, err %v", err)
	}
}

func TestOpenCachesAMissingTrack(t *testing.T) {
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := NewClientNotFoundTTL(0, netproxy.Direct())
	for range 2 {
		if _, err := c.Open(context.Background(), srv.URL+"/music/gone.opus"); !errors.Is(err, ErrAssetNotFound) {
			t.Fatalf("err %v, want not found", err)
		}
	}
	if asked.Load() != 1 {
		t.Fatalf("asked %d times, want once", asked.Load())
	}
}

func TestOpenCutsAStalledBody(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(streamTestFile)))
		w.(http.Flusher).Flush()
		<-hold
	}))
	defer srv.Close()
	defer close(hold) // before srv.Close, which waits for the handler
	c := NewClientNotFoundTTL(0, netproxy.Direct())
	c.stallLimit = streamTestStall
	body, err := c.Open(context.Background(), srv.URL+"/music/slow.opus")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("a body that stopped sending read to the end")
	}
}

func TestReadRangeReadsOnlyTheSpan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.opus", time.Time{}, bytes.NewReader(streamTestFile))
	}))
	defer srv.Close()
	got, err := NewClientNotFoundTTL(0, netproxy.Direct()).ReadRange(context.Background(), srv.URL+"/music/a.opus", streamTestFrom, streamTestSpan)
	if err != nil {
		t.Fatal(err)
	}
	if want := streamTestFile[streamTestFrom : streamTestFrom+streamTestSpan]; !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadRangeReportsAHostWithoutRanges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(streamTestFile)
	}))
	defer srv.Close()
	if _, err := NewClientNotFoundTTL(0, netproxy.Direct()).ReadRange(context.Background(), srv.URL+"/music/a.opus", streamTestFrom, streamTestSpan); !errors.Is(err, ErrNoRanges) {
		t.Fatalf("err %v, want ErrNoRanges", err)
	}
}
