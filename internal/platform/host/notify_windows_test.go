package host

import "testing"

// A crafted showname must not break out of the toast's XML or the script's string.
func TestXMLEscapeStopsInjection(t *testing.T) {
	const (
		input = "a&<b>\"c'\n"
		want  = "a&amp;&lt;b&gt;&quot;c&apos;"
	)
	if got := xmlEscape(input); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
