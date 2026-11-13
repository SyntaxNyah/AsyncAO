package netproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// tunnelSetupTimeout bounds the talk with the proxy, which a stuck proxy could otherwise hold forever.
	tunnelSetupTimeout = 15 * time.Second

	defaultHTTPProxyPort  = "80"
	defaultHTTPSProxyPort = "443"

	schemeHTTPS = "https"
)

// dialTunnel connects to the proxy and asks it for a tunnel to addr. Errors name only the proxy's host, never its
// credentials, and contain the word proxy, which the lobby's error text relies on.
func dialTunnel(ctx context.Context, via *url.URL, addr string) (net.Conn, error) {
	conn, err := dialDirect(ctx, "tcp", proxyAddress(via))
	if err != nil {
		return nil, fmt.Errorf("proxy %s: %w", via.Host, err)
	}
	// A cancelled context must interrupt the blocking reads below.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	established, err := setUpTunnel(conn, via, addr)
	if !stop() {
		err = errors.Join(err, ctx.Err())
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy %s: %w", via.Host, err)
	}
	return established, nil
}

func setUpTunnel(conn net.Conn, via *url.URL, addr string) (net.Conn, error) {
	if err := conn.SetDeadline(time.Now().Add(tunnelSetupTimeout)); err != nil {
		return nil, err
	}
	stream := conn
	if via.Scheme == schemeHTTPS {
		secure := tls.Client(conn, &tls.Config{ServerName: via.Hostname()})
		if err := secure.Handshake(); err != nil {
			return nil, err
		}
		stream = secure
	}
	established, err := httpConnect(stream, via, addr)
	if err != nil {
		return nil, err
	}
	return established, conn.SetDeadline(time.Time{})
}

func proxyAddress(via *url.URL) string {
	if via.Port() != "" {
		return via.Host
	}
	port := defaultHTTPProxyPort
	if via.Scheme == schemeHTTPS {
		port = defaultHTTPSProxyPort
	}
	return net.JoinHostPort(via.Hostname(), port)
}

func httpConnect(conn net.Conn, via *url.URL, addr string) (net.Conn, error) {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: http.Header{"User-Agent": {"AsyncAO"}},
	}
	if via.User != nil {
		password, _ := via.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(via.User.Username() + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refused a tunnel to %s: %s", addr, resp.Status)
	}
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// bufferedConn keeps bytes the proxy sent straight after its reply, which the reply's reader already consumed.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
