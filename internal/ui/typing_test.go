package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestTypingPrefGatesSend pins the #3 guarantee the advisor flagged: with the indicator
// OFF the client never emits a pulse, and with it on the throttle holds to ~1 / interval.
func TestTypingPrefGatesSend(t *testing.T) {
	t0 := time.Unix(1000, 0)
	a := &App{d: Deps{Prefs: &config.AssetPreferences{}}}
	a.frameNow = t0
	a.icInput = "hello"

	// Pref OFF: never sends, whatever's typed — the zero-traffic guarantee.
	if a.shouldSendTypingPulse(t0) {
		t.Fatal("typing pulse sent with the indicator OFF")
	}

	// Pref ON + a fresh keystroke + throttle clear → sends once.
	a.d.Prefs.SetTypingIndicator(true)
	a.icInput = "hello!" // a change registers a keystroke at t0
	if !a.shouldSendTypingPulse(t0) {
		t.Fatal("no pulse on a fresh keystroke with the indicator on")
	}
	a.lastTypingSent = t0

	// Inside the throttle window: no resend.
	if a.shouldSendTypingPulse(t0.Add(2 * time.Second)) {
		t.Error("pulse resent inside the throttle window")
	}
	// Past the throttle window, still actively typing: resends.
	if !a.shouldSendTypingPulse(t0.Add(typingResendInterval)) {
		t.Error("no resend after the throttle window")
	}
	// Empty draft: never sends.
	a.icInput = ""
	if a.shouldSendTypingPulse(t0.Add(typingResendInterval)) {
		t.Error("pulse sent with an empty draft")
	}
}

// TestTypingLine pins the caption formatting incl. the "+N others" cap.
func TestTypingLine(t *testing.T) {
	if typingLine(nil) != "" {
		t.Error("no typers → empty line")
	}
	if got := typingLine([]string{"Maya"}); got != "Maya is typing…" {
		t.Errorf("one typer = %q", got)
	}
	if got := typingLine([]string{"A", "B"}); got != "A and B are typing…" {
		t.Errorf("two typers = %q", got)
	}
	if got := typingLine([]string{"A", "B", "C", "D"}); got != "A, B and 2 others are typing…" {
		t.Errorf("four typers = %q", got)
	}
}
