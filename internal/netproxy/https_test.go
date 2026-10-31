package netproxy_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy/netproxytest"
)

func httpsTarget(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, testBody)
	}))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "https://")
}

// TLS must run end to end inside the tunnel: the proxy sees only the address it was asked for.
func TestHTTPSGoesThroughATunnel(t *testing.T) {
	_, target := httpsTarget(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)

	resp, err := f.InsecureClient().Get("https://" + unreachable + "/")
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

// Verification checks the server's certificate against the name asked for, not against the proxy.
func TestHTTPSStillVerifiesTheServerThroughATunnel(t *testing.T) {
	_, target := httpsTarget(t)
	proxy := netproxytest.Start(t, target)
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)

	resp, err := f.Client(0).Get("https://" + unreachable + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("an untrusted certificate was accepted through the tunnel")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("err = %v, want a certificate error", err)
	}
}

// An https:// proxy address means TLS to the proxy itself, then CONNECT inside that.
func TestHTTPSProxyAddressSpeaksTLSToTheProxy(t *testing.T) {
	seen := make(chan string, 1)
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.Host
		w.WriteHeader(http.StatusForbidden)
	}))
	defer proxy.Close()
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)

	_, err := f.DialContext(context.Background(), "tcp", unreachable)
	if err == nil {
		t.Fatal("expected the untrusted test proxy to fail verification")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("err = %v, want the proxy's certificate checked", err)
	}
	select {
	case got := <-seen:
		t.Errorf("sent %q to a proxy that failed verification", got)
	default:
	}
}

func TestBypassedHTTPSGoesDirect(t *testing.T) {
	srv, target := httpsTarget(t)
	proxy := netproxytest.Start(t, "")
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	if f.Proxied(host) {
		t.Fatalf("%s should be direct, as loopback always is", host)
	}
	resp, err := f.InsecureClient().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(proxy.Asked()) != 0 {
		t.Errorf("proxy was asked for %v", proxy.Asked())
	}
}
