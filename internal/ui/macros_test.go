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

// TestAutoLoginSkipsAkashi pins the v1.55.3 hotfix: the automatic on-join login
// is suppressed on Akashi (its two-step "/login" ⏎ credential flow is under
// investigation) yet still fires for other server software. Only the auto path
// gates — a manual loginNow is unaffected (covered by TestLoginLines).
func TestAutoLoginSkipsAkashi(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.frameNow = time.Now()
	a.serverKey = "ws://login.test"
	a.d.Prefs.SetServerLogin(a.serverKey, "admin", "hunter2", true) // AutoLogin ON
	a.d.Prefs.SetAutoLoginToast(false)                              // no OS toast side effect in the test

	a.sess.Software = "Akashi 1.8"
	a.autoLoginOnReady()
	if len(a.oocQueue) != 0 {
		t.Fatalf("Akashi auto-login must queue nothing (hotfix), got %d: %v", len(a.oocQueue), a.oocQueue)
	}

	// The guard is Akashi-specific: other software still auto-logs in.
	a.sess.Software = "Athena"
	a.autoLoginOnReady()
	if len(a.oocQueue) == 0 {
		t.Fatal("non-Akashi auto-login must still queue the login flow")
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
