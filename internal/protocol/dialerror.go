package protocol

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DialError is a Dial failure the SERVER answered, as opposed to one the network
// produced: the TCP connection and (for wss://) the TLS handshake both succeeded,
// the HTTP upgrade request went out, and the server replied with a status other
// than 101 — plus, often, an explanation.
//
// It exists because that explanation was being thrown away. Dial discarded the
// *http.Response, so a server refusing the upgrade on purpose was indistinguishable
// from one that is not a WebSocket server at all, and the client said the wrong
// thing out loud: internal/ui's friendlyConnError matched the library's "expected
// handshake response status code 101 but got 503" on " status" and told the user the
// server "may not be a WebSocket (AO2 2.11) server". For a tsuserver-family server
// in lockdown that is simply false, and it sends people away from a server that
// would let them in if they read its message (Nyathena's server.go answers a
// firewall block with 403 Forbidden, a connection-rate trip with 429, and a lockdown
// whose socket accept failed with 503 and the join message as the body).
//
// Errors are values, so this stays in the package that PRODUCES the failure and
// internal/ui reads it with errors.As. Nothing new points the wrong way.
type DialError struct {
	// URL is the ws:// or wss:// URL that was dialled.
	URL string
	// StatusCode / Status are the HTTP response that refused the upgrade.
	StatusCode int
	Status     string
	// Body is the server's own plain-text explanation, sanitized and bounded, or ""
	// when it sent none (or sent something that is not human-readable prose — see
	// dialBody). Consumers must treat "" as "the server did not say".
	Body string
	// Err is the underlying library error, kept so errors.Is/As still see the whole
	// chain and every pre-existing classifier keeps working unchanged.
	Err error
}

func (e *DialError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("protocol: dialing %s: server refused the connection (%s): %s", e.URL, e.Status, e.Body)
	}
	return fmt.Sprintf("protocol: dialing %s: server refused the connection (%s): %v", e.URL, e.Status, e.Err)
}

func (e *DialError) Unwrap() error { return e.Err }

// dialBodyMaxBytes bounds how much of a refusal body we keep.
//
// coder/websocket already limits what it buffers to the first 1024 bytes of a
// failed handshake's body (dial.go), so this is defence in depth rather than the
// only bound — a future library change must not turn an attacker-controlled
// response into an unbounded read on a connect path (rule §17.4).
const dialBodyMaxBytes = 1024

// dialBody pulls the server's explanation out of a refusal response, or returns ""
// when there is nothing worth showing a human.
//
// Requires text/plain, and that requirement is the point rather than pedantry. A 403
// or 503 from the AO server itself is http.Error, which is text/plain; the same
// status from a reverse proxy or a CDN in front of it is an HTML error page, and
// piping markup into a modal that says "the server told you this" would be both
// unreadable and a lie about who said it.
//
// Control characters are dropped and newlines and tabs kept, by SanitizeText — the
// same scrub the wire-packet path uses, for the same reason: the string lands in a
// texture through SDL_ttf and on the clipboard through SDL_SetClipboardText, and both
// marshal via C.CString, where an embedded NUL silently truncates everything after it.
//
// The body is safe to read here and needs no Close: on a handshake failure the
// library has already drained it, closed the real one, and replaced resp.Body with
// an in-memory reader over what it kept.
func dialBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/plain") {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, dialBodyMaxBytes))
	if err != nil && len(b) == 0 {
		return ""
	}
	// SanitizeText, not a local copy of it: the wire-packet path (internal/courtroom's
	// capServerText) needs the identical guarantee, and two copies of a rendering-safety
	// rule is one copy too many. It also cleans up after the library's own 1024-byte
	// cut, which can land mid-rune.
	return strings.TrimSpace(SanitizeText(string(b)))
}
