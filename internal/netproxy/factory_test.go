package netproxy_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy/netproxytest"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const (
	testBody    = "through"
	unreachable = "asyncao-test.invalid:1234"
)

func noEnv(string) string { return "" }

func noSystem() platform.ProxySettings { return platform.ProxySettings{} }

func targetServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, testBody)
	}))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "http://")
}

func TestManualProxyCarriesPlainHTTPThroughAConnectTunnel(t *testing.T) {
	_, target := targetServer(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)

	resp, err := f.Client(0).Get("http://" + unreachable + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != testBody {
		t.Errorf("body = %q", body)
	}
	if got := proxy.Asked(); !slices.Equal(got, []string{unreachable}) {
		t.Errorf("proxy was asked for %v", got)
	}
}

func TestProxyLoginTravelsWithTheConnect(t *testing.T) {
	_, target := targetServer(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, strings.Replace(proxy.URL, "http://", "http://me:secret@", 1))

	conn, err := f.DialContext(context.Background(), "tcp", unreachable)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("me:secret"))
	if got := proxy.Header(0).Get("Proxy-Authorization"); got != want {
		t.Errorf("auth header = %q", got)
	}
	if strings.Contains(f.Current().Describe(), "secret") {
		t.Error("the status line shows the password")
	}
}

func TestARefusedTunnelNamesTheProxy(t *testing.T) {
	proxy := netproxytest.Start(t, "")
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)
	_, err := f.DialContext(context.Background(), "tcp", unreachable)
	if err == nil || !strings.Contains(err.Error(), "proxy") || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
}

// A connection before Apply would otherwise go direct, the leak this package exists to prevent.
func TestNothingConnectsBeforeTheFirstApply(t *testing.T) {
	f := netproxy.New(noSystem, noEnv)
	_, err := f.DialContext(context.Background(), "tcp", unreachable)
	if err == nil || !strings.Contains(err.Error(), "before the proxy setting") {
		t.Errorf("err = %v", err)
	}
}

func TestBadManualAddressBlocksInsteadOfGoingDirect(t *testing.T) {
	_, target := targetServer(t)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, "ftp://nope")
	// Loopback is always direct, so a public name proves the block.
	if _, err := f.DialContext(context.Background(), "tcp", unreachable); !errors.Is(err, netproxy.ErrBadProxyAddress) {
		t.Errorf("err = %v", err)
	}
	if conn, err := f.DialContext(context.Background(), "tcp", target); err != nil {
		t.Errorf("loopback was blocked: %v", err)
	} else {
		conn.Close()
	}
}

func TestProxyScriptBlocksButBypassedHostsStillConnect(t *testing.T) {
	f := netproxy.New(func() platform.ProxySettings {
		return platform.ProxySettings{AutoConfigURL: "http://wpad.test/p.pac", Bypass: "asyncao-test.invalid"}
	}, noEnv)
	p := f.Apply(netproxy.ModeSystem, "")
	if !p.Blocked() {
		t.Fatal("a proxy script must block")
	}
	if _, err := f.DialContext(context.Background(), "tcp", "example.test:1"); !errors.Is(err, netproxy.ErrProxyUnresolvable) {
		t.Errorf("err = %v", err)
	}
	if f.Proxied("asyncao-test.invalid") {
		t.Error("a bypassed host counted as proxied")
	}
}

func TestDirectModeIgnoresTheComputersProxy(t *testing.T) {
	f := netproxy.New(func() platform.ProxySettings { return platform.ProxySettings{Server: "proxy.test:1"} },
		func(string) string { return "http://env.test:1" })
	if p := f.Apply(netproxy.ModeDirect, ""); p.Via != nil {
		t.Errorf("via = %v", p.Via)
	}
}

func TestEnvironmentBeatsTheOSAndBothSpellingsWork(t *testing.T) {
	for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"} {
		f := netproxy.New(func() platform.ProxySettings { return platform.ProxySettings{Server: "os.test:1"} },
			func(key string) string {
				if key == name {
					return "env.test:2"
				}
				return ""
			})
		if p := f.Apply(netproxy.ModeSystem, ""); p.Via == nil || p.Via.Host != "env.test:2" {
			t.Errorf("%s: via = %v", name, p.Via)
		}
	}
}

func TestSystemServerListPrefersHTTPS(t *testing.T) {
	cases := []struct{ server, want string }{
		{"http=a.test:1;https=b.test:2;socks=c.test:3", "b.test:2"},
		{"http=a.test:1;socks=c.test:3", "a.test:1"},
		{"socks=c.test:3", "c.test:3"},
		{"bare.test:4", "bare.test:4"},
		{"http=;https=b.test:2", "b.test:2"},
	}
	for _, tc := range cases {
		f := netproxy.New(func() platform.ProxySettings { return platform.ProxySettings{Server: tc.server} }, noEnv)
		if p := f.Apply(netproxy.ModeSystem, ""); p.Via == nil || p.Via.Host != tc.want {
			t.Errorf("%q: via = %v, want %s", tc.server, p.Via, tc.want)
		}
	}
}

// A reused connection keeps the route it was opened on, so changing the setting must drop idle ones.
func TestApplyDropsIdleConnectionsSoTheNewRouteTakesEffect(t *testing.T) {
	_, target := targetServer(t)
	first, second := netproxytest.Start(t, target), netproxytest.Start(t, target)
	f := netproxy.New(noSystem, noEnv)
	address := "http://" + unreachable + "/"
	get := func() {
		t.Helper()
		resp, err := f.Client(0).Get(address)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	f.Apply(netproxy.ModeManual, first.URL)
	get()
	f.Apply(netproxy.ModeManual, second.URL)
	get()
	if len(second.Asked()) != 1 {
		t.Errorf("the new proxy saw %v; the old connection was reused", second.Asked())
	}
}

// socksServer speaks just enough SOCKS5 to record the request and tunnel to target.
func socksServer(t *testing.T, target string, wantLogin bool) (string, chan []byte) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	seen := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		const headerBytes, bufBytes = 2, 512
		buf := make([]byte, bufBytes)
		if _, err := io.ReadFull(conn, buf[:headerBytes]); err != nil {
			return
		}
		if _, err := io.ReadFull(conn, buf[:buf[1]]); err != nil {
			return
		}
		if wantLogin {
			_, _ = conn.Write([]byte{5, 2})
			n, _ := conn.Read(buf)
			seen <- append([]byte(nil), buf[:n]...)
			_, _ = conn.Write([]byte{1, 0})
		} else {
			_, _ = conn.Write([]byte{5, 0})
		}
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		if !wantLogin {
			seen <- append([]byte(nil), buf[:n]...)
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer upstream.Close()
		_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		go func() { _, _ = io.Copy(upstream, conn) }()
		_, _ = io.Copy(conn, upstream)
	}()
	return listener.Addr().String(), seen
}

func TestSOCKS5SendsTheNameNotALocalLookup(t *testing.T) {
	_, target := targetServer(t)
	addr, seen := socksServer(t, target, false)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, "socks5://"+addr)
	resp, err := f.Client(0).Get("http://" + unreachable + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != testBody {
		t.Errorf("body = %q", body)
	}
	if request := <-seen; !strings.Contains(string(request), "asyncao-test.invalid") {
		t.Errorf("request %v does not carry the host name", request)
	}
}

func TestSOCKS5SendsTheLogin(t *testing.T) {
	_, target := targetServer(t)
	addr, seen := socksServer(t, target, true)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, "socks5://me:secret@"+addr)
	conn, err := f.DialContext(context.Background(), "tcp", unreachable)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if login := string(<-seen); !strings.Contains(login, "me") || !strings.Contains(login, "secret") {
		t.Errorf("login message = %q", login)
	}
}

func TestDescribeSaysWhatIsInEffect(t *testing.T) {
	f := netproxy.New(noSystem, noEnv)
	cases := []struct {
		mode netproxy.Mode
		url  string
		want string
	}{
		{netproxy.ModeManual, "proxy.test:8080", "http://proxy.test:8080"},
		{netproxy.ModeManual, "ftp://x", "can't be read"},
		{netproxy.ModeDirect, "", "no proxy"},
		{netproxy.ModeSystem, "", "no proxy configured"},
	}
	for _, tc := range cases {
		if got := f.Apply(tc.mode, tc.url).Describe(); !strings.Contains(got, tc.want) {
			t.Errorf("%d %q: %q lacks %q", tc.mode, tc.url, got, tc.want)
		}
	}
}
