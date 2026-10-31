// Package netproxytest runs a real CONNECT proxy for tests, so a test can prove traffic went through it.
package netproxytest

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
)

// Proxy is a local CONNECT proxy that sends every tunnel to one target, whatever address the client asked for.
type Proxy struct {
	// URL is the proxy's address, for Factory.Apply.
	URL string

	mu      sync.Mutex
	asked   []string
	headers []http.Header
}

// Start runs a proxy until the test ends. An empty target refuses every tunnel with 403.
func Start(t *testing.T, target string) *Proxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	p := &Proxy{URL: "http://" + listener.Addr().String()}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go p.serve(conn, target)
		}
	}()
	return p
}

// Asked returns the addresses clients asked for, in order.
func (p *Proxy) Asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

// Header returns the headers of the nth request.
func (p *Proxy) Header(n int) http.Header {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.headers[n]
}

func (p *Proxy) serve(conn net.Conn, target string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil || req.Method != http.MethodConnect {
		_, _ = io.WriteString(conn, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
		return
	}
	p.mu.Lock()
	p.asked = append(p.asked, req.Host)
	p.headers = append(p.headers, req.Header)
	p.mu.Unlock()
	if target == "" {
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\n\r\n")
		return
	}
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer upstream.Close()
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	<-done
}
