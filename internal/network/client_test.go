package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleflightCollapsesConcurrentFetches(t *testing.T) {
	var upstream atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		<-release // hold every waiter on one in-flight request
		fmt.Fprint(w, "sprite-bytes")
	}))
	defer srv.Close()

	c := NewClient()
	const callers = 32
	var wg sync.WaitGroup
	results := make([][]byte, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.Fetch(context.Background(), srv.URL+"/characters/phoenix/(a)normal.webp")
		}(i)
	}
	// Give every goroutine time to join the flight, then release the one
	// upstream request.
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := upstream.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want exactly 1 (singleflight)", got)
	}
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d error: %v", i, errs[i])
		}
		if string(results[i]) != "sprite-bytes" {
			t.Errorf("caller %d got %q", i, results[i])
		}
	}
}

func TestNotFoundCachedWithinTTL(t *testing.T) {
	var upstream atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient()
	url := srv.URL + "/characters/edgeworth/(a)missing.webp"

	if _, err := c.Fetch(context.Background(), url); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("first fetch err = %v, want ErrAssetNotFound", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := c.Fetch(context.Background(), url); !errors.Is(err, ErrAssetNotFound) {
			t.Fatalf("repeat fetch err = %v, want ErrAssetNotFound", err)
		}
	}
	if got := upstream.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1 (cached 404 must not re-probe)", got)
	}
	if s := c.Stats(); s.Cached404s != 10 {
		t.Errorf("Cached404s = %d, want 10", s.Cached404s)
	}
}

func TestNotFoundTTLExpires(t *testing.T) {
	var upstream atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	const ttl = 50 * time.Millisecond
	c := newClient(DefaultRequestTimeout, ttl)
	url := srv.URL + "/sounds/blips/missing.opus"

	_, _ = c.Fetch(context.Background(), url)
	time.Sleep(4 * ttl)
	_, _ = c.Fetch(context.Background(), url)
	if got := upstream.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2 (TTL must expire)", got)
	}
}

func TestForgetNotFoundAllowsImmediateRetry(t *testing.T) {
	var upstream atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient()
	url := srv.URL + "/x.webp"
	_, _ = c.Fetch(context.Background(), url)
	c.ForgetNotFound(url)
	_, _ = c.Fetch(context.Background(), url)
	if got := upstream.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2 after ForgetNotFound", got)
	}
}

func TestFetchKnownAndUnknownContentLength(t *testing.T) {
	payload := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/known":
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload)
		case "/chunked":
			w.(http.Flusher).Flush() // force chunked: no Content-Length
			w.Write(payload)
		}
	}))
	defer srv.Close()

	c := NewClient()
	for _, path := range []string{"/known", "/chunked"} {
		got, err := c.Fetch(context.Background(), srv.URL+path)
		if err != nil {
			t.Fatalf("Fetch(%s): %v", path, err)
		}
		if string(got) != string(payload) {
			t.Errorf("Fetch(%s) = %q, want %q", path, got, payload)
		}
	}
}

func TestCallerCancellationDoesNotKillSharedFetch(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		fmt.Fprint(w, "slow-asset")
	}))
	defer srv.Close()

	c := NewClient()
	url := srv.URL + "/slow.webp"

	impatient, cancel := context.WithCancel(context.Background())
	impatientErr := make(chan error, 1)
	go func() {
		_, err := c.Fetch(impatient, url)
		impatientErr <- err
	}()

	patientResult := make(chan []byte, 1)
	go func() {
		data, err := c.Fetch(context.Background(), url)
		if err != nil {
			t.Errorf("patient caller failed: %v", err)
		}
		patientResult <- data
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-impatientErr; !errors.Is(err, context.Canceled) {
		t.Errorf("impatient caller err = %v, want context.Canceled", err)
	}

	close(release)
	select {
	case data := <-patientResult:
		if string(data) != "slow-asset" {
			t.Errorf("patient caller got %q", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("patient caller never completed; cancellation killed the shared fetch")
	}
}

func TestHostBackoffAfterFailure(t *testing.T) {
	// Dead listener: connection refused immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	c := NewClient()
	if _, err := c.Fetch(context.Background(), deadURL+"/a.webp"); err == nil {
		t.Fatal("fetch against dead server succeeded?")
	}
	_, err := c.Fetch(context.Background(), deadURL+"/b.webp")
	if !errors.Is(err, ErrHostBackingOff) {
		t.Errorf("second fetch err = %v, want ErrHostBackingOff", err)
	}
}

func TestBackoffClearsOnSuccess(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			panic(http.ErrAbortHandler) // aborts the connection mid-response
		}
		fmt.Fprint(w, "recovered")
	}))
	defer srv.Close()

	c := NewClient()
	url := srv.URL + "/x.webp"
	if _, err := c.Fetch(context.Background(), url); err == nil {
		t.Fatal("expected first fetch to fail")
	}

	// Manually clear the window (as time passing would) and recover.
	fail.Store(false)
	c.recordSuccess(hostOf(url))
	data, err := c.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("fetch after recovery: %v", err)
	}
	if string(data) != "recovered" {
		t.Errorf("got %q", data)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"http://example.com/a/b.webp": "example.com",
		"https://example.com:8443/a":  "example.com:8443",
		"http://10.0.0.1:8080/x?q=1":  "10.0.0.1:8080",
		"http://host":                 "host",
		"not-a-url":                   "",
		"https://h.example.org#frag":  "h.example.org",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDNSPreResolveCachesLocalhost(t *testing.T) {
	d := newDNSCache()
	d.preResolve(context.Background(), "localhost")
	addrs, stale, found := d.lookup("localhost")
	if !found || len(addrs) == 0 {
		t.Skip("localhost did not resolve in this environment")
	}
	if stale {
		t.Error("fresh entry reported stale")
	}
}
