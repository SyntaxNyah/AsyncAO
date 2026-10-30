package protocol

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DialError is a Dial failure the server answered with a non-101 status, so its explanation reaches the user
// instead of "not a WebSocket server".
type DialError struct {
	// URL is the ws:// or wss:// URL that was dialled.
	URL string
	// StatusCode and Status are the HTTP response that refused the upgrade.
	StatusCode int
	Status     string
	// Body is the server's sanitized, bounded plain-text explanation; "" means the server did not say.
	Body string
	// Err is the underlying library error, kept so errors.Is and errors.As see the whole chain.
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
const dialBodyMaxBytes = 1024

// dialBody needs no Close: on a handshake failure the library has already swapped resp.Body for a memory reader.
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
	// Also cleans up after the library's 1024-byte cut, which can land mid-rune.
	return strings.TrimSpace(SanitizeText(string(b)))
}
