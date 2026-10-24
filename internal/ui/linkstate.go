package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// linkState owns link state.
type linkState struct {
	// --- connection / session ---
	conn *protocol.Conn
	sess *courtroom.Session
	room *courtroom.Courtroom
	urls courtroom.URLBuilder
	// #130 CM/mod dashboard: per-server software override (SoftwareUnknown = auto-detect from
	// the ID packet). Parks per tab; a fresh connect's new sessionState resets it to auto.
	cmSoftwareOverride courtroom.ServerSoftware
	// autoLoginTried latches the auto-login to fire AT MOST ONCE per session.
	// EventReady fires on every DONE packet, and some servers re-send DONE
	// mid-session (the WAP/Akashi fork, area changes) — without this the saved
	// login re-queued on every one and spammed OOC. resetSessionState clears it,
	// so a genuine reconnect logs in again. See autoLoginOnReady.
	autoLoginTried bool
	// Voice chat membership (per-server, so it parks per tab): voiceJoined = we've
	// sent VS_JOIN; voiceMicOn = our speak state is on (announced via VS_SPEAK).
	voiceJoined bool
	voiceMicOn  bool
	// voiceCapsAnnounced: we've posted the one-time "this area supports voice" OOC
	// hint this session (so it confirms VS_CAPS arrived + points at the button).
	voiceCapsAnnounced bool
}
