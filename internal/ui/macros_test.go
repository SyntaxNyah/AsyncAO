package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestMacroQueuePacing pins the engine: lines send in order on the
// macroLineDelay cadence (prompt-style flows need the gap), never early,
// and the queue is bounded.
func TestMacroQueuePacing(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil) // swallows sends
	t0 := time.Now()
	a.frameNow = t0

	a.queueOOCLines([]string{"/login", "user pass", "/tsundere 1"})
	if len(a.oocQueue) != 3 {
		t.Fatalf("queued %d lines, want 3", len(a.oocQueue))
	}

	a.processOOCQueue() // t0: only the first line is due
	if len(a.oocQueue) != 2 {
		t.Fatalf("at t0 exactly one line must send, %d left", len(a.oocQueue))
	}
	a.frameNow = t0.Add(macroLineDelay)
	a.processOOCQueue()
	if len(a.oocQueue) != 1 {
		t.Fatalf("at t0+delay the second line must send, %d left", len(a.oocQueue))
	}
	a.frameNow = t0.Add(10 * macroLineDelay)
	a.processOOCQueue()
	if len(a.oocQueue) != 0 {
		t.Fatal("late frames must drain everything due")
	}

	// Bound: spamming can't grow past macroQueueCap.
	var many []string
	for i := 0; i < macroQueueCap+10; i++ {
		many = append(many, "/spam")
	}
	a.queueOOCLines(many)
	if len(a.oocQueue) > macroQueueCap {
		t.Fatalf("queue exceeded cap: %d > %d", len(a.oocQueue), macroQueueCap)
	}
}

// TestOOCNameFallback pins the AsyncAO<1-200> identity: sticky within a
// run, in range, and overridden by a real name.
func TestOOCNameFallback(t *testing.T) {
	a := testTabApp(t)
	first := a.oocNameOrDefault()
	if !strings.HasPrefix(first, "AsyncAO") {
		t.Fatalf("fallback = %q, want AsyncAO<n>", first)
	}
	if first != a.oocNameOrDefault() {
		t.Fatal("fallback must be sticky within one run")
	}
	n := strings.TrimPrefix(first, "AsyncAO")
	if len(n) == 0 || len(n) > 3 {
		t.Fatalf("fallback suffix %q out of the 1-200 range shape", n)
	}
	a.oocName = "Nyah"
	if got := a.oocNameOrDefault(); got != "Nyah" {
		t.Fatalf("a set name must win, got %q", got)
	}
}

// TestInLoginGrace pins the post-login callword grace: off before any login,
// on inside the window (so the server's name-echoing login replies don't
// self-ping), and off again once it expires.
func TestInLoginGrace(t *testing.T) {
	a := &App{}
	base := time.Now()
	a.frameNow = base
	if a.inLoginGrace() {
		t.Error("no login yet (zero loginAt) must not be in grace")
	}
	a.loginAt = base
	a.frameNow = base.Add(2 * time.Second)
	if !a.inLoginGrace() {
		t.Error("2s after login must be within the grace window")
	}
	a.frameNow = base.Add(loginCallwordGrace + time.Second)
	if a.inLoginGrace() {
		t.Error("past the window must not be in grace")
	}
}

// TestLoginLines pins the two wire shapes: Akashi's two-step prompt flow
// vs the one-line form everyone else (and unknown servers) uses.
func TestLoginLines(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)

	a.sess.Software = "Akashi 1.8"
	got := a.loginLines("admin", "hunter2")
	if len(got) != 2 || got[0] != "/login" || got[1] != "admin hunter2" {
		t.Fatalf("akashi flow = %v", got)
	}

	// KFO has no usernames: password only.
	a.sess.Software = "KFO-Server"
	got = a.loginLines("admin", "hunter2")
	if len(got) != 1 || got[0] != "/login hunter2" {
		t.Fatalf("kfo flow = %v, want password-only", got)
	}

	for _, soft := range []string{"Nyathena", "Athena", "Whisker", ""} {
		a.sess.Software = soft
		got = a.loginLines("admin", "hunter2")
		if len(got) != 1 || got[0] != "/login admin hunter2" {
			t.Fatalf("%q flow = %v, want one-line", soft, got)
		}
	}
}

// TestAutoLoginFiresOnce pins the auto-login hotfix: EventReady rides the DONE
// packet, which some servers re-send mid-session (the WAP/Akashi fork, area
// changes) — the saved login used to re-queue on every one and spam OOC. A
// per-session latch caps it at ONE attempt ("try once and stop"). WAP /
// witches-akashi-party (SoftwareWitches) is disabled by request; stock Akashi and
// every other family still auto-log in once.
func TestAutoLoginFiresOnce(t *testing.T) {
	newApp := func(software string) *App {
		a := testTabApp(t)
		a.sess = courtroom.NewRehearsalSession("", nil)
		a.sess.Software = software
		a.frameNow = time.Now()
		a.serverKey = "ws://login.test"
		a.d.Prefs.SetServerLogin(a.serverKey, "admin", "hunter2", true) // AutoLogin ON
		a.d.Prefs.SetAutoLoginToast(false)                              // no OS toast side effect in the test
		return a
	}

	// Stock Akashi and the non-Akashi families auto-log in — exactly ONCE, however
	// many times EventReady (DONE) re-fires.
	for _, sw := range []string{"Athena", "Akashi 1.8", "KFO-Server"} {
		a := newApp(sw)
		a.autoLoginOnReady()
		n := len(a.oocQueue)
		if n == 0 {
			t.Fatalf("%s: auto-login must queue the login flow once, got 0", sw)
		}
		a.autoLoginOnReady() // a re-sent DONE
		a.autoLoginOnReady() // …and another
		if len(a.oocQueue) != n {
			t.Fatalf("%s: auto-login must fire once: queue grew %d → %d on repeat DONE", sw, n, len(a.oocQueue))
		}
	}

	// WAP / witches-akashi-party is disabled: it announces "WAP-Akashi" (and the
	// canonical "witches-akashi-party"), both → SoftwareWitches → no auto-login.
	for _, sw := range []string{"WAP-Akashi", "witches-akashi-party"} {
		a := newApp(sw)
		a.autoLoginOnReady()
		if len(a.oocQueue) != 0 {
			t.Fatalf("%s: WAP auto-login must be disabled, got %v", sw, a.oocQueue)
		}
	}
}

// TestMacroSanitize pins the config caps: counts, line counts, line
// length, key normalization, empty drops.
func TestMacroSanitize(t *testing.T) {
	p, _ := newTestMacroPrefs(t)
	long := strings.Repeat("x", config.MacroLineMax+50)
	p.SetMacros([]config.MacroSpec{
		{Name: " Login ", Key: " A ", Lines: []string{" /login ", "", long}},
		{Name: "", Lines: []string{"dropped: no name"}},
		{Name: "empty", Lines: nil},
	})
	got := p.Macros()
	if len(got) != 1 {
		t.Fatalf("sanitize kept %d macros, want 1", len(got))
	}
	m := got[0]
	if m.Name != "Login" || m.Key != "a" || len(m.Lines) != 2 {
		t.Fatalf("sanitized macro = %+v", m)
	}
	if len(m.Lines[1]) != config.MacroLineMax {
		t.Fatalf("line length %d, want capped at %d", len(m.Lines[1]), config.MacroLineMax)
	}
}

func newTestMacroPrefs(t *testing.T) (*config.AssetPreferences, string) {
	t.Helper()
	a := testTabApp(t)
	return a.d.Prefs, ""
}
