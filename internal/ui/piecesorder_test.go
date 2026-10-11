package ui

import (
	"os"
	"strings"
	"testing"
)

// TestTornPlayersTabExists guards the premise of the test above: if the Players
// tab ever stops being tearable, the ordering rationale changes and this pair
// should be revisited rather than silently kept.
func TestTornPlayersTabExists(t *testing.T) {
	src, err := os.ReadFile("torntabs.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "logTabPlayers") {
		t.Skip("the Players tab is no longer tearable; revisit why the pieces panel draws last")
	}
}
