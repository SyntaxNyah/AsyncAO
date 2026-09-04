package ui

// OOC-to-disk logging. The per-(server, tab session) writer identity is already
// proven by translogsplit_test.go/translogretire_test.go; these tests are the OOC
// half — the channel was never wired to a writer at all before this change, only
// to the in-memory oocLog slice the on-screen panel reads.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestOOCLogRespectsDetailedLogPref mirrors how logDetailed already no-ops with
// detailed logging off: pushOOC must not open (or write to) any transcript writer,
// so a user who turned logging off never gets a file appearing on disk.
func TestOOCLogRespectsDetailedLogPref(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetDetailedLog(false)
	a.serverName = "Skrapegropen-ooc-pref-off"

	a.pushOOC("Nyah: should not be logged", "Nyah")

	if n := len(a.translogs); n != 0 {
		t.Errorf("pushOOC opened %d transcript writer(s) with detailed logging OFF — it must stay a no-op", n)
	}
}

// transcriptLogFiles/transcriptNewFile find which file a transcriptFor call just
// created, by diffing the server's log directory before/after — the writer type
// exposes no path of its own to ask for directly.
func transcriptLogFiles(t *testing.T, server string) map[string]bool {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := filepath.Join(filepath.Dir(exe), "logs", sanitizeLogFolder(server))
	names, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

func transcriptNewFile(t *testing.T, server string, before map[string]bool) (string, bool) {
	t.Helper()
	for n := range transcriptLogFiles(t, server) {
		if !before[n] {
			return n, true
		}
	}
	return "", false
}

// TestPushOOCSuppressedRosterBurstNeverReachesDisk pins "log only what's shown":
// the /gas reply burst that pushOOC deliberately keeps OFF the on-screen OOC log
// (the suppression window) must not reach the transcript file either. Checked by
// reading the file back after a synchronous CloseTranscript, not by peeking the
// writer's channel length — the background goroutine can drain a wrongly-queued
// line before an in-process check ever observes it, which would hide the bug.
func TestPushOOCSuppressedRosterBurstNeverReachesDisk(t *testing.T) {
	const server = "Skrapegropen-ooc-suppressed"
	a := testTabApp(t)
	a.d.Prefs.SetDetailedLog(true)
	a.serverName = server
	t.Cleanup(a.CloseTranscript)
	a.frameNow = time.Now()
	a.suppressAreaEchoUntil = a.frameNow.Add(time.Minute) // arm the /gas suppression window

	before := transcriptLogFiles(t, server)
	if a.transcriptFor(server, a.logSession) == nil {
		t.Skip("transcript writers unavailable in this environment (no writable exe dir)")
	}
	file, ok := transcriptNewFile(t, server, before)
	if !ok {
		t.Fatal("opening the transcript writer created no new file")
	}

	beforeOOC := len(a.oocLog)
	a.pushOOC("5 players online", "") // looksLikeAreaList == true
	if len(a.oocLog) != beforeOOC {
		t.Fatal("setup broken: the roster burst reached the on-screen OOC log, so it wasn't actually suppressed")
	}

	a.CloseTranscript() // synchronous flush, so the file reflects everything queued

	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read transcript file: %v", err)
	}
	if strings.Contains(string(b), "players online") {
		t.Error("a suppressed roster burst reached the transcript file — it must never reach disk")
	}
}

// TestTwoTabsOOCLogsGoToSeparateFiles drives the REAL seams — pushOOC for the live
// tab, routeBackgroundEvent's EventOOC case for a parked one — and proves the exact
// bug class reported: "two async tabs both write into same log file". It reads the
// files back after a synchronous CloseTranscript (the one place a wait is allowed),
// so this is the encapsulation test: OOC cannot land in a writer belonging to a
// different session, driven through the production call path rather than reimplemented.
func TestTwoTabsOOCLogsGoToSeparateFiles(t *testing.T) {
	const server = "Skrapegropen-ooc-split"
	a := testTabApp(t)
	a.d.Prefs.SetDetailedLog(true)
	a.serverName = server
	t.Cleanup(a.CloseTranscript)

	// Open each tab's writer EXPLICITLY, independent of the OOC seam under test:
	// transcriptFor itself is unconditional (the DetailedLogOn gate lives in
	// logDetailed/logDetailedOOC, not here), so an unwritable exe dir skips
	// cleanly here and a BROKEN OOC seam instead shows up as a content mismatch
	// below — never a false skip that would hide the regression.
	before := transcriptLogFiles(t, server)
	if a.transcriptFor(server, a.logSession) == nil {
		t.Skip("transcript writers unavailable in this environment (no writable exe dir)")
	}
	tab1File, ok := transcriptNewFile(t, server, before)
	if !ok {
		t.Fatal("opening tab one's transcript writer created no new file")
	}

	a.pushOOC("Nyah: hello from tab one", "Nyah")

	// Park tab one, bring up tab two on the SAME server.
	a.tabs = append(a.tabs, &courtTab{})
	a.activeTab = 0
	a.parkActive()
	a.serverName = server

	before = transcriptLogFiles(t, server)
	if a.transcriptFor(server, a.logSession) == nil {
		t.Fatal("tab two's transcript writer failed to open")
	}
	tab2File, ok := transcriptNewFile(t, server, before)
	if !ok {
		t.Fatal("opening tab two's transcript writer created no new file")
	}
	if tab1File == tab2File {
		t.Fatalf("both tabs resolved to the SAME file %q — this is the reported defect", tab1File)
	}

	a.pushOOC("Nyah: hello from tab two", "Nyah")

	// The exact bug class reported: tab one, now BACKGROUNDED, logs OOC of its own.
	// It must land in tab1File (its own log), not tab2File (the now-active tab's).
	a.routeBackgroundEvent(a.tabs[0], courtroom.Event{Kind: courtroom.EventOOC, Name: "Nyah", Text: "parked tab OOC"})

	a.CloseTranscript() // flush + close every writer; safe to wait, the test is over

	b1, err := os.ReadFile(tab1File)
	if err != nil {
		t.Fatalf("read tab1 file: %v", err)
	}
	b2, err := os.ReadFile(tab2File)
	if err != nil {
		t.Fatalf("read tab2 file: %v", err)
	}
	f1, f2 := string(b1), string(b2)

	if !strings.Contains(f1, "hello from tab one") {
		t.Error("tab one's file is missing its own OOC line")
	}
	if !strings.Contains(f1, "parked tab OOC") {
		t.Error("tab one's file is missing the line it logged WHILE PARKED — the exact bug class reported")
	}
	if strings.Contains(f1, "hello from tab two") {
		t.Error("tab one's file leaked tab two's OOC line")
	}

	if !strings.Contains(f2, "hello from tab two") {
		t.Error("tab two's file is missing its own OOC line")
	}
	if strings.Contains(f2, "hello from tab one") || strings.Contains(f2, "parked tab OOC") {
		t.Error("tab two's file leaked tab one's OOC lines")
	}
}
