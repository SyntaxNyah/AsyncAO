package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Group-chat visuals: character icons on every message/member/DM surface, a stable per-person name colour, bubbles with
// timestamps drawn newest-first from the bottom, a per-group accent chip and unread counts. All local rendering.

const (
	// msgBubbleIconPx / msgBubbleLineH size a thread bubble's icon and text rows.
	msgBubbleIconPx = int32(20)
	msgBubbleLineH  = int32(17)
	// msgBubbleMaxLines caps one bubble's wrapped text so a single message cannot fill the box.
	msgBubbleMaxLines = 6
	// msgIconAskCap bounds the paced-demand map (hard rule 4).
	msgIconAskCap = 128
	// msgNameSat / msgNameVal are the fixed name-colour values for this panel, independent of the IC-log pref.
	msgNameSat, msgNameVal = 0.55, 0.95
)

// rosterCharByUID resolves a live UID to its character folder, or "" when the player isn't in the roster.
func (a *App) rosterCharByUID(uid int) string {
	if uid == 0 {
		return ""
	}
	want := strconv.Itoa(uid)
	roster := a.rosterView()
	for i := range roster {
		if roster[i].uid == want {
			return strings.TrimSpace(roster[i].name)
		}
	}
	return ""
}

// rosterCharByName resolves a display name (showname or char) to its character
// folder for the DM header icon.
func (a *App) rosterCharByName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	roster := a.rosterView()
	for i := range roster {
		if strings.EqualFold(strings.TrimSpace(roster[i].name), name) ||
			strings.EqualFold(strings.TrimSpace(roster[i].showname), name) {
			return strings.TrimSpace(roster[i].name)
		}
	}
	return ""
}

// demandMsgIcon paces a char-icon fetch: one ask per base per charIconRetryInterval. The map resets past its cap, which
// is harmless because the stamps only pace retries.
func (a *App) demandMsgIcon(base string) {
	if base == "" {
		return
	}
	if a.msgIconAsk == nil {
		a.msgIconAsk = make(map[string]time.Time, msgIconAskCap)
	}
	if time.Since(a.msgIconAsk[base]) < charIconRetryInterval {
		return
	}
	if len(a.msgIconAsk) >= msgIconAskCap {
		a.msgIconAsk = make(map[string]time.Time, msgIconAskCap)
	}
	a.msgIconAsk[base] = time.Now()
	a.d.Manager.Prefetch(base, assets.AssetTypeCharIcon, network.PriorityLow) // AssetType: CharIcon
}

// drawMsgCharIcon paints a small char icon for char (resolved folder) into cell, falling back to an initial disc in the
// person's name colour. It reads the store directly because these panels draw only while open.
func (a *App) drawMsgCharIcon(char, displayName string, cell sdl.Rect) {
	c := a.ctx
	if char != "" {
		base := a.urls.CharIcon(char)
		if page, ok := a.d.Store.Get(base); ok && len(page.Frames) > 0 {
			_ = c.Ren.Copy(page.Frames[0], nil, &cell)
			c.Border(cell, ColPanelHi)
			return
		}
		a.demandMsgIcon(base)
	}
	// Fallback: a coloured disc with the display initial (offline / spectator).
	col := nameColor(displayName, msgNameSat, 0.55)
	c.Fill(cell, col)
	initial := strings.ToUpper(displayName)
	if initial != "" {
		c.LabelClipped(cell.X+5, cell.Y+2, cell.W-6, initial[:1], ColText)
	}
	c.Border(cell, ColPanelHi)
}

// groupChipColor is a group's stable accent (hash of its id) for the list chip and header.
func groupChipColor(id uint32) sdl.Color {
	r, g, b := hsvToRGB(float64(id%360)/360, 0.6, 0.95)
	return sdl.Color{R: r, G: g, B: b, A: 255}
}

// msgStamp renders a bubble timestamp (HH:MM, local).
func msgStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.Format("15:04")
}

// drawChatBubbles renders a thread as bubbles, newest at the bottom, walking backwards until the box is full so
// variable-height bubbles work. Mine sit right-aligned and accent-tinted; others left with icon and coloured name.
// lineAt returns (from, text, fromMe, char, at) for entry i.
func (a *App) drawChatBubbles(box sdl.Rect, n int, lineAt func(i int) (string, string, bool, string, time.Time)) {
	c := a.ctx
	if n == 0 {
		c.LabelClipped(box.X+6, box.Y+6, box.W-12, "No messages yet — say hi below.", ColTextDim)
		return
	}
	clipPrev, clipHad := c.pushClip(box)
	defer c.popClip(clipPrev, clipHad)
	wrapW := box.W - msgBubbleIconPx - 40
	if wrapW < 60 {
		wrapW = 60
	}
	y := box.Y + box.H - 6
	for i := n - 1; i >= 0 && y > box.Y; i-- {
		from, text, mine, char, at := lineAt(i)
		lines := c.WrapText(text, wrapW, msgBubbleMaxLines)
		if len(lines) == 0 {
			lines = []string{""}
		}
		bubbleH := int32(len(lines))*msgBubbleLineH + 20
		y -= bubbleH + 6
		bw := int32(0)
		for _, ln := range lines {
			if w := c.TextWidth(ln); w > bw {
				bw = w
			}
		}
		if nw := c.TextWidth(from) + 44; nw > bw { // name row + stamp never overflow the bubble
			bw = nw
		}
		bw += 16
		var bubble sdl.Rect
		var fill sdl.Color
		if mine {
			bubble = sdl.Rect{X: box.X + box.W - 6 - bw, Y: y, W: bw, H: bubbleH}
			fill = sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 46}
		} else {
			bubble = sdl.Rect{X: box.X + 6 + msgBubbleIconPx + 4, Y: y, W: bw, H: bubbleH}
			fill = sdl.Color{R: ColPanelHi.R, G: ColPanelHi.G, B: ColPanelHi.B, A: 200}
			a.drawMsgCharIcon(char, from, sdl.Rect{X: box.X + 4, Y: y, W: msgBubbleIconPx, H: msgBubbleIconPx})
		}
		c.Fill(bubble, fill)
		nameCol := ColAccent
		if !mine {
			nameCol = nameColor(from, msgNameSat, msgNameVal)
		}
		a.labelName(bubble.X+6, bubble.Y+2, bubble.W-46, from, nameCol) // CJK-safe name
		if st := msgStamp(at); st != "" {
			c.Label(bubble.X+bubble.W-c.TextWidth(st)-6, bubble.Y+2, st, ColTextDim)
		}
		ty := bubble.Y + 18
		for _, ln := range lines {
			a.labelName(bubble.X+6, ty, bubble.W-12, ln, ColText) // CJK-safe message body
			ty += msgBubbleLineH
		}
	}
}

// groupUnreadLabel decorates a chat-list label with its unread count.
func groupUnreadLabel(name string, unread int) string {
	if unread <= 0 {
		return "# " + name
	}
	return fmt.Sprintf("# %s (%d)", name, unread)
}
