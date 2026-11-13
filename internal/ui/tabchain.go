package ui

// Tab order for the chat fields. AO2-Client never calls setTabOrder, so its chain is Qt's widget construction order
// (courtroom.cpp, lines 187-219): showname, IC, OOC, OOC name, music search. fieldSeq is draw order, which differs.
// Ranked by role, not id, because a role's id varies by layout. A role with nothing drawn has no place in the chain,
// since focusing an undrawn field would swallow keystrokes.
const (
	tabRankShowname = iota // AO2 ui_ic_chat_name
	tabRankIC              // AO2 ui_ic_chat_message
	tabRankOOC             // AO2 ui_ooc_chat_message
	tabRankOOCName         // AO2 ui_ooc_chat_name
	tabChainRanks          // count of roles — a loop bound, never a rank
)

// tabRankNone marks fields outside the chat chain; they keep draw order.
const tabRankNone = -1

// tabChainSpanCap bounds the fieldSeq window the reorder may rewrite and sizes its stack scratch, keeping it
// allocation-free. The widest real frame spans 6 fields; past the cap, draw order stands.
const tabChainSpanCap = 16

// tabRank maps a field id to its AO2 chat-chain role, or tabRankNone.
func tabRank(id string) int {
	switch id {
	case "icshownameov": // classic drawICInputRow / themed drawCourtroomThemed
		return tabRankShowname
	case icFieldID: // "ic" — the IC message box in both layouts
		return tabRankIC
	case "oocmsg", "ooc": // default OOC box / Legacy bottom bar / themed OOC input
		return tabRankOOC
	case "oocname2", "oocname": // default OOC box / themed OOC name
		return tabRankOOCName
	}
	return tabRankNone
}

// orderTabChain rewrites seq in place so its chat fields form one block in role order, starting where the first was;
// non-chat fields between them slide just after the block. Draw order breaks ties within a role: the Legacy layout can
// draw two OOC inputs editing a.oocInput, and the first wins. Idempotent, because BeginFrame may cycle several times
// against the same fieldSeq.
func orderTabChain(seq []string) {
	first, last, n := -1, -1, 0
	for i, id := range seq {
		if tabRank(id) == tabRankNone {
			continue
		}
		if first < 0 {
			first = i
		}
		last, n = i, n+1
	}
	if n < 2 {
		return // 0 or 1 chat field on screen: there is no order to impose
	}
	span := last - first + 1
	if span > tabChainSpanCap {
		return // past the named cap: leave draw order exactly as it was
	}
	var win [tabChainSpanCap]string
	w := 0
	for r := 0; r < tabChainRanks; r++ { // role-major; draw order within a role
		for i := first; i <= last; i++ {
			if tabRank(seq[i]) == r {
				win[w] = seq[i]
				w++
			}
		}
	}
	for i := first; i <= last; i++ { // then the interleaved non-chat fields
		if tabRank(seq[i]) == tabRankNone {
			win[w] = seq[i]
			w++
		}
	}
	copy(seq[first:last+1], win[:w])
}
