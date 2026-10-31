// Package netproxytest runs a real proxy for tests, so a test can prove traffic went through it: it
// tunnels CONNECT requests and relays plain forward (absolute-URI) requests, both to one target.
package netproxytest

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
)

// Proxy is a local proxy that sends every request to one target, whatever address the client asked
// for. CONNECT requests are tunnelled; plain forward requests are relayed.
type Proxy struct {
	// URL is the proxy's address, for Factory.Apply.
	URL string

	mu       sync.Mutex
	asked    []string
	headers  []http.Header
	forwards []string
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

// Asked returns the addresses clients asked to tunnel with CONNECT, in order.
func (p *Proxy) Asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

// Forwards returns the hosts clients asked the proxy to fetch as plain forward requests, in order.
func (p *Proxy) Forwards() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.forwards...)
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
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		p.tunnel(conn, reader, req, target)
		return
	}
	p.forward(conn, req, target)
}

// tunnel carries a CONNECT request through to target.
func (p *Proxy) tunnel(conn net.Conn, reader *bufio.Reader, req *http.Request, target string) {
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

// forward relays a plain absolute-URI request to target, rewriting it to origin form.
func (p *Proxy) forward(conn net.Conn, req *http.Request, target string) {
	p.mu.Lock()
	p.forwards = append(p.forwards, req.Host)
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
	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""
	if err := req.Write(upstream); err != nil {
		return
	}
	_, _ = io.Copy(conn, upstream)
}
