package ui

// eggs is the creator-easter-egg detection cache: the displayed text the last
// scan ran for and the egg kind it resolved. It is per session — it parks with
// the tab — because the text it caches comes from that session's displayed
// message, so a settled frame is one string compare and never a rescan.
//
// It carries no env pointer: it needs no shared state, and its two draw call
// sites reach it through kindOf rather than reading the fields (the component
// owns its state; App methods never touch it directly).
type eggs struct {
	msg  string
	kind uint8
}

// refresh is the compare-and-store guard: a new text rescans (creatorEgg) and
// stores both, returning true; the same text is a no-op returning false.
// drawChatOverlay calls it once per frame with the displayed text, so a settled
// frame is one string compare with no allocation and no rescan. Returning
// whether it rescanned makes the "same text twice ⇒ one scan" behaviour
// testable without SDL (the mixChannels / applyResumeDuck pure-split precedent).
func (e *eggs) refresh(text string) (scanned bool) {
	if text == e.msg {
		return false
	}
	e.msg = text
	e.kind = creatorEgg(text)
	return true
}

// kindOf is the resolved egg kind (eggNone = no effect) — the read the draw
// paths use instead of touching the field.
func (e *eggs) kindOf() uint8 { return e.kind }
