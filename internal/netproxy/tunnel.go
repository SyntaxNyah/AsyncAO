package netproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

const (
	// tunnelSetupTimeout bounds the talk with the proxy, which a stuck proxy could otherwise hold forever.
	tunnelSetupTimeout = 15 * time.Second

	defaultHTTPProxyPort  = "80"
	defaultHTTPSProxyPort = "443"
	defaultSOCKSProxyPort = "1080"

	schemeHTTPS = "https"

	// SOCKS5, RFC 1928 and RFC 1929.
	socksVersion       = 5
	socksAuthVersion   = 1
	socksMethodNone    = 0
	socksMethodPass    = 2
	socksCmdConnect    = 1
	socksAddrIPv4      = 1
	socksAddrDomain    = 3
	socksAddrIPv6      = 4
	socksReplySuccess  = 0
	socksIPv4Bytes     = 4
	socksIPv6Bytes     = 16
	socksPortBytes     = 2
	socksMaxFieldBytes = 255
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
	var established net.Conn
	var err error
	switch via.Scheme {
	case "socks5", "socks5h":
		err = socksConnect(conn, via, addr)
		established = conn
	case schemeHTTPS:
		secure := tls.Client(conn, &tls.Config{ServerName: via.Hostname()})
		if err = secure.Handshake(); err != nil {
			return nil, err
		}
		established, err = httpConnect(secure, via, addr)
	default:
		established, err = httpConnect(conn, via, addr)
	}
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
	switch via.Scheme {
	case schemeHTTPS:
		port = defaultHTTPSProxyPort
	case "socks5", "socks5h":
		port = defaultSOCKSProxyPort
	}
	return net.JoinHostPort(via.Hostname(), port)
}

// httpConnect asks for a tunnel with CONNECT. Everything uses it, so ws:// and wss:// behave alike.
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

// socksConnect always sends names, not addresses, so the proxy resolves them and no lookup leaves this machine.
func socksConnect(conn net.Conn, via *url.URL, addr string) error {
	if err := socksGreet(conn, via); err != nil {
		return err
	}
	request, err := socksRequest(addr)
	if err != nil {
		return err
	}
	if _, err := conn.Write(request); err != nil {
		return err
	}
	return socksReadReply(conn)
}

func socksGreet(conn net.Conn, via *url.URL) error {
	methods := []byte{socksVersion, 1, socksMethodNone}
	if via.User != nil {
		methods = []byte{socksVersion, 2, socksMethodNone, socksMethodPass}
	}
	if _, err := conn.Write(methods); err != nil {
		return err
	}
	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		return err
	}
	if choice[0] != socksVersion {
		return errors.New("is not a SOCKS5 server")
	}
	switch choice[1] {
	case socksMethodNone:
		return nil
	case socksMethodPass:
		if via.User != nil {
			return socksAuthenticate(conn, via.User)
		}
	}
	return errors.New("accepted none of the login methods offered")
}

func socksAuthenticate(conn net.Conn, user *url.Userinfo) error {
	password, _ := user.Password()
	name := user.Username()
	if len(name) > socksMaxFieldBytes || len(password) > socksMaxFieldBytes {
		return errors.New("login is too long for SOCKS5")
	}
	message := []byte{socksAuthVersion, byte(len(name))}
	message = append(message, name...)
	message = append(message, byte(len(password)))
	message = append(message, password...)
	if _, err := conn.Write(message); err != nil {
		return err
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[1] != 0 {
		return errors.New("rejected the login")
	}
	return nil
}

func socksRequest(addr string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("bad port in %s: %w", addr, err)
	}
	request := []byte{socksVersion, socksCmdConnect, 0}
	if ip, err := netip.ParseAddr(host); err == nil {
		kind := byte(socksAddrIPv6)
		if ip.Is4() {
			kind = socksAddrIPv4
		}
		request = append(request, kind)
		request = append(request, ip.AsSlice()...)
	} else {
		if len(host) > socksMaxFieldBytes {
			return nil, fmt.Errorf("host name is too long for SOCKS5: %s", host)
		}
		request = append(request, socksAddrDomain, byte(len(host)))
		request = append(request, host...)
	}
	return binary.BigEndian.AppendUint16(request, uint16(port)), nil
}

func socksReadReply(conn net.Conn) error {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[1] != socksReplySuccess {
		return fmt.Errorf("refused the connection (SOCKS reply %d)", header[1])
	}
	var size int
	switch header[3] {
	case socksAddrIPv4:
		size = socksIPv4Bytes
	case socksAddrIPv6:
		size = socksIPv6Bytes
	case socksAddrDomain:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return err
		}
		size = int(length[0])
	default:
		return fmt.Errorf("sent an unknown address type %d", header[3])
	}
	_, err := io.ReadFull(conn, make([]byte, size+socksPortBytes))
	return err
}
