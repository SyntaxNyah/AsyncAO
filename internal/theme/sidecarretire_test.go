package theme

// THE DEFERRED ELEMENT-SECTION DELETE (v1.90.0 W9).
//
// ONE concern: Sidecar.RetireElementSection and the write-time flush behind it.
// What it is FOR — an editor delete that survives a save — is gated where the act
// lives (internal/ui's themeeditordoc_test.go); this is the seam.
//
// The property that makes the deferral worth having, and the reason the removal
// is not simply performed on the spot: an element delete's undo is a per-element
// resurrection, and the file has to come back BYTE-IDENTICAL when the user
// changes their mind before saving.

import (
	"strconv"
	"strings"
	"testing"
)

// retireFixture is a hand-authored theme: two elements, comments on both, and
// the author's own spacing. Everything a canonical rewrite would flatten.
const retireFixture = `[theme]
format = 1

[element.keeper]
; the author's note about this one
kind   = shape
shape  = pill
fill   = #ff0000

[element.doomed]
; and a note about the other
kind   = shape
shape  = sharp
fill   = #00ff00
`

func mustParseSidecar(t *testing.T, src string) *Sidecar {
	t.Helper()
	sc, err := ParseSidecar([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return sc
}

func mustBytes(t *testing.T, sc *Sidecar) string {
	t.Helper()
	b, err := sc.Bytes()
	if err != nil {
		t.Fatalf("serialise: %v", err)
	}
	return string(b)
}

// TestARetiredElementSectionGoesAtTheWriteAndNotBefore pins both halves of the
// deferral in one sequence: the lines are still there afterwards, and they are
// gone the moment anything serialises.
func TestARetiredElementSectionGoesAtTheWriteAndNotBefore(t *testing.T) {
	sc := mustParseSidecar(t, retireFixture)
	sc.Elements = sc.Elements[:1] // the model half of a delete

	if !sc.RetireElementSection("doomed") {
		t.Fatal("retiring a section the file carries reported nothing to do")
	}
	if _, still := sc.doc.Get("element.doomed", "shape"); !still {
		t.Fatal("the section was removed immediately — an undo before the next save could no longer " +
			"restore the author's own lines")
	}
	out := mustBytes(t, sc)
	if strings.Contains(out, "[element.doomed]") {
		t.Fatalf("the write did not perform the retirement:\n%s", out)
	}
	if !strings.Contains(out, "; the author's note about this one") {
		t.Fatalf("the surviving element lost its comment:\n%s", out)
	}
	back, err := ParseSidecar([]byte(out))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if _, resurrected := back.FindElement("doomed"); resurrected {
		t.Fatal("the deleted element came back out of the file")
	}
}

// TestUnretiringRestoresTheFileByteForByte is the property the deferral exists
// for. An immediate removal would pass every model-level assertion and fail this
// one, because the writer would re-emit the section canonically, at the end of
// the file, in place of the author's own lines.
func TestUnretiringRestoresTheFileByteForByte(t *testing.T) {
	sc := mustParseSidecar(t, retireFixture)
	before := mustBytes(t, sc)

	deleted := sc.Elements[1]
	sc.Elements = sc.Elements[:1]
	sc.RetireElementSection(deleted.ID)
	// ...and the user changes their mind.
	sc.Elements = append(sc.Elements, deleted)
	sc.UnretireElementSection(deleted.ID)

	if got := mustBytes(t, sc); got != before {
		t.Fatalf("undoing a delete did not restore the file byte-for-byte:\n--- before ---\n%s\n--- after ---\n%s",
			before, got)
	}
}

// TestRetiringASectionTheFileNeverHadIsANoOp: an element authored this session
// has no lines yet, and refusing to record one is also what BOUNDS the list.
func TestRetiringASectionTheFileNeverHadIsANoOp(t *testing.T) {
	sc := mustParseSidecar(t, retireFixture)
	if sc.RetireElementSection("never_existed") {
		t.Error("retiring a section the file never carried reported that it had work to do")
	}
	if n := len(sc.retire); n != 0 {
		t.Errorf("the pending list holds %d ids for a section that does not exist", n)
	}
	if got := mustBytes(t, sc); !strings.Contains(got, "[element.doomed]") {
		t.Error("a no-op retirement removed something anyway")
	}
}

// TestTheRetireListIsBoundedAndFailsSafe is hard rule 4 for this list, and it
// pins WHICH WAY it fails at the bound. Past ElementRetireCap the removal is
// performed on the spot instead of recorded: the FILE stays correct and only the
// undo's byte-exactness is given up, which is the right thing to lose. A list
// that simply stopped recording would leak the section back into the theme —
// the exact bug the retirement exists to fix.
func TestTheRetireListIsBoundedAndFailsSafe(t *testing.T) {
	sc := NewSidecar()
	// Sections without model elements, so the document can carry more of them than
	// ElementCap allows a theme to hold. This is the only way to reach the bound.
	ids := make([]string, ElementRetireCap+1)
	for i := range ids {
		ids[i] = "e" + strconv.Itoa(i)
		if err := sc.doc.Set(secElementPrefix+ids[i], "kind", "shape"); err != nil {
			t.Fatalf("staging section %d: %v", i, err)
		}
	}
	for i, id := range ids {
		if !sc.RetireElementSection(id) {
			t.Fatalf("retiring %s (the %dth) reported nothing to do", id, i)
		}
	}
	if n := len(sc.retire); n > ElementRetireCap {
		t.Fatalf("the pending list holds %d ids, cap is %d", n, ElementRetireCap)
	}
	// The one past the bound went NOW rather than being dropped on the floor.
	last := secElementPrefix + ids[len(ids)-1]
	if _, still := sc.doc.Get(last, "kind"); still {
		t.Fatalf("[%s] was neither recorded nor removed — past the cap the section leaks back "+
			"into the theme, which is the bug the retirement exists to fix", last)
	}
	out := mustBytes(t, sc)
	for _, id := range ids {
		if strings.Contains(out, "["+secElementPrefix+id+"]") {
			t.Fatalf("[%s%s] survived the write", secElementPrefix, id)
		}
	}
}

// TestARetirementDoesNotSurviveItsOwnFlush: the list is spent by the write, so a
// second save cannot delete a section the user has since re-authored under the
// same id.
func TestARetirementDoesNotSurviveItsOwnFlush(t *testing.T) {
	sc := mustParseSidecar(t, retireFixture)
	sc.Elements = sc.Elements[:1]
	sc.RetireElementSection("doomed")
	mustBytes(t, sc)
	if n := len(sc.retire); n != 0 {
		t.Fatalf("the write left %d retirements pending", n)
	}
	// Re-author an element under the retired id and save twice.
	sc.Elements = append(sc.Elements, Element{ID: "doomed", Kind: ElemShape, Shape: "pill",
		Fill: RGBA{R: 1, G: 2, B: 3, A: 255}})
	mustBytes(t, sc)
	out := mustBytes(t, sc)
	if !strings.Contains(out, "[element.doomed]") {
		t.Fatalf("a re-authored element was deleted by a stale retirement:\n%s", out)
	}
	back, err := ParseSidecar([]byte(out))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	el, ok := back.FindElement("doomed")
	if !ok || el.Shape != "pill" {
		t.Fatalf("the re-authored element read back as %+v (present=%v)", el, ok)
	}
}
