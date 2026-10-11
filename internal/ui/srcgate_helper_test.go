package ui

import "testing"

// failTB records a Fatalf call so a helper's failure path can be asserted without
// terminating the test (a census that silently stops checking must be observable).
type failTB struct {
	testing.TB
	fatalfCalled bool
}

func (f *failTB) Fatalf(format string, args ...any) { f.fatalfCalled = true }
func (f *failTB) Helper()                           {}

// TestFuncBodySourceFoundAndAbsent pins the package-wide lookup's contract: a
// present function is found wherever it lives, and an absent one must fail the
// gate loudly rather than silently stop checking.
func TestFuncBodySourceFoundAndAbsent(t *testing.T) {
	if body := funcBodySource(t, "ui.go", "blitLabel"); body == nil {
		t.Fatal("blitLabel should be found package-wide")
	}
	ft := &failTB{}
	funcBodySource(ft, "ui.go", "ThisFunctionDoesNotExist")
	if !ft.fatalfCalled {
		t.Error("funcBodySource did not fail for an absent function — a renamed/removed function would go unchecked")
	}
}

