package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// streamStallLimit cuts a download that has sent nothing for this long; one that keeps sending is never cut.
const streamStallLimit = 30 * time.Second

// ErrNoRanges reports a host that answered a range request with the whole file.
var ErrNoRanges = errors.New("network: host does not serve byte ranges")

// Body is a response body read as it arrives; Close ends the request.
type Body struct {
	body   io.ReadCloser
	stall  *time.Timer
	cancel context.CancelFunc
	c      *Client
	host   string
	// Size is the body's length, or -1 when the host didn't say.
	Size int64
}

// Open starts a GET for url and returns once the host answers 200, for a caller that reads the body as it arrives.
// The 404 cache and backoff apply as in Fetch; the read is cut when it stalls, never for taking long.
func (c *Client) Open(ctx context.Context, url string) (*Body, error) {
	if err := c.refuse(url); err != nil {
		return nil, err
	}
	resp, host, stall, cancel, err := c.start(ctx, url, "")
	if err != nil {
		return nil, err
	}
	if err := c.checkStatus(url, host, resp, http.StatusOK); err != nil {
		stall.Stop()
		cancel()
		resp.Body.Close()
		return nil, err
	}
	stall.Reset(c.stallLimit)
	size := resp.ContentLength
	if size < 0 {
		size = -1
	}
	return &Body{body: resp.Body, stall: stall, cancel: cancel, c: c, host: host, Size: size}, nil
}

func (b *Body) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.stall.Reset(b.c.stallLimit)
		b.c.bytesRead.Add(int64(n))
	}
	if errors.Is(err, io.EOF) {
		b.c.recordSuccess(b.host)
		b.c.hits.Add(1)
	}
	return n, err
}

// Close ends the request, whether or not the body was read to the end.
func (b *Body) Close() error {
	b.stall.Stop()
	b.cancel()
	return b.body.Close()
}

// ReadRange returns n bytes of url from offset from, or ErrNoRanges when the host sends the whole file instead.
func (c *Client) ReadRange(ctx context.Context, url string, from, n int64) ([]byte, error) {
	if err := c.refuse(url); err != nil {
		return nil, err
	}
	span := "bytes=" + strconv.FormatInt(from, 10) + "-" + strconv.FormatInt(from+n-1, 10)
	resp, host, deadline, cancel, err := c.start(ctx, url, span)
	if err != nil {
		return nil, err
	}
	defer deadline.Stop()
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil, fmt.Errorf("%w: %s", ErrNoRanges, url)
	}
	if err := c.checkStatus(url, host, resp, http.StatusPartialContent); err != nil {
		return nil, err
	}
	deadline.Reset(c.bodyBudget)
	data := make([]byte, n)
	got, err := io.ReadFull(resp.Body, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		c.failures.Add(1)
		return nil, fmt.Errorf("network: reading %s: %w", url, err)
	}
	c.bytesRead.Add(int64(got))
	return data[:got], nil
}

// start sends one request, with span as its Range header when set, and waits for the headers under the host's adaptive
// deadline; the timer it returns cancels the request when it fires, and the caller resets it for the body.
func (c *Client) start(ctx context.Context, url, span string) (*http.Response, string, *time.Timer, context.CancelFunc, error) {
	c.requests.Add(1)
	host := hostOf(url)
	ctx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(c.adaptiveTimeout(host), cancel)
	req, err := c.newRequest(ctx, url)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, host, nil, nil, err
	}
	if span != "" {
		req.Header.Set("Range", span)
	}
	began := time.Now()
	resp, err := c.assetHTTP().Do(req)
	if err != nil {
		timer.Stop()
		cancel()
		c.recordFailure(host)
		c.failures.Add(1)
		return nil, host, nil, nil, fmt.Errorf("network: fetching %s: %w", url, err)
	}
	c.observeLatency(host, time.Since(began))
	return resp, host, timer, cancel, nil
}
