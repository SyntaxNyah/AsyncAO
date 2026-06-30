package courtroom

// Typing indicator (#3): an opt-in, cross-client "X is typing…" signal between AsyncAO
// users. Unlike the other zero-width channels — sprite style, profile, status, effects,
// reaction, messaging — which ride an IC message you were sending ANYWAY, typing happens
// BEFORE you send, so it has no carrier message. It therefore rides a dedicated OOC (CT)
// message whose entire TEXT is the zero-width frame; the sender's identity is the OOC
// name field. An AsyncAO receiver detects the frame, shows the indicator, and SUPPRESSES
// the line from the OOC log. Standard AO2/webAO clients have no idea about the channel,
// so they'd see an empty-looking OOC line — the unavoidable cost of an out-of-band signal,
// which is why the whole feature is opt-in and OFF by default.
//
// Because it's extra, unsolicited traffic, the SENDER (the UI) throttles hard — at most
// one pulse every few seconds while you type — to stay under server OOC flood limits. With
// the pref off, the client emits ZERO typing traffic.

const (
	// typingFrameMagic is the leading payload byte of a typing frame, distinct from the
	// sprite-style version (1), profile (0x70), status (0x71), effects (0x72), reaction
	// (0x73) and messaging (0x74) magics so the shared zero-width scanner tells them apart.
	typingFrameMagic  = 0x75
	typingWireVersion = 1
)

// EncodeTypingMarker returns the zero-width run that is the ENTIRE OOC text of a typing
// pulse (the sender's identity travels in the CT name field, not here).
func EncodeTypingMarker() string {
	return packZeroWidth([]byte{typingFrameMagic, typingWireVersion})
}

// IsTypingMarker reports whether an OOC line's text carries a typing frame. The receiver
// uses it to show the indicator and DROP the line from the OOC log (a typing pulse is
// never real OOC chat). A frame from a newer peer is still recognised by its magic.
func IsTypingMarker(text string) bool {
	if !hasMarker(text) {
		return false
	}
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) >= 2 && fr[0] == typingFrameMagic && fr[1] == typingWireVersion {
			return true
		}
	}
	return false
}
