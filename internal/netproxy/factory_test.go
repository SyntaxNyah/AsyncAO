package netproxy_test

import (
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
	address     = "http://" + unreachable + "/"
)

func noSystem() platform.ProxySettings { return platform.ProxySettings{} }

func systemServer(server, bypass string) func() platform.ProxySettings {
	return func() platform.ProxySettings {
		return platform.ProxySettings{Server: server, Bypass: platform.ParseProxyBypass(bypass)}
	}
}

func targetServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, testBody)
	}))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "http://")
}

func readAll(t *testing.T, client *http.Client) {
	t.Helper()
	resp, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != testBody {
		t.Errorf("body = %q", body)
	}
}

func TestPlainHTTPIsForwardedNotTunnelled(t *testing.T) {
	_, target := targetServer(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, proxy.URL, "")

	readAll(t, f.Current().Client(netproxy.Web, 0))
	// Plain http goes as a forward request, so it loads through a proxy that only allows CONNECT to 443.
	if got := proxy.Forwards(); !slices.Equal(got, []string{unreachable}) {
		t.Errorf("proxy was asked to forward %v", got)
	}
	if got := proxy.Asked(); len(got) != 0 {
		t.Errorf("http was tunnelled with CONNECT: %v", got)
	}
}

func TestHTTPSStillTunnelsThroughTheWebClient(t *testing.T) {
	proxy := netproxytest.Start(t, http2Target(t))
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, proxy.URL, "")
	client := f.Current().WebClient(netproxy.DialFunc((&net.Dialer{}).DialContext), func(tr *http.Transport) { tr.TLSClientConfig.InsecureSkipVerify = true })
	resp, err := client.Get("https://" + unreachable + "/")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := proxy.Asked(); !slices.Equal(got, []string{unreachable}) {
		t.Errorf("https was not tunnelled with CONNECT: asked=%v forwards=%v", got, proxy.Forwards())
	}
}

func TestGameClientTunnelsPlainHTTP(t *testing.T) {
	_, target := targetServer(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, proxy.URL, "")

	readAll(t, f.Current().Client(netproxy.Game, 0))
	if got := proxy.Asked(); !slices.Equal(got, []string{unreachable}) {
		t.Errorf("proxy was asked for %v", got)
	}
}

func TestProxyLoginTravelsWithTheConnect(t *testing.T) {
	_, target := targetServer(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, strings.Replace(proxy.URL, "http://", "http://me:secret@", 1), "")

	readAll(t, f.Current().Client(netproxy.Game, 0))
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
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, proxy.URL, "")
	_, err := f.Current().Client(netproxy.Game, 0).Get(address)
	if err == nil || !strings.Contains(err.Error(), "proxy") || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
}

func TestBadManualAddressBlocksEveryKindButNotLoopback(t *testing.T) {
	srv, _ := targetServer(t)
	f := netproxy.New(noSystem)
	for _, raw := range []string{"ftp://nope", "socks5://nope:1080"} {
		if p := f.Apply(netproxy.ModeManual, raw, ""); !p.Blocked() {
			t.Errorf("%q did not block", raw)
		}
		for _, kind := range []netproxy.Kind{netproxy.Web, netproxy.Game} {
			if _, err := f.Current().Client(kind, 0).Get(address); !errors.Is(err, netproxy.ErrBadProxyAddress) {
				t.Errorf("%q kind %d: err = %v", raw, kind, err)
			}
			resp, err := f.Current().Client(kind, 0).Get(srv.URL)
			if err != nil {
				t.Errorf("%q kind %d: loopback was blocked: %v", raw, kind, err)
				continue
			}
			resp.Body.Close()
		}
	}
}

func TestDirectAndManualIgnoreEverythingTheMachineSays(t *testing.T) {
	f := netproxy.New(systemServer("proxy.test:1", "intranet.test"))
	if n := f.Apply(netproxy.ModeDirect, "", ""); n.UsesProxy() || n.Blocked() {
		t.Errorf("direct mode must connect directly: %s", n.Describe())
	}
	if n := f.Apply(netproxy.ModeManual, " ", ""); !n.Blocked() {
		t.Errorf("manual with no address must block: %s", n.Describe())
	}
	n := f.Apply(netproxy.ModeManual, "manual.test:2", "")
	if !strings.Contains(n.Describe(), "manual.test:2") {
		t.Errorf("describe = %q", n.Describe())
	}
	if !n.Proxied("intranet.test") {
		t.Error("the OS bypass list applied to a manual proxy")
	}
}

func TestSystemServerListPrefersHTTPS(t *testing.T) {
	cases := []struct{ server, want string }{
		{"http=a.test:1;https=b.test:2;socks=c.test:3", "b.test:2"},
		{"http=a.test:1;socks=c.test:3", "a.test:1"},
		{"bare.test:4", "bare.test:4"},
		{"http=;https=b.test:2", "b.test:2"},
	}
	for _, tc := range cases {
		f := netproxy.New(systemServer(tc.server, ""))
		if got := f.Apply(netproxy.ModeSystem, "", "").Describe(); !strings.Contains(got, tc.want) {
			t.Errorf("%q: %q lacks %s", tc.server, got, tc.want)
		}
	}
	if p := netproxy.New(systemServer("socks=c.test:3", "")).Apply(netproxy.ModeSystem, "", ""); p.UsesProxy() {
		t.Error("a SOCKS-only setting counted as a proxy")
	}
}

func TestSystemBypassListSendsHostsDirect(t *testing.T) {
	f := netproxy.New(systemServer("proxy.test:1", "intranet.test"))
	f.Apply(netproxy.ModeSystem, "", "")
	if f.Current().Proxied("intranet.test") {
		t.Error("a host the OS bypasses counted as proxied")
	}
	if !f.Current().Proxied("example.test") {
		t.Error("a host the OS doesn't bypass went direct")
	}
}

func TestManualBypassSendsHostsDirect(t *testing.T) {
	f := netproxy.New(systemServer("proxy.test:1", "os-list.test"))
	n := f.Apply(netproxy.ModeManual, "manual.test:2", "lan.test;10.*")
	for _, host := range []string{"lan.test", "api.lan.test", "10.0.0.7"} {
		if n.Proxied(host) {
			t.Errorf("%s went through the proxy", host)
		}
	}
	if !n.Proxied("example.test") {
		t.Error("a host not on the list went direct")
	}
	// A bad address still leaves listed hosts reachable, as loopback is.
	if blocked := f.Apply(netproxy.ModeManual, "ftp://nope", "lan.test"); !blocked.Blocked() || blocked.Proxied("lan.test") {
		t.Errorf("blocked = %v, lan proxied = %v", blocked.Blocked(), blocked.Proxied("lan.test"))
	}
}

func TestAChangedSettingAppliesToTheNextConnection(t *testing.T) {
	_, target := targetServer(t)
	first, second := netproxytest.Start(t, target), netproxytest.Start(t, target)
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, first.URL, "")
	readAll(t, f.Current().Client(netproxy.Web, 0))
	f.Apply(netproxy.ModeManual, second.URL, "")
	readAll(t, f.Current().Client(netproxy.Web, 0))
	if got := len(first.Forwards()); got != 1 {
		t.Errorf("the old proxy saw %d requests, want 1", got)
	}
	if got := len(second.Forwards()); got != 1 {
		t.Errorf("the new proxy saw %d requests, want 1", got)
	}
}

func TestAnOpenResponseSurvivesAChange(t *testing.T) {
	_, target := targetServer(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem)
	f.Apply(netproxy.ModeManual, proxy.URL, "")
	resp, err := f.Current().Client(netproxy.Game, 0).Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	f.Apply(netproxy.ModeDirect, "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != testBody {
		t.Errorf("after the change: %q, %v", body, err)
	}
}

func TestDescribeSaysWhatIsInEffect(t *testing.T) {
	f := netproxy.New(noSystem)
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
		if got := f.Apply(tc.mode, tc.url, "").Describe(); !strings.Contains(got, tc.want) {
			t.Errorf("%d %q: %q lacks %q", tc.mode, tc.url, got, tc.want)
		}
	}
}
