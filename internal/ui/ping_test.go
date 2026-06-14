package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestSortByPing pins the lobby connect-time order: favorites stay pinned (even
// with a high ping), then probed servers ascending by RTT, then unprobed /
// unreachable (by name), and legacy TCP-only entries always last.
func TestSortByPing(t *testing.T) {
	entries := []network.ServerEntry{
		{Name: "Slow", IP: "slow", WSSPort: 2096},
		{Name: "Fast", IP: "fast", WSSPort: 2096},
		{Name: "Unprobed", IP: "unp", WSSPort: 2096},
		{Name: "Dead", IP: "dead", WSSPort: 2096},
		{Name: "Fav", IP: "fav", WSSPort: 2096, Favorite: true},
		{Name: "Legacy", IP: "leg", Port: 50001}, // TCP-only → not joinable
	}
	pings := map[string]time.Duration{
		"wss://fast:2096": 30 * time.Millisecond,
		"wss://slow:2096": 200 * time.Millisecond,
		"wss://dead:2096": -1,                     // unreachable
		"wss://fav:2096":  500 * time.Millisecond, // pinned despite the high ping
	}
	sortByPing(entries, pings)
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name
	}
	want := []string{"Fav", "Fast", "Slow", "Dead", "Unprobed", "Legacy"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
