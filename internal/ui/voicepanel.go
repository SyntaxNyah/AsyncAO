package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// The Voice panel — a Nyathena-gated, non-blocking floating panel for the
// server's VS_* voice channel: Join / Leave, the live peer list with speaking
// indicators, and your own speak (push-to-talk-style) toggle. It rides the
// server's voice relay (courtroom/voice.go); the option appears ONLY when the
// server advertises voice (VS_CAPS — i.e. Nyathena), per the "show the voice
// option only for Nyathena, like a server wire" requirement.
//
// Live MIC AUDIO (capture + playback) is the next slice — the Opus codec
// (internal/voice) and this signaling are already in place, so it's purely
// additive. Until then this shares presence + speaking state (which the server
// relays), so two AsyncAO clients can see each other in voice.

// voiceExtraLabel is the Extras-menu entry; the draw loops skip it when voice
// isn't offered, so it's hidden on non-Nyathena servers.
const voiceExtraLabel = "Voice (Nyathena)"

const (
	voicePanelDefW = 320
	voicePanelDefH = 300
	voicePanelMinW = 240
	voicePanelMinH = 180
)

// voiceOfferable reports whether to show any voice surface: a live session whose
// server advertised voice (VS_CAPS.enabled). This single check is the gate.
func (a *App) voiceOfferable() bool {
	return a.sess != nil && a.sess.VoiceAvailable()
}

func (a *App) toggleVoice() { a.showVoice = !a.showVoice }

func (a *App) voicePanelRect(w, h int32) sdl.Rect {
	if !a.voiceWin.placed {
		dw := clampI32(voicePanelDefW, voicePanelMinW, w-2*floatWinMargin)
		dh := clampI32(voicePanelDefH, voicePanelMinH, h-2*floatWinMargin)
		return sdl.Rect{X: w - dw - floatWinMargin, Y: floatTitleH, W: dw, H: dh}
	}
	return a.voiceWin.rect(voicePanelDefW, voicePanelDefH, voicePanelMinW, voicePanelMinH, w, h)
}

// voicePeerName resolves a voice peer uid to a display name via the live roster,
// falling back to "UID <n>" when the roster doesn't have it.
func (a *App) voicePeerName(uid int) string {
	want := strconv.Itoa(uid)
	for _, p := range a.rosterView() {
		if p.uid != want {
			continue
		}
		if n := strings.TrimSpace(p.showname); n != "" {
			return n
		}
		if n := strings.TrimSpace(p.name); n != "" {
			return n
		}
		if n := strings.TrimSpace(p.ooc); n != "" {
			return n
		}
	}
	return "UID " + want
}

// drawVoicePanel paints the floating voice panel. pressed is the shared floatWin
// press edge from drawFloatingPanels.
func (a *App) drawVoicePanel(w, h int32, pressed *bool) {
	c := a.ctx
	r := a.voicePanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Voice", ColText)
	closeB := sdl.Rect{X: r.X + r.W - 70 - pad, Y: r.Y + 3, W: 70, H: btnH}
	if c.Button(closeB, "Close") {
		a.showVoice = false
		return
	}
	a.floatWinDrag(&a.voiceWin, sdl.Rect{X: r.X, Y: r.Y, W: closeB.X - r.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.voiceWin, grip, r, voicePanelMinW, voicePanelMinH, pressed)
	a.drawResizeGrip(grip)

	x := r.X + pad
	y := r.Y + floatTitleH + 8
	right := r.X + r.W - pad

	if !a.voiceOfferable() {
		c.LabelClipped(x, y, right-x, "Voice chat isn't available on this server.", ColTextDim)
		return
	}

	// Join / Leave + (when joined) your speak toggle.
	if !a.voiceJoined {
		if c.Button(sdl.Rect{X: x, Y: y, W: 130, H: btnH}, "Join voice") {
			a.sess.VoiceJoin()
			a.voiceJoined = true
		}
		c.LabelClipped(x+140, y+5, right-(x+140), "Join the server's voice channel.", ColTextDim)
	} else {
		if c.Button(sdl.Rect{X: x, Y: y, W: 130, H: btnH}, "Leave voice") {
			if a.voiceMicOn {
				a.sess.VoiceSpeak(false)
				a.voiceMicOn = false
			}
			a.sess.VoiceLeave()
			a.voiceJoined = false
		}
		talk := "Talk: off"
		if a.voiceMicOn {
			talk = "Talk: ON"
		}
		tw := right - (x + 140)
		if tw < 90 {
			tw = 90
		}
		if c.Button(sdl.Rect{X: x + 140, Y: y, W: tw, H: btnH}, talk) {
			a.voiceMicOn = !a.voiceMicOn
			a.sess.VoiceSpeak(a.voiceMicOn)
		}
	}
	y += btnH + 6
	c.LabelClipped(x, y, right-x, "Live mic audio is coming; for now this shares your speaking state.", ColTextDim)
	y += 18

	// Peer list with live speaking indicators.
	caps := a.sess.VoiceCapsInfo()
	hdr := "In voice (" + strconv.Itoa(a.sess.VoicePeerCount()) + ")"
	if caps.MaxPeers > 0 {
		hdr += " / max " + strconv.Itoa(caps.MaxPeers)
	}
	c.Label(x, y, hdr, ColTextDim)
	y += 18
	peers := a.sess.VoicePeers()
	sort.Ints(peers)
	me := a.myUID()
	for _, uid := range peers {
		if y > r.Y+r.H-18 {
			break
		}
		col := ColTextDim
		if a.sess.VoiceIsSpeaking(uid) {
			col = ColAccent // currently transmitting
		}
		name := a.voicePeerName(uid)
		if uid == me {
			name += " (you)"
		}
		c.Label(x, y, "•", col)
		c.LabelClipped(x+16, y, right-(x+16), name, ColText)
		y += 18
	}
	if len(peers) == 0 {
		c.LabelClipped(x, y, right-x, "Nobody's in voice yet.", ColTextDim)
	}
}
