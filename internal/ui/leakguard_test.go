package ui

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain runs the suite and then fails if any goroutine still references this
// module's production code after every test's t.Cleanup has run. A goroutine
// outliving its test is a leak, and its stack names the owner.
func TestMain(m *testing.M) {
	code := m.Run()
	if leaks := leakedStacks(); len(leaks) > 0 {
		os.Stderr.WriteString("leaked goroutines after all cleanups:\n")
		for _, s := range leaks {
			os.Stderr.WriteString(s + "\n----\n")
		}
		os.Exit(1)
	}
	os.Exit(code)
}

// leakAllowlist matches by function name and carries the reason a goroutine may
// legitimately still be alive (or winding down) after the suite.
var leakAllowlist = []string{
	"internal/ui.TestMain", // this guard itself, plus the generated test runner
	"main.main",
	"coder/websocket",    // peer timeout/read loops closing on a handshake that races the dump
	"wsTestServer.func1", // hijacked handler parked on r.Context().Done, closed by the harness conn.Close
	"TestDeliberateDisconnectDoesNotReconnect.func1", // hijacked handler, same
	"TestPumpConnectionSurfacesHalfDeadWrite.func1",  // hijacked handler, same
	"TestPumpConnectionSurvivesServerKick.func1",     // hijacked handler, same
	"hashicorp/golang-lru/v2/expirable",              // expirable.LRU janitor: library has no Stop; production creates one Client
}

// leakedStacks polls briefly for goroutines winding down on a close handshake,
// then returns the stacks of the remaining production goroutines not in the
// allowlist.
func leakedStacks() []string {
	deadline := time.Now().Add(5 * time.Second)
	for {
		leaks := productionGoroutines()
		if len(leaks) == 0 || time.Now().After(deadline) {
			return leaks
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// productionGoroutines returns the stack of every goroutine that references
// production code and is not covered by the allowlist.
func productionGoroutines() []string {
	buf := make([]byte, 8<<20)
	n := runtime.Stack(buf, true)
	var leaks []string
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if !strings.Contains(g, "github.com/SyntaxNyah/AsyncAO/internal/") {
			continue
		}
		allowed := false
		for _, a := range leakAllowlist {
			if strings.Contains(g, a) {
				allowed = true
				break
			}
		}
		if !allowed {
			leaks = append(leaks, g)
		}
	}
	return leaks
}
