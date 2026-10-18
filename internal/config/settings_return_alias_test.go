package config

import (
	"sync"
	"testing"
)

// TestReturnedCollectionsNeverAlias is the silent-race gate: getters that return a map,
// slice or a struct holding them must hand back a DEEP copy, so a caller mutating the
// result never races a reader of the shared snapshot. Four readers Load() the snapshot
// while the main goroutine mutates the copies returned by Macros() and ServerWarmInfoFor().
//
// Mutation line: revert Macros() to a shallow `copy(out, snap.UserMacros)` (drop the
// snapCloneMacroSpec per-element deep copy) and this test FAILS under -race — the caller's
// `m[0].Lines[0] = "mutated"` writes the snapshot's slice the readers are ranging.
func TestReturnedCollectionsNeverAlias(t *testing.T) {
	p := defaultPrefs("")
	p.SetMacros([]MacroSpec{{Name: "m", Lines: []string{"a", "b"}}})
	p.RememberServerChars("s", []string{"ch"})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := p.Load()
				if len(snap.UserMacros) > 0 && len(snap.UserMacros[0].Lines) > 0 {
					_ = snap.UserMacros[0].Lines[0]
				}
				if sw := snap.ServerWarm["s"]; len(sw.Chars) > 0 {
					_ = sw.Chars[0]
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if m := p.Load().Macros(); len(m) > 0 && len(m[0].Lines) > 0 {
			m[0].Lines[0] = "mutated"
		}
		if s := p.ServerWarmInfoFor("s"); len(s.Chars) > 0 {
			s.Chars[0] = "mutated"
		}
	}
	close(stop)
	wg.Wait()
}
