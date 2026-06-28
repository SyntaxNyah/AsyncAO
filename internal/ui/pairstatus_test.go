package ui

import (
	"strconv"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestNotePairPartner pins #20 pair tracking: a paired IC message records "char → partner", a solo
// message clears it (so the map reflects the player's CURRENT pairing), matching is case-insensitive,
// and the map stays bounded by pairPartnersCap.
func TestNotePairPartner(t *testing.T) {
	a := testTabApp(t)
	paired := &protocol.ChatMessage{CharName: "Phoenix", Pair: protocol.PairInfo{CharID: 5, Name: "Maya"}}
	solo := &protocol.ChatMessage{CharName: "Phoenix"}

	a.notePairPartner(paired)
	if got := a.pairPartnerOf(&areaPlayer{name: "phoenix"}); got != "Maya" { // case-insensitive lookup
		t.Errorf("pairPartnerOf after paired = %q, want Maya", got)
	}
	a.notePairPartner(solo) // a solo line clears the pairing
	if got := a.pairPartnerOf(&areaPlayer{name: "Phoenix"}); got != "" {
		t.Errorf("a solo message should clear the pair, got %q", got)
	}

	// Bounded: piling on distinct paired speakers can't grow past the cap.
	for i := 0; i < pairPartnersCap+25; i++ {
		a.notePairPartner(&protocol.ChatMessage{CharName: "c" + strconv.Itoa(i), Pair: protocol.PairInfo{CharID: 1, Name: "x"}})
	}
	if len(a.pairPartners) > pairPartnersCap {
		t.Errorf("pairPartners grew to %d, want capped at %d", len(a.pairPartners), pairPartnersCap)
	}
}
