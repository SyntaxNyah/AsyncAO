package host

import "testing"

// A quote in a file name must not end the string and run the rest as a command.
func TestPowerShellLiteralKeepsQuotesInside(t *testing.T) {
	const (
		input = `C:\a'; calc; 'b.gif`
		want  = `'C:\a''; calc; ''b.gif'`
	)
	if got := powerShellLiteral(input); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCopyFileToClipboardRefusesAnEmptyPath(t *testing.T) {
	if (Desktop{}).CopyFileToClipboard("") {
		t.Error("an empty path was accepted")
	}
}
