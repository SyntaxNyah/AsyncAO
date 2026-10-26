package ui

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestParseIncomingPM pins the best-effort received-PM parser (the Friends-tab DM
// thread): the recognised "PM from <sender>: <message>" shape with optional
// "(...)" sender annotation, case-insensitive prefix, and — critically — NO match
// on the server's own outgoing echo ("PM sent to ...") or ordinary OOC, so nothing
// double-records and a non-PM line is never misfiled.
func TestParseIncomingPM(t *testing.T) {
	cases := []struct {
		in          string
		sender, msg string
		ok          bool
	}{
		{"PM from Phoenix: hello there", "Phoenix", "hello there", true},
		{"PM from Phoenix (CID 5): hey", "Phoenix", "hey", true}, // annotation dropped
		{"pm from miles: case prefix", "miles", "case prefix", true},
		{"PM sent to Phoenix: hi", "", "", false}, // outgoing echo must NOT match
		{"just a normal ooc line", "", "", false},
		{"PM from Phoenix:", "", "", false}, // no message
		{"PM from : nobody", "", "", false}, // no sender
	}
	for _, tc := range cases {
		sender, msg, ok := parseIncomingPM(tc.in)
		if ok != tc.ok || sender != tc.sender || msg != tc.msg {
			t.Errorf("parseIncomingPM(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, sender, msg, ok, tc.sender, tc.msg, tc.ok)
		}
	}
}

// TestPMThreadBounds pins the conversation store: per-thread + thread-count caps,
// the outgoing-echo guard, and that a real "PM from" lands as an incoming line.
func TestPMThreadBounds(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	a.serverKey = "s"

	// Per-thread line cap keeps only the newest pmThreadLineCap lines.
	for i := 0; i < pmThreadLineCap+20; i++ {
		a.pmAppend("Phoenix", true, "msg")
	}
	if n := len(a.pmThreads["phoenix"]); n != pmThreadLineCap {
		t.Errorf("thread len = %d, want %d (capped)", n, pmThreadLineCap)
	}

	// Conversation cap bounds the number of distinct threads.
	for i := 0; i < pmThreadCap+10; i++ {
		a.pmAppend(fmt.Sprintf("partner%d", i), true, "hi")
	}
	if n := len(a.pmThreads); n > pmThreadCap {
		t.Errorf("threads = %d, want <= %d", n, pmThreadCap)
	}

	// detectIncomingPM: outgoing echo creates nothing; a real "PM from" records once.
	a2 := &App{d: Deps{Prefs: prefs}}
	a2.serverKey = "s"
	a2.detectIncomingPM("PM sent to Phoenix: hi")
	if len(a2.pmThreads) != 0 {
		t.Errorf("outgoing echo must not create a thread, got %d", len(a2.pmThreads))
	}
	a2.detectIncomingPM("PM from Phoenix: hi back")
	if got := a2.pmThreads["phoenix"]; len(got) != 1 || got[0].fromMe || got[0].text != "hi back" {
		t.Errorf("incoming PM recorded wrong: %+v", got)
	}
}
