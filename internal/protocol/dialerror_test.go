package protocol

// The gates for a REFUSED handshake — a server that answered and said no.
//
// Every one of them drives the real Dial against a real httptest server, because the
// whole seam is a contract with coder/websocket about what survives a failed
// handshake (the library drains the body, closes the real one and replaces it with an
// in-memory reader over what it kept). A test that built an *http.Response by hand and
// called dialBody would pin our own arithmetic and prove nothing about that contract
// — and it is the contract that broke the feature.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// refusingServer answers every upgrade attempt with status and body, never accepting
// the WebSocket. This is exactly what a tsuserver-family server in lockdown does
// (../Nyathena/internal/athena/server.go: http.Error for the firewall block, the
// connection-rate trip and the lockdown whose accept failed).
func refusingServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// dialRefused dials the server and returns the *DialError it must produce.
func dialRefused(t *testing.T, srv *httptest.Server) *DialError {
	t.Helper()
	conn, err := Dial(context.Background(), wsURL(srv))
	if err == nil {
		conn.Close()
		t.Fatal("the dial SUCCEEDED against a server that refuses every upgrade")
	}
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("a refusal produced %T (%v), not a *DialError — internal/ui cannot tell "+
			"\"the server said no\" from \"this is not a WebSocket server\"", err, err)
	}
	return de
}

// TestARefusedDialCarriesTheServersOwnWords is the gate for the reported case: a
// Russian server that whitelists by IP, refusing with the instructions for getting in.
//
// The body is what the user needs and what was being thrown away. Newlines and
// Cyrillic both have to survive — the Discord invite and the access code are on their
// own lines, and a mangled or truncated URL is a URL people mistype.
func TestARefusedDialCarriesTheServersOwnWords(t *testing.T) {
	const body = "Вы не находитесь в вайт-листе сервера.\n" +
		"Присоединитесь к нашему Discord: https://discord.gg/n95zkcBE8h\n" +
		"Для получения доступа обратитесь к администрации."

	de := dialRefused(t, refusingServer(t, http.StatusForbidden, "text/plain; charset=utf-8", body))
	if de.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want %d", de.StatusCode, http.StatusForbidden)
	}
	if de.Body != body {
		t.Fatalf("the refusal body did not survive:\n got %q\nwant %q", de.Body, body)
	}
	if !strings.Contains(de.Error(), "discord.gg") {
		t.Error("Error() drops the server's message — the one string a log or a bug report keeps")
	}
	if de.URL == "" || de.Status == "" {
		t.Errorf("URL/Status not carried: %q / %q", de.URL, de.Status)
	}
}

// TestARefusalBodyIsOnlyQuotedWhenItIsProse pins the Content-Type requirement, which
// is a correctness rule and not pedantry: a 403 from the AO server itself is
// http.Error (text/plain), while the same status from a reverse proxy or a CDN in
// front of it is an HTML error page. Putting markup in a modal that says "the server
// told you this" is unreadable AND a lie about who said it.
func TestARefusalBodyIsOnlyQuotedWhenItIsProse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{"plain text", "text/plain; charset=utf-8", "Too many connections", "Too many connections"},
		{"bare text/plain", "text/plain", "Forbidden", "Forbidden"},
		{"a proxy's HTML page", "text/html; charset=utf-8", "<html><body>502</body></html>", ""},
		{"no content type at all", "", "Forbidden", ""},
		{"json", "application/json", `{"error":"nope"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			de := dialRefused(t, refusingServer(t, http.StatusTooManyRequests, tc.contentType, tc.body))
			if de.Body != tc.want {
				t.Errorf("Body = %q, want %q", de.Body, tc.want)
			}
		})
	}
}

// TestARefusalBodyIsBoundedAndScrubbed is rule §17.4 on a connect path plus the
// SDL_ttf hazard. The response is attacker-chosen: it must not be read unbounded, and
// what survives must be safe to rasterize — an embedded NUL truncates the C string
// silently and an escape sequence draws as tofu. Newlines and tabs are the two
// controls that carry meaning and are kept.
func TestARefusalBodyIsBoundedAndScrubbed(t *testing.T) {
	huge := strings.Repeat("A", 64*1024)
	de := dialRefused(t, refusingServer(t, http.StatusServiceUnavailable, "text/plain", huge))
	if len(de.Body) == 0 {
		t.Fatal("a long refusal body produced nothing at all")
	}
	if len(de.Body) > dialBodyMaxBytes {
		t.Errorf("kept %d bytes of a %d-byte body, past the %d cap", len(de.Body), len(huge), dialBodyMaxBytes)
	}

	de = dialRefused(t, refusingServer(t, http.StatusForbidden, "text/plain",
		"line one\x00\x1b[31mred\x07\nline\ttwo\n"))
	if strings.ContainsAny(de.Body, "\x00\x1b\x07") {
		t.Errorf("control characters reached the body: %q", de.Body)
	}
	if !strings.Contains(de.Body, "\n") || !strings.Contains(de.Body, "\t") {
		t.Errorf("the scrub ate the newline or the tab, which carry the layout: %q", de.Body)
	}
	if !strings.Contains(de.Body, "red") {
		t.Errorf("the scrub ate real text along with the escape: %q", de.Body)
	}
}

// TestANetworkFailureIsNotADialError is the other half of the classifier contract, and
// the one that keeps the change closed. internal/ui's friendlyConnError matches a
// pile of substrings on the error text for DNS failures, refused connections and TLS
// errors. Those all leave resp == nil, so they must keep exactly the wrapped form they
// have today — a DialError with a zero StatusCode would send every one of them down
// the new "the server refused you" branch.
func TestANetworkFailureIsNotADialError(t *testing.T) {
	// A port nothing is listening on: the connection is refused before any HTTP.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := wsURL(srv)
	srv.Close() // now dial the dead address

	_, err := Dial(context.Background(), url)
	if err == nil {
		t.Fatal("dialling a closed port succeeded")
	}
	var de *DialError
	if errors.As(err, &de) {
		t.Fatalf("a transport failure came back as a *DialError (status %d) — every "+
			"existing error-text classifier in internal/ui would be bypassed", de.StatusCode)
	}
	if !strings.Contains(err.Error(), "protocol: dialing") {
		t.Errorf("the transport error lost its wrapper: %v", err)
	}
}

// TestADialErrorStillUnwrapsToTheLibraryError pins substitutability: the new type is
// added to the chain, it does not replace it. Anything that was calling errors.Is or
// reading the library's own message keeps working.
func TestADialErrorStillUnwrapsToTheLibraryError(t *testing.T) {
	de := dialRefused(t, refusingServer(t, http.StatusServiceUnavailable, "text/plain", "locked down"))
	if de.Unwrap() == nil {
		t.Fatal("the library error was dropped — errors.Is/As down the chain now fail")
	}
	if !errors.Is(de, de.Err) {
		t.Error("errors.Is cannot reach the wrapped error through the DialError")
	}
	// And the status is still legible in the chain's text, which is what the debug log
	// and any bug report carry.
	if !strings.Contains(de.Error(), "503") {
		t.Errorf("Error() lost the status: %v", de)
	}
}
