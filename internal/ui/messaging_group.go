package ui

// AsyncAO group chats: client-side groups carried over /pm fan-out — there is NO
// server-side group. The owner mints a random group id and invites AsyncAO players;
// each message /pm's every member with a group control frame, so it stays private to
// members and never touches the room. Roles are spoof-proof: the server attributes
// every PM to its real sender (courtroom.ParsePMSender), so a "kick" is honoured only
// when it actually came from the group's owner. Membership is reconstructed from the
// control frames (invite / join / leave / kick) as they arrive.

const (
	maxGroups       = 16  // bound the group store
	maxGroupMembers = 50  // /pm fan-out cap (server rate-limit friendliness)
	maxGroupLines   = 200 // per-group history cap
)

type msgMember struct {
	uid  int
	name string
}

type groupLine struct {
	from   string
	text   string
	fromMe bool
}

type msgGroup struct {
	id       uint32
	name     string
	ownerUID int
	members  []msgMember
	lines    []groupLine
}

func (g *msgGroup) hasMember(uid int) bool {
	for _, m := range g.members {
		if m.uid == uid {
			return true
		}
	}
	return false
}

func (g *msgGroup) addMember(uid int, name string) {
	if uid == 0 || g.hasMember(uid) || len(g.members) >= maxGroupMembers {
		return
	}
	g.members = append(g.members, msgMember{uid: uid, name: name})
}

func (g *msgGroup) removeMember(uid int) {
	out := g.members[:0]
	for _, m := range g.members {
		if m.uid != uid {
			out = append(out, m)
		}
	}
	g.members = out
}

func (g *msgGroup) appendLine(from, text string, fromMe bool) {
	g.lines = append(g.lines, groupLine{from: from, text: text, fromMe: fromMe})
	if len(g.lines) > maxGroupLines { // keep the newest; fresh backing frees the old head
		g.lines = append([]groupLine(nil), g.lines[len(g.lines)-maxGroupLines:]...)
	}
}

// myUID is our server-assigned UID, or 0 when not in a session.
func (a *App) myUID() int {
	if a.sess == nil {
		return 0
	}
	return a.sess.PlayerID
}

// applyGroupInvite records a group we were invited to (owner = the inviter), adding
// the owner and us as the initial members. Idempotent; bounded.
func (a *App) applyGroupInvite(id uint32, name string, ownerUID int, ownerName string) {
	g := a.ensureGroup(id)
	if g == nil {
		return
	}
	if g.name == "" {
		g.name = name
	}
	if g.ownerUID == 0 {
		g.ownerUID = ownerUID
	}
	g.addMember(ownerUID, ownerName)
	g.addMember(a.myUID(), a.oocNameOrDefault())
}

// applyGroupJoin adds a member who announced they joined a group we're in.
func (a *App) applyGroupJoin(id uint32, uid int, name string) {
	if g := a.msgGroups[id]; g != nil {
		g.addMember(uid, name)
	}
}

// applyGroupText files a received group message (the sender is implicitly a member).
// Ignored when we don't know the group (no invite seen).
func (a *App) applyGroupText(id uint32, fromUID int, fromName, text string) {
	g := a.msgGroups[id]
	if g == nil {
		return
	}
	g.addMember(fromUID, fromName)
	g.appendLine(fromName, text, false)
}

// applyGroupKick removes a member — but ONLY when the kick came from the group's
// owner (byUID == ownerUID); the server-attributed sender makes this unforgeable. If
// we are the target, the group is dropped locally.
func (a *App) applyGroupKick(id uint32, byUID, targetUID int) {
	g := a.msgGroups[id]
	if g == nil || byUID != g.ownerUID {
		return
	}
	if targetUID == a.myUID() {
		delete(a.msgGroups, id)
		return
	}
	g.removeMember(targetUID)
}

// applyGroupLeave removes a member who announced they left.
func (a *App) applyGroupLeave(id uint32, uid int) {
	if g := a.msgGroups[id]; g != nil {
		g.removeMember(uid)
	}
}

// ensureGroup returns the group for id, creating it (bounded) if absent. nil at the cap.
func (a *App) ensureGroup(id uint32) *msgGroup {
	if a.msgGroups == nil {
		a.msgGroups = map[uint32]*msgGroup{}
	}
	if g := a.msgGroups[id]; g != nil {
		return g
	}
	if len(a.msgGroups) >= maxGroups {
		return nil
	}
	g := &msgGroup{id: id}
	a.msgGroups[id] = g
	return g
}
