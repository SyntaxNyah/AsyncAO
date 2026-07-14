//go:build !novoice

package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
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

// voiceBuilt is true in this (voice-enabled) build. The -tags novoice stub sets
// it false, which makes the Settings sidebar omit the Voice tab and the search
// skip it. It's a compile-time const, so the default build's settings render
// carries zero added cost.
const voiceBuilt = true

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

// voiceButtonState gives the courtroom Voice button its label + colour so it
// doubles as a transmit indicator: a red "● Voice" while your mic is live, an
// accent "Voice ✓" when you're in voice (mic off), plain green when available.
func (a *App) voiceButtonState() (label string, border sdl.Color) {
	switch {
	case a.voiceJoined && a.voiceMicOn:
		return "● Voice", ColDanger // transmitting
	case a.voiceJoined:
		return "Voice ✓", ColAccent // in voice, not talking
	default:
		return "Voice", ColTierGreen // available here
	}
}

func (a *App) voicePanelRect(w, h int32) sdl.Rect {
	if r, ok := a.seedPanelFromSlot(&a.voiceWin, slotPanelVoice, voicePanelDefW, voicePanelDefH, voicePanelMinW, voicePanelMinH, w, h); ok {
		return r
	}
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

// voiceJoinChannel joins the server's voice channel and starts the live audio
// engine (opt-in: this is the only place the audio path turns on). Engine start
// is fail-safe — if no audio device is available we stay joined for PRESENCE
// (you still appear in voice and see others), just without sound.
func (a *App) voiceJoinChannel() {
	if a.sess == nil || a.voiceJoined {
		return
	}
	a.sess.VoiceJoin()
	a.voiceJoined = true
	if a.voiceAudio == nil {
		eng, err := newVoiceEngine(func(b64 string) {
			if a.sess != nil {
				a.sess.VoiceFrame(b64) // single send path: main loop only
			}
		}, a.d.Prefs.VoiceInput(), a.d.Prefs.VoiceOutVol())
		if err == nil {
			a.voiceAudio = eng
		}
	}
}

// voiceLeaveChannel leaves voice and tears down the audio engine.
func (a *App) voiceLeaveChannel() {
	if a.sess != nil {
		if a.voiceMicOn {
			a.sess.VoiceSpeak(false)
		}
		a.sess.VoiceLeave()
	}
	a.voiceMicOn = false
	a.voiceJoined = false
	a.stopVoiceAudio()
}

// voiceSetMic flips transmit on/off — both the engine (capture) and the wire
// (VS_SPEAK so peers see your speaking state).
func (a *App) voiceSetMic(on bool) {
	a.voiceMicOn = on
	if a.voiceAudio != nil {
		a.voiceAudio.setMic(on)
	}
	if a.sess != nil {
		a.sess.VoiceSpeak(on)
	}
}

// stopVoiceAudio closes the engine (devices + codecs). Safe any time; called on
// leave, disconnect and teardown.
func (a *App) stopVoiceAudio() {
	if a.voiceAudio != nil {
		a.voiceAudio.close()
		a.voiceAudio = nil
	}
}

// voicePump drives capture→encode→send and decode→mix→play once per frame, plus the
// settings mic-test loopback. No-op (a couple of nil checks) unless the audio engine
// or a mic test is running. The mic test auto-stops the moment we leave the Voice
// settings tab — so the test mic never stays open in the background (#84).
func (a *App) voicePump() {
	a.voiceAudio.pump()
	if a.micTest != nil && (a.screen != ScreenSettings || settings.tab != tabVoice) {
		a.stopMicTest()
	}
	a.micTestPump()
}

// voiceOnAudio feeds one inbound peer frame (VS_AUDIO) to the mixer.
func (a *App) voiceOnAudio(uid int, b64 string) { a.voiceAudio.pushRemote(uid, b64) }

// voiceReconcilePeers drops decoders for peers who have left voice (VS_LEAVE /
// VS_PEERS), keeping the engine's peer set in step with the session's.
func (a *App) voiceReconcilePeers() {
	if a.voiceAudio == nil || a.sess == nil {
		return
	}
	live := map[int]bool{}
	for _, uid := range a.sess.VoicePeers() {
		live[uid] = true
	}
	for uid := range a.voiceAudio.peers {
		if !live[uid] {
			a.voiceAudio.dropPeer(uid)
		}
	}
}

// voicePeerChar resolves a voice peer uid to its CHARACTER name (for the icon +
// profile lookup) via the live roster — "" when unknown (spectator / not in this
// area's roster snapshot).
func (a *App) voicePeerChar(uid int) string {
	want := strconv.Itoa(uid)
	for _, p := range a.rosterView() {
		if p.uid == want {
			return strings.TrimSpace(p.name)
		}
	}
	return ""
}

// drawVoicePanel paints the floating voice panel. pressed is the shared floatWin
// press edge from drawFloatingPanels.
func (a *App) drawVoicePanel(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.voiceWin.dragging || a.voiceWin.resizing // detect the drag/resize-end frame for slot persistence
	r := a.voicePanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Voice", ColText)
	if a.voiceJoined && a.voiceMicOn { // your mic is live → a red transmit indicator
		c.Label(r.X+pad+58, r.Y+6, "● MIC LIVE", ColDanger)
	}
	closeB := sdl.Rect{X: r.X + r.W - 70 - pad, Y: r.Y + 3, W: 70, H: btnH}
	if c.Button(closeB, "Close") {
		a.showVoice = false
		return
	}
	a.floatWinDrag(&a.voiceWin, sdl.Rect{X: r.X, Y: r.Y, W: closeB.X - r.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.voiceWin, grip, r, voicePanelMinW, voicePanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.voiceWin.dragging && !a.voiceWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelVoice, r, w, h)
	}

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
			a.voiceJoinChannel()
		}
		c.LabelClipped(x+140, y+5, right-(x+140), "Join the server's voice channel.", ColTextDim)
		y += btnH + 6
	} else {
		if c.Button(sdl.Rect{X: x, Y: y, W: 130, H: btnH}, "Leave voice") {
			a.voiceLeaveChannel()
		}
		canTalk := a.voiceAudio.canTalk()
		talk := "Talk: off"
		if a.voiceMicOn {
			talk = "Talk: ON"
		}
		if !canTalk {
			talk = "No mic"
		}
		tw := right - (x + 140)
		if tw < 90 {
			tw = 90
		}
		if c.Button(sdl.Rect{X: x + 140, Y: y, W: tw, H: btnH}, talk) && canTalk {
			a.voiceSetMic(!a.voiceMicOn)
		}
		y += btnH + 6
		// Output volume + mute (listen controls).
		muteLbl := "Mute others"
		if a.voiceAudio != nil && a.voiceAudio.muted {
			muteLbl = "Unmute"
		}
		if c.Button(sdl.Rect{X: x, Y: y, W: 130, H: btnH}, muteLbl) && a.voiceAudio != nil {
			a.voiceAudio.setMuted(!a.voiceAudio.muted)
		}
		vol := int32(100)
		if a.voiceAudio != nil {
			vol = int32(a.voiceAudio.outVol)
		}
		if nv := c.Slider("voiceOutVol", sdl.Rect{X: x + 140, Y: y, W: right - (x + 140), H: btnH}, vol, 100); nv != vol && a.voiceAudio != nil {
			a.voiceAudio.setOutVol(int(nv))
			a.d.Prefs.SetVoiceOutVol(int(nv)) // remember across sessions
		}
		y += btnH + 6
	}
	// Status line: presence-only when the engine couldn't start (no audio device).
	if a.voiceJoined && a.voiceAudio == nil {
		c.LabelClipped(x, y, right-x, "Audio unavailable on this device — presence only (no sound).", ColTierYellow)
		y += 18
	}

	// Peer list with live speaking indicators.
	caps := a.sess.VoiceCapsInfo()
	hdr := "In voice (" + strconv.Itoa(a.sess.VoicePeerCount()) + ")"
	if caps.MaxPeers > 0 {
		hdr += " / max " + strconv.Itoa(caps.MaxPeers)
	}
	c.Label(x, y, hdr, ColTextDim)
	// Voice-latency chip: the output buffer depth (ms) we're adding — grows when the
	// adaptive jitter buffer deepens on a shaky connection, shrinks when it's smooth.
	if a.voiceJoined && a.voiceAudio != nil {
		lat := a.voiceAudio.bufferMs()
		latCol := ColTierGreen
		if lat > 120 {
			latCol = ColTierYellow
		}
		if lat > 240 {
			latCol = ColDanger
		}
		c.Label(right-90, y, "~"+strconv.Itoa(lat)+" ms", latCol)
	}
	y += 18
	peers := a.sess.VoicePeers()
	sort.Ints(peers)
	me := a.myUID()
	const vIcon = int32(20) // char-icon thumbnail size per row
	for i, uid := range peers {
		if y > r.Y+r.H-vIcon {
			break
		}
		speaking := a.sess.VoiceIsSpeaking(uid)
		dotCol := ColTextDim
		if speaking {
			dotCol = ColAccent // currently transmitting → the dot lights up
		}
		c.Label(x, y+4, "•", dotCol)
		// Character icon: straight from the URL-keyed texture Store (no index→page
		// cache, so no reorder hazard); a paced fetch + placeholder on a miss. A
		// speaking peer gets an accent frame so you can see who's talking at a glance.
		ch := a.voicePeerChar(uid)
		iconR := sdl.Rect{X: x + 14, Y: y, W: vIcon, H: vIcon}
		c.Fill(iconR, ColPanelHi)
		if ch != "" {
			base := a.urls.CharIcon(ch)
			if page, ok := a.d.Store.Get(base); ok && len(page.Frames) > 0 {
				_ = c.Ren.Copy(page.Frames[0], nil, &iconR)
			} else {
				a.demandAsset(&a.voiceIconAsk, len(peers), i, base, assets.AssetTypeCharIcon) // AssetType: CharIcon
			}
		}
		if speaking {
			c.Border(iconR, ColAccent)
		}
		// [UID] name — the username + id the user asked to see; "(you)" for self.
		name := a.voicePeerName(uid)
		label := "[" + strconv.Itoa(uid) + "] " + name
		if uid == me {
			label += " (you)"
		}
		// Custom profile (pronouns · tagline) from the existing cross-client profile
		// channel — keyed by the character name, falling back to the display name.
		if a.room != nil {
			key := ch
			if key == "" {
				key = name
			}
			if p, ok := a.room.RemoteProfile(key); ok {
				if extra := strings.TrimSpace(strings.TrimSpace(p.Pronouns) + "  " + strings.TrimSpace(p.Tag)); extra != "" {
					label += "  ·  " + extra
				}
			}
		}
		tx := x + 14 + vIcon + 6
		c.LabelClipped(tx, y+4, right-tx, label, ColText)
		y += vIcon + 4
	}
	if len(peers) == 0 {
		c.LabelClipped(x, y, right-x, "Nobody's in voice yet.", ColTextDim)
	}
}
