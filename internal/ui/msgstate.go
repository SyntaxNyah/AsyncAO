package ui

import "time"

// msgState owns msgs state.
type msgState struct {
	env *env
	// Group Chat panel (messaging): floating, non-blocking, global (not per-session).
	// msgSel = selected conversation (partner char name); msgInput = compose buffer.
	showMessages   bool
	msgSel         string
	msgSelGroup    uint32 // selected group id (0 = a DM is selected via msgSel)
	msgGroupManage bool   // group view is showing the members / invite panel
	msgInput       string
	msgListScroll  int32
	msgGroups      map[uint32]*msgGroup // client-side group chats (keyed by group id)
	pendingInvites []groupInvite        // group invites awaiting Accept / Decline
	msgIconAsk     map[string]time.Time // paced char-icon demands for the messaging surfaces (bounded)
}
