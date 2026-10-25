package ui

// eggs is the creator-easter-egg detection cache: the displayed text the last
// scan ran for and the egg kind it resolved. It is per session — it parks with
// the tab — because the text it caches comes from that session's displayed
// message, so a settled frame is one string compare and never a rescan.
type eggs struct {
	msg  string
	kind uint8
}

// refresh is the compare-and-store guard: a new text rescans (creatorEgg) and
// stores both, returning true; the same text is a no-op returning false.
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
