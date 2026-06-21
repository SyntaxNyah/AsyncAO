package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

func newTimerApp(t *testing.T) *App {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	return &App{d: Deps{Prefs: prefs}} // ctx + Audio nil: fireTimer is nil-guarded
}

// TestTimerLifecycle pins the local-timer state machine (#97): start → running,
// pause freezes the remainder (active but not running), resume continues from it,
// reset returns to idle showing the configured duration. Start also persists.
func TestTimerLifecycle(t *testing.T) {
	a := newTimerApp(t)
	a.timerSetSec = 120

	a.startTimer()
	if !a.timerRunning() {
		t.Fatal("startTimer: want running")
	}
	if rem := a.timerRemaining(); rem <= 119*time.Second || rem > 120*time.Second {
		t.Errorf("running remaining = %v, want ~120s", rem)
	}
	if got := a.d.Prefs.TimerSecondsValue(); got != 120 {
		t.Errorf("start must persist the duration: got %d, want 120", got)
	}

	a.pauseTimer()
	if a.timerRunning() {
		t.Error("pause: must not be running")
	}
	if !a.timerActive() || a.timerPausedLeft <= 0 {
		t.Error("pause: want paused (active, remainder frozen)")
	}
	frozen := a.timerPausedLeft

	a.resumeTimer()
	if !a.timerRunning() {
		t.Error("resume: want running")
	}
	if rem := a.timerRemaining(); rem > frozen+time.Second || rem < frozen-2*time.Second {
		t.Errorf("resume remaining = %v, want ~%v", rem, frozen)
	}

	a.resetTimer()
	if a.timerActive() {
		t.Error("reset: want idle")
	}
	if rem := a.timerRemaining(); rem != 120*time.Second {
		t.Errorf("idle remaining = %v, want the configured 120s", rem)
	}
}

// TestTimerFire pins the alarm: a due countdown fires (banner set) and stops; with
// Repeat it restarts from the configured duration; an idle timer never fires.
func TestTimerFire(t *testing.T) {
	a := newTimerApp(t)

	a.timerSetSec = 60
	a.timerRepeat = false
	a.timerEndAt = time.Now().Add(-time.Second) // already due
	a.pollTimer()
	if a.timerRunning() {
		t.Error("no-repeat: must stop after firing")
	}
	if a.warnLine != "Timer finished!" {
		t.Errorf("warn banner = %q, want \"Timer finished!\"", a.warnLine)
	}

	a.timerRepeat = true
	a.timerEndAt = time.Now().Add(-time.Second)
	a.pollTimer()
	if !a.timerRunning() {
		t.Fatal("repeat: must restart")
	}
	if rem := a.timerRemaining(); rem <= 58*time.Second || rem > 60*time.Second {
		t.Errorf("repeat remaining = %v, want ~60s", rem)
	}

	a.resetTimer()
	a.warnLine = ""
	a.pollTimer()
	if a.warnLine != "" {
		t.Error("idle pollTimer must be a no-op (no fire)")
	}
}

// TestFormatTimer pins MM:SS, the floor at zero, and the H:MM:SS form past an hour.
func TestFormatTimer(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00"},
		{-5 * time.Second, "00:00"},
		{65 * time.Second, "01:05"},
		{5 * time.Minute, "05:00"},
		{3661 * time.Second, "1:01:01"},
	}
	for _, c := range cases {
		if got := formatTimer(c.d); got != c.want {
			t.Errorf("formatTimer(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
