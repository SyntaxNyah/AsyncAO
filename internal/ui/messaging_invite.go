package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// Group invites are explicit now: an incoming invite is queued (not auto-joined)
// and surfaced as a top-centre banner with Accept / Decline, plus a window flash
// and — when you're tabbed away — a desktop toast. Accepting joins the group and
// tells the owner; declining tells the owner to drop you.

const maxPendingInvites = 8

type groupInvite struct {
	gid       uint32
	name      string
	ownerUID  int
	ownerName string
}

// queueGroupInvite records an incoming invite for Accept/Decline and pings. Deduped
// + bounded; ignored if you're already in that group.
func (a *App) queueGroupInvite(gid uint32, name string, ownerUID int, ownerName string) {
	if gid == 0 {
		return
	}
	if _, joined := a.msgGroups[gid]; joined {
		return
	}
	for _, inv := range a.pendingInvites {
		if inv.gid == gid {
			return // already pending
		}
	}
	if len(a.pendingInvites) >= maxPendingInvites {
		return
	}
	a.pendingInvites = append(a.pendingInvites, groupInvite{gid: gid, name: name, ownerUID: ownerUID, ownerName: ownerName})
	a.warnLine = clampLine(ownerName + " invited you to the group \"" + name + "\"")
	a.warnAt = a.now()
	a.ctx.FlashWindow()
	if !a.ctx.WindowFocused() && time.Since(a.lastOSToast) >= osToastMinInterval {
		a.lastOSToast = time.Now()
		showOSToast("AsyncAO — group invite", ownerName+" invited you to \""+name+"\"")
	}
}

// acceptInvite joins the group and tells the owner; opens the new group.
func (a *App) acceptInvite(inv groupInvite) {
	a.applyGroupInvite(inv.gid, inv.name, inv.ownerUID, inv.ownerName)
	if a.sess != nil {
		body := "[AsyncAO joined the group]" +
			courtroom.WireMessage{Kind: courtroom.MsgJoin, GroupID: inv.gid}.EncodeMarker()
		a.sess.SendOOC(a.oocNameOrDefault(), courtroom.PMCommand([]int{inv.ownerUID}, body))
	}
	a.removePendingInvite(inv.gid)
	a.showMessages, a.msgSelGroup, a.msgSel = true, inv.gid, ""
}

// declineInvite drops the invite and tells the owner to remove us.
func (a *App) declineInvite(inv groupInvite) {
	if a.sess != nil {
		body := "[AsyncAO declined the invite]" +
			courtroom.WireMessage{Kind: courtroom.MsgLeave, GroupID: inv.gid}.EncodeMarker()
		a.sess.SendOOC(a.oocNameOrDefault(), courtroom.PMCommand([]int{inv.ownerUID}, body))
	}
	a.removePendingInvite(inv.gid)
}

func (a *App) removePendingInvite(gid uint32) {
	out := a.pendingInvites[:0]
	for _, inv := range a.pendingInvites {
		if inv.gid != gid {
			out = append(out, inv)
		}
	}
	a.pendingInvites = out
}

// drawGroupInviteToast shows the newest pending invite as a top-centre banner with
// Accept / Decline. Non-blocking; drawn over the courtroom only, and only when an
// invite is pending (zero cost otherwise).
func (a *App) drawGroupInviteToast(w, h int32) {
	if len(a.pendingInvites) == 0 {
		return
	}
	c := a.ctx
	inv := a.pendingInvites[len(a.pendingInvites)-1]
	const bw, bh = int32(430), int32(58)
	r := sdl.Rect{X: (w - bw) / 2, Y: 8, W: bw, H: bh}
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	c.LabelClipped(r.X+10, r.Y+8, bw-20, inv.ownerName+" invited you to a group chat:", ColTextDim)
	c.LabelClipped(r.X+10, r.Y+28, bw-170, "\""+inv.name+"\"", ColText)
	if c.Button(sdl.Rect{X: r.X + bw - 156, Y: r.Y + bh - 28, W: 72, H: 22}, "Accept") {
		a.acceptInvite(inv)
	}
	if c.Button(sdl.Rect{X: r.X + bw - 78, Y: r.Y + bh - 28, W: 70, H: 22}, "Decline") {
		a.declineInvite(inv)
	}
}
