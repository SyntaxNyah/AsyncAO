package netproxy

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// withResolver installs f for one test and restores the environment default
// afterwards. The resolver is process-wide by design, so this must not leak.
func withResolver(t *testing.T, f ProxyFunc) {
	t.Helper()
	SetResolver(f)
	t.Cleanup(func() { SetResolver(nil) })
}

// TestProxyDefaultsToTheEnvironment: with nothing installed, behaviour is the
// stdlib's — which is what makes wiring netproxy.Proxy into a transport a
// strictly safe change for every user who has no proxy at all.
func TestProxyDefaultsToTheEnvironment(t *testing.T) {
	got, err := Proxy(&http.Request{URL: mustURL(t, "https://example.test/x")})
	if err != nil {
		t.Fatalf("default resolver errored: %v", err)
	}
	want, wantErr := http.ProxyFromEnvironment(&http.Request{URL: mustURL(t, "https://example.test/x")})
	if wantErr != nil {
		t.Fatalf("stdlib errored: %v", wantErr)
	}
	if (got == nil) != (want == nil) || (got != nil && got.String() != want.String()) {
		t.Errorf("default proxy = %v, want the stdlib's %v", got, want)
	}
}

// TestSetResolverTakesEffectAfterFirstUse is the reason Proxy exists at all
// instead of assigning http.ProxyFromEnvironment straight into every transport:
// the stdlib function memoises the environment behind a sync.Once on FIRST USE,
// process-wide. Anything read later — a settings change, an OS-config refresh —
// could never change the answer.
func TestSetResolverTakesEffectAfterFirstUse(t *testing.T) {
	req := &http.Request{URL: mustURL(t, "https://example.test/x")}
	if _, err := Proxy(req); err != nil { // force the stdlib's Once to fire first
		t.Fatal(err)
	}
	want := mustURL(t, "http://proxy.test:8080")
	withResolver(t, func(*http.Request) (*url.URL, error) { return want, nil })
	got, err := Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.String() != want.String() {
		t.Errorf("proxy after SetResolver = %v, want %v — a later resolver swap must win", got, want)
	}
	SetResolver(nil)
	if got, _ := Proxy(req); got != nil && got.String() == want.String() {
		t.Error("SetResolver(nil) must restore the environment default")
	}
}

// TestResolverSeesTheRequest: the decision is per-request (a NO_PROXY rule, a
// per-scheme split, a PAC evaluation), so the installed resolver must receive
// the request rather than being asked once for a global answer.
func TestResolverSeesTheRequest(t *testing.T) {
	var seen []string
	withResolver(t, func(r *http.Request) (*url.URL, error) {
		seen = append(seen, r.URL.String())
		return nil, nil
	})
	for _, raw := range []string{"http://a.test/1", "https://b.test/2"} {
		if _, err := Proxy(&http.Request{URL: mustURL(t, raw)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != "http://a.test/1" || seen[1] != "https://b.test/2" {
		t.Errorf("resolver saw %v, want both request URLs verbatim", seen)
	}
}

// TestForURLReportsProxiedAndDirect drives the DNS-pre-resolve gate: "would a
// proxy carry this?" must be answerable without building a real request.
func TestForURLReportsProxiedAndDirect(t *testing.T) {
	via := mustURL(t, "http://proxy.test:8080")
	withResolver(t, func(r *http.Request) (*url.URL, error) {
		if r.URL.Host == "secret.test" {
			return via, nil
		}
		return nil, nil
	})
	if got := ForURL(mustURL(t, "https://secret.test/a")); got == nil || got.String() != via.String() {
		t.Errorf("ForURL(proxied) = %v, want %v", got, via)
	}
	if got := ForURL(mustURL(t, "https://open.test/a")); got != nil {
		t.Errorf("ForURL(direct) = %v, want nil", got)
	}
	if got := ForURL(nil); got != nil {
		t.Errorf("ForURL(nil) = %v, want nil", got)
	}
}

// TestForURLTreatsAnErrorAsProxied: a proxy configuration that fails to resolve
// must NOT read as "no proxy". Reading it that way is the exact shape of a leak
// — a broken PAC or a malformed proxy URL would silently hand every hostname to
// the local resolver.
func TestForURLTreatsAnErrorAsProxied(t *testing.T) {
	withResolver(t, func(*http.Request) (*url.URL, error) { return nil, errors.New("bad proxy config") })
	if got := ForURL(mustURL(t, "https://secret.test/a")); got == nil {
		t.Error("an erroring proxy resolver reported DIRECT — a broken config must never open the direct path")
	}
}

// TestTransportCarriesTheProxyAndTheStdlibTimeouts: cloning http.DefaultTransport
// rather than building an empty literal is the substance of the game-socket fix.
// The literal it replaced dropped Proxy AND every timeout with it.
func TestTransportCarriesTheProxyAndTheStdlibTimeouts(t *testing.T) {
	tr := Transport()
	if tr.Proxy == nil {
		t.Fatal("Transport() returned a transport with no Proxy — the whole point")
	}
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not an *http.Transport in this build")
	}
	if tr.TLSHandshakeTimeout != def.TLSHandshakeTimeout || tr.TLSHandshakeTimeout == 0 {
		t.Errorf("TLSHandshakeTimeout = %v, want the stdlib's %v", tr.TLSHandshakeTimeout, def.TLSHandshakeTimeout)
	}
	if tr.IdleConnTimeout != def.IdleConnTimeout || tr.IdleConnTimeout == 0 {
		t.Errorf("IdleConnTimeout = %v, want the stdlib's %v", tr.IdleConnTimeout, def.IdleConnTimeout)
	}
	// A clone, not a share: the caller sets InsecureSkipVerify on it, and reaching
	// http.DefaultTransport's own TLS config would disable verification for the
	// updater and the master list too.
	if tr == def {
		t.Fatal("Transport() handed back http.DefaultTransport itself")
	}
	tr.TLSClientConfig = nil
	if def.TLSClientConfig != nil && tr.TLSClientConfig == def.TLSClientConfig {
		t.Error("mutating the returned transport reached the shared default")
	}
}

// TestResolverSwapIsRaceFree: Proxy is read from every transport goroutine while
// an OS-config refresh may replace the resolver. Meaningful only under -race.
func TestResolverSwapIsRaceFree(t *testing.T) {
	req := &http.Request{URL: mustURL(t, "https://example.test/x")}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_, _ = Proxy(req)
		}
	}()
	for i := 0; i < 200; i++ {
		SetResolver(func(*http.Request) (*url.URL, error) { return nil, nil })
		SetResolver(nil)
	}
	<-done
}
