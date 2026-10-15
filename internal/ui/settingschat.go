package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// drawSettingsChat: message timing, typing, casing alerts, callwords, plus the
// friends / ignore / DND / music-history / mod sections — everything from the old
// Audio & Chat tab except the volumes (now their own Audio tab).
func (a *App) drawSettingsChat(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	y = a.settingsSection(y, w, "Group chat")
	gcBtn := a.d.Prefs.Load().GroupChatButton
	if next := c.Checkbox(pad, y, "Show the Group Chat button in the courtroom (ON by default): a main button to open DMs & group chats with other AsyncAO players. It's always in Extras → Group Chat too.", gcBtn); next != gcBtn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				GroupChatButton =

				next

		})
	}
	y += 30

	y = a.settingsSection(y, w, "Text & typing")
	// Hold-to-clear: hold a key (default Backspace, rebindable) to wipe a text
	// box at once instead of deleting char-by-char.
	hcOn, hcKey, hcMs := a.d.Prefs.HoldClear()
	if next := c.Checkbox(pad, y, "Hold a key to clear a text box (a fast wipe — no holding backspace char-by-char)", hcOn); next != hcOn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				HoldClearOn =

				next

		})
	}
	y += 28
	if hcOn {
		c.Label(pad+16, y+4, "Clear key:", ColTextDim)
		keyLabel := hcKey
		if a.holdKeyArmed {
			keyLabel = "press a key…  (Esc cancels)"
		}
		if c.Button(sdl.Rect{X: pad + 96, Y: y, W: 200, H: btnH}, keyLabel) {
			a.holdKeyArmed = !a.holdKeyArmed
		}
		if a.holdKeyArmed && c.keyPressed != 0 {
			if c.keyPressed != sdl.K_ESCAPE {
				a.d.Prefs.SetHoldClearKey(sdl.GetKeyName(c.keyPressed))
			}
			a.holdKeyArmed = false
			c.keyPressed = 0 // consume — don't let the captured key act elsewhere
		}
		y += btnH + 6
		if next := a.sliderRow(y, "  Hold time (ms)", hcMs, 100, config.MinHoldClearMs, config.MaxHoldClearMs); next != hcMs {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					HoldClearMs =

					next

			})

		}
		c.Label(pad+270, y+4, "how long to hold before it clears (default 1.5 s)", ColTextDim)
		y += 30
	}

	// Word delete (ON by default): Ctrl+Backspace removes the whole preceding
	// word instead of one letter — the standard editor chord. Off restores
	// plain-key behavior (Ctrl+Backspace does nothing inside a field).
	wdOn := a.d.Prefs.Load().WordDelete
	if next := c.Checkbox(pad, y, "Ctrl+Backspace deletes a word", wdOn); next != wdOn {
		a.d.Prefs.SetWordDelete(next)
	}
	y += 24
	c.Label(pad+16, y, "In any text box. Off restores plain-key behavior.", ColTextDim)
	y += 22

	// Message timing (AO2-Client options.ini parity); applies live. Plain
	// descriptions beside each knob so they're self-explanatory.
	c.Label(pad, y, "Text speed — how the IC chatbox plays messages:", ColTextDim)
	y += 20
	crawl, stay, rate := a.d.Prefs.Timing()
	crawl = a.sliderRow(y, "Text crawl ms", crawl, 5, config.MinTextCrawlMs, config.MaxTextCrawlMs)
	c.Label(pad+270, y+4, "delay between letters (higher = slower, easier to read)", ColTextDim)
	y += 26
	stay = a.sliderRow(y, "Text stay ms", stay, 100, 0, config.MaxTextStayMs)
	c.Label(pad+270, y+4, "pause after a message finishes before the next plays", ColTextDim)
	y += 26
	rate = a.sliderRow(y, "Chat limit ms", rate, 100, 0, config.MaxChatRateLimitMs)
	c.Label(pad+270, y+4, "smallest gap between YOUR sent messages (anti-spam)", ColTextDim)
	y += 30
	if c0, s0, r0 := a.d.Prefs.Timing(); c0 != crawl || s0 != stay || r0 != rate {
		a.d.Prefs.SetTiming(crawl, stay, rate)
		a.applyTimingToRoom()
	}

	// Packed-room catch-up vs. the OG-client queue. ON (default) = webAO pacing:
	// when 20 people talk at once, backlog messages skip their animations so the
	// stage tracks real-time. OFF = the original AO2 client's queue — every line
	// plays in full and in order, nothing cut off, but a room full of webAO users
	// (who skip the queue) can leave you behind. The log keeps every line either
	// way. (This is the "message queue" toggle — it was a setting, not removed.)
	cuOn, cuThresh := a.d.Prefs.CatchUp()
	const cuLabel = "Catch up in packed rooms — uncheck for the OG-client queue (every line plays in full, nothing cut off)"
	if next := c.Checkbox(pad, y, cuLabel, cuOn); next != cuOn {
		a.d.Prefs.SetCatchUp(next, cuThresh)
		a.applyTimingToRoom()
	}
	c.Tooltip(sdl.Rect{X: pad, Y: y, W: 22 + c.TextWidth(cuLabel), H: 16},
		"On (default): in a packed room, queued messages skip their animations so chat stays at real-time — like webAO. Off: the original AO2 client's queue — every IC message plays in full and in order (nothing cut off), but a roomful of webAO users can leave you minutes behind. The IC log keeps every line either way.")
	y += 26
	c.Label(pad+22, y, "Checked (default): webAO pacing — in a busy room, backlog messages skip their animation to keep chat current (they can flash past).", ColTextDim)
	y += 18
	c.Label(pad+22, y, "Unchecked: the original AO2 client's queue — every message plays out in full and in order, nothing skipped (busy webAO rooms may lag you).", ColTextDim)
	y += 22
	if cuOn {
		nt := a.numberRow(y, "Catch up after", cuThresh, 1, 1, 50)
		c.Label(pad+270, y+4, "fast-forward once at least this many messages are waiting (1 = stay on the newest)", ColTextDim)
		if nt != cuThresh {
			a.d.Prefs.SetCatchUp(cuOn, nt)
			a.applyTimingToRoom()
		}
		y += 30
	}
	// Message-send extras — they shape the messages you TYPE, so they belong
	// with text & typing.
	re := a.d.Prefs.Load().RandomEmote
	if next := c.Checkbox(pad, y, "Auto-random emote (OFF by default): every message picks a different emote from your character's set — for the lazy, and to show off more sprites", re); next != re {
		a.d.Prefs.SetRandomEmote(next)
	}
	y += 26
	rmc := a.d.Prefs.Load().RandomMessageColor
	if next := c.Checkbox(pad, y, "Random colour for each IC message (OFF by default): every message you send picks a random text colour — everyone sees it (standard colour field)", rmc); next != rmc {
		a.d.Prefs.SetRandomMessageColor(next)
	}
	y += 26
	rbw := a.d.Prefs.Load().RainbowMessages
	if next := c.Checkbox(pad, y, "Rainbow IC messages (OFF by default): your text cycles the palette per letter (takes priority over random; renders on clients that read inline \\cr colour)", rbw); next != rbw {
		a.d.Prefs.SetRainbowMessages(next)
	}
	y += 26
	// #14 2.8 additive text (ON by default): honor an incoming ADDITIVE=1 line
	// (it appends to the previous message, mirroring AO2's additive_previous) and
	// offer the Additive checkbox on servers that advertise it. Off falls back to
	// the pre-2.8 replace behavior.
	add := a.d.Prefs.Load().AdditiveText
	if next := c.Checkbox(pad, y, "Additive text (ON by default): let a message add to the previous one instead of replacing it (2.8 narration RP) — shows the Additive checkbox on servers that support it", add); next != add {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AdditiveText =

				next

		})

		if a.room != nil {
			a.room.AdditiveText = next // apply live
		}
	}
	y += 26

	y = a.settingsSection(y, w, "Chat log")
	// IC log depth (Nightingale's report): how many IC lines the on-screen log
	// keeps before the oldest scrolls away. Type an exact number, or press ∞ to
	// never cap it.
	c.Label(pad, y+4, "IC log depth:", ColText)
	depth := a.d.Prefs.Load().ICLogDepthVal
	depthFieldID := "iclogdepth"
	depthField := sdl.Rect{X: pad + 150, Y: y, W: 64, H: fieldH}
	if c.focusID != depthFieldID {
		switch depth {
		case config.ICLogDepthUnlimited:
			settings.logDepthBuf = ""
		case 0:
			settings.logDepthBuf = strconv.Itoa(icLogCap)
		default:
			settings.logDepthBuf = strconv.Itoa(depth)
		}
	}
	var depthEntered bool
	settings.logDepthBuf, depthEntered = c.TextField(depthFieldID, depthField, settings.logDepthBuf, "lines")
	if depthEntered {
		if n, err := strconv.Atoi(strings.TrimSpace(settings.logDepthBuf)); err == nil && n > 0 {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					ICLogDepthVal =

					n

			})

		} else {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					ICLogDepthVal =

					0

			})

			// blank / garbage → back to the default
		}
		c.focusID = ""
	}
	c.Tooltip(depthField, "How many IC lines the on-screen log keeps before the oldest scrolls away. Type an exact number, or press ∞ to never cap it.")
	depthInf := sdl.Rect{X: depthField.X + depthField.W + 6, Y: y, W: 28, H: btnH}
	if depth == config.ICLogDepthUnlimited {
		c.Fill(sdl.Rect{X: depthInf.X - 2, Y: depthInf.Y - 2, W: depthInf.W + 4, H: depthInf.H + 4}, ColAccent)
	}
	if c.Button(depthInf, "∞") {
		if depth == config.ICLogDepthUnlimited {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					ICLogDepthVal =

					0

			})

		} else {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					ICLogDepthVal =

					config.
						ICLogDepthUnlimited

			})

		}
	}
	c.Tooltip(depthInf, "Don't cap at all: keep every IC line for the whole session.")
	depthLbl := "default (" + strconv.Itoa(icLogCap) + " lines)"
	if depth == config.ICLogDepthUnlimited {
		depthLbl = "don't cap"
	} else if depth > 0 {
		depthLbl = strconv.Itoa(depth) + " lines"
	}
	c.Label(depthInf.X+depthInf.W+8, y+4, depthLbl, ColAccent)
	y += 28

	// Per-area IC scrollback (opt-in): each visited area keeps its own log.
	areaScroll := a.d.Prefs.Load().PerAreaScroll
	if next := c.Checkbox(pad, y, "Per-area chat scrollback (OFF by default): each area keeps its own IC log; switches when you click an area in the Areas list", areaScroll); next != areaScroll {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				PerAreaScroll =

				next

		})

	}
	y += 26
	// Detailed transcript logging (opt-OUT since the flip — every other
	// AO client saves the log, and people only discover they wanted it afterwards).
	// Existing configs are moved once by DetailedLogDefaultMigrated; turning it back
	// off here sticks forever.
	detLog := a.d.Prefs.Load().DetailedLog
	if next := c.Checkbox(pad, y, "Save the chat log to disk (ON by default): append every IC line to logs/transcript.log with timestamp, server, area, character + showname", detLog); next != detLog {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DetailedLog =

				next

		})

	}
	y += 26
	// Ghost text (Crystalwarrior's ask): the tail of a message the typewriter has
	// not reached yet is drawn transparent instead of absent, so the line keeps its
	// final shape and the log stops reflowing under the reader on every rune.
	ghost := a.d.Prefs.Load().GhostCrawlText
	if next := c.Checkbox(pad, y, "Ghost the text a message hasn't typed out yet (ON by default): the rest of the line shows faintly while it crawls, instead of the log growing a word at a time", ghost); next != ghost {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				GhostCrawlText =

				next

		})

	}
	y += 26
	// Auto-clip on modcall (opt-out): save the recent IC log when a modcall fires.
	clipMod := a.d.Prefs.Load().AutoClipModcall
	if next := c.Checkbox(pad, y, "Auto-clip on modcall (ON by default): when a mod is called, save the last IC lines to logs/<server>/modcalls/ as evidence", clipMod); next != clipMod {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AutoClipModcall =

				next

		})

	}
	y += 26
	// Show the full song URL in the "has played a song" log line instead of just
	// the name (a song's link, on request).
	songURL := a.d.Prefs.Load().ShowSongURL
	if next := c.Checkbox(pad, y, "Show the full song URL in the music log line (OFF by default): the whole link instead of just the song name", songURL); next != songURL {
		a.d.Prefs.SetShowSongURL(next)
	}
	y += 26
	// Smooth log scrolling (OFF by default = AO2 parity). The default snaps the log
	// to a newly-arrived line the instant it lands, exactly as AO2-Client does;
	// turning this on brings back the glide. It governs the FOLLOW only — the wheel
	// and the scrollbar keep their own easing either way.
	const smoothLogLabel = "Smooth log scrolling (OFF by default): when a new IC message arrives the log jumps straight to it, like AO2. Turn on to glide instead."
	smoothLog := a.d.Prefs.Load().SmoothLogScroll
	if next := c.Checkbox(pad, y, smoothLogLabel, smoothLog); next != smoothLog {
		a.d.Prefs.SetSmoothLogScroll(next)
	}
	c.Tooltip(sdl.Rect{X: pad, Y: y, W: 22 + c.TextWidth(smoothLogLabel), H: 16},
		"Only the automatic follow — the jump that keeps the newest message in view — is affected. Scrolling with the wheel or the scrollbar is unchanged.")
	y += 26

	y = a.settingsSection(y, w, "Area list")
	// Current-area highlight (playtest ask): the row you're in reads coloured
	// at a glance in the player list + mod dashboard; green by default,
	// recolourable here (the Extras-box hex convention: blank = default).
	c.Label(pad, y+4, "Your area's colour:", ColTextDim)
	areaHex := a.d.Prefs.Load().AreaHighlightHex
	sw := sdl.Rect{X: pad + 160, Y: y + 1, W: 18, H: 18}
	c.Fill(sw, a.areaHighlightCol())
	c.Border(sw, ColTextDim)
	if next, _ := c.TextField("areahicol", sdl.Rect{X: pad + 186, Y: y, W: 110, H: fieldH}, areaHex, "rrggbb"); next != areaHex {
		a.d.Prefs.SetAreaHighlightColorHex(next)
	}
	wheelLbl := "Wheel…"
	if a.showAreaWheel {
		wheelLbl = "Close"
	}
	if c.Button(sdl.Rect{X: pad + 306, Y: y - 1, W: 64, H: 22}, wheelLbl) {
		a.showAreaWheel = !a.showAreaWheel
	}
	c.LabelClipped(pad+378, y+4, w-pad-378-scrollBarW, "hex like 4ac96c — blank = the stock green; marks the area you're in", ColTextDim)
	y += 32
	if a.showAreaWheel {
		// The shared inline colour wheel (drawWheelPicker), writing back as hex —
		// the same value the field above edits. The extra button restores the
		// stock green (the blank-pref default).
		hi := a.areaHighlightCol()
		wheelY := y
		ny, hx := a.drawWheelPicker(pad, wheelY, int(hi.R)<<16|int(hi.G)<<8|int(hi.B), func(rgb int) {
			a.d.Prefs.SetAreaHighlightColorHex(fmt.Sprintf("%06x", rgb))
		})
		if c.Button(sdl.Rect{X: hx, Y: wheelY + 42, W: 100, H: btnH}, "Stock green") {
			a.d.Prefs.SetAreaHighlightColorHex("")
		}
		y = ny
	}

	y = a.settingsSection(y, w, "Case alerts")
	// Case announcements (CASEA, tsuserver-family): subscribe by role.
	y = a.drawCasingRow(y)

	y = a.settingsSection(y, w, "Callwords")
	// #203 self-mention: auto-treat your own showname / character name as a
	// callword (whole-word match, never your own messages) so you're pinged when
	// addressed by name without adding it manually. Honours the sound/toast/OS
	// options below and the streamer / DND / login-grace suppressions.
	ms := a.d.Prefs.Load().MentionSelf
	if next := c.Checkbox(pad, y, "Alert me when someone says my name (OFF by default): treats your showname + character as callwords, matched as whole words and never on your own lines", ms); next != ms {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MentionSelf =

				next

		})

	}
	y += 30
	// Callwords manager: type a word (or paste "a, b, c") + Add, and each shows
	// below with a × to remove. Flash + sound + toast fire on an IC/OOC match.
	c.Label(pad, y+4, "Add word(s):", ColTextDim)
	var callCommit bool
	settings.callAddInput, callCommit = c.TextField("callwordadd", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, settings.callAddInput, "your name, nickname… (comma-separates; flash + sound when seen in IC/OOC)")
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "+ Add") || callCommit {
		if n := a.d.Prefs.AddCallWord(settings.callAddInput); n > 0 {
			settings.callAddInput = ""
			settings.statusLine = fmt.Sprintf("Added %d callword(s).", n)
		} else {
			settings.statusLine = "Nothing added (blank, already listed, or at the 32-word cap)."
		}
	}
	y += 26
	// §3.5 rule reminder: whole-word match by default, trailing '*' for the loose
	// prefix shorthand. Kept to one dim line so the manager layout is unchanged.
	c.Label(pad+130, y, "Matched as whole words — add a trailing * for a loose prefix (e.g. obj* catches \"objection\").", ColTextDim)
	y += 22
	if words := a.d.Prefs.CallWords(); len(words) > 0 {
		for _, w := range words {
			if c.Button(sdl.Rect{X: pad + 28, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.RemoveCallWord(w)
			}
			c.LabelClipped(pad+56, y+1, 320, w, ColText)
			y += 24
		}
	} else {
		c.Label(pad+28, y+1, "No callwords yet — add your name to get pinged when it's said.", ColTextDim)
		y += 24
	}
	y += 6
	c.Label(pad, y+4, "Callword sound:", ColTextDim)
	if next, _ := c.TextField("cwsound", sdl.Rect{X: pad + 130, Y: y, W: 480, H: fieldH}, a.d.Prefs.Load().CallwordSoundFile, "custom .wav/.ogg/.mp3/.opus path (blank = built-in ping)"); next != a.d.Prefs.Load().CallwordSoundFile {
		a.d.Prefs.SetCallwordSoundPath(next)
	}
	y += 32
	if c.Button(sdl.Rect{X: pad + 120, Y: y, W: 130, H: btnH}, "Test sound") {
		// Play exactly what a callword/friend alert fires: the custom file if set,
		// else the built-in ping — so people can confirm it's actually audible.
		if f := a.d.Prefs.Load().CallwordSoundFile; f != "" {
			a.d.Audio.PlayFile(f)
		} else {
			a.d.Audio.PlayAlert()
		}
	}
	c.Label(pad+260, y+6, "play the alert sound now to check it works", ColTextDim)
	y += 34
	ct := a.d.Prefs.Load().CallwordToast
	if next := c.Checkbox(pad, y, "Toast when a callword is heard (ON by default): a popup names the word, like the modcall/friend toasts.", ct); next != ct {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CallwordToast =

				next

		})

	}
	y += 26
	cot := a.d.Prefs.Load().CallwordOSToast
	if next := c.Checkbox(pad+16, y, "Also pop a DESKTOP (OS) notification when AsyncAO is in the background (OFF by default; Windows; rate-limited)", cot); next != cot {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CallwordOSToast =

				next

		})

	}
	y += 26
	cooc := a.d.Prefs.Load().CallwordsOOC
	if next := c.Checkbox(pad, y, "Check OOC messages for callwords too (OFF by default — IC only; on = also ping on OOC, e.g. someone typing your room ID)", cooc); next != cooc {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CallwordsOOC =

				next

		})

	}
	y += 30
	y = a.settingsSection(y, w, "Do Not Disturb")
	// Do Not Disturb: session-only by default (clears every launch so it can't
	// silently kill your callwords days later) — mutes the personal pings only.
	// The keybind (default Ctrl+D, rebindable on the Controls tab) toggles it too.
	dndPersist := a.d.Prefs.Load().DNDPersist
	dndTail := "Clears on restart."
	if dndPersist {
		dndTail = "Remembered across restarts (option below)."
	}
	dndLabel := "Do Not Disturb (Ctrl+" + strings.ToUpper(a.hotkeyFor(hotkeyDND)) + ") — mute callword + friend pings (sound/toast/window flash). Modcalls + case alerts still come through. " + dndTail
	if next := c.Checkbox(pad, y, dndLabel, a.dndOn); next != a.dndOn {
		a.setDND(next)
		if next {
			settings.statusLine = "Do Not Disturb ON — callword + friend pings muted."
		} else {
			settings.statusLine = "Do Not Disturb off."
		}
	}
	y += 26
	if next := c.Checkbox(pad+16, y, "Remember Do Not Disturb across restarts (default off — DND normally clears every launch)", dndPersist); next != dndPersist {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DNDPersist =

				next

		})

		if next {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					DNDSaved =

					a.
						dndOn

			})

			// snapshot the current state so it restores correctly
		}
	}
	y += 30

	// AO2's four send-time options, shipped with AsyncAO's defaults. The three
	// "sticky" ones govern Courtroom::reset_ui (courtroom.cpp:2340, :2348, :2356),
	// which clears each pick only when the matching option is OFF; AO2 ships all
	// three ON, AsyncAO ships them OFF so the picks reset after every send (matching
	// the one-shot Screenshake/Realization arms).
	y = a.settingsSection(y, w, "Sending a message")
	y = a.settingsDesc(pad, y, "What the IC bar does to itself after you press Enter. These are Attorney Online's own options, shipped with AsyncAO's defaults (reset after every send).", ColTextDim)
	y += 6
	ssnd := a.d.Prefs.Load().StickySounds
	if next := c.Checkbox(pad, y, "Sticky sounds (OFF by default): keep the SFX dropdown's pick after sending, instead of returning it to Default", ssnd); next != ssnd {
		a.d.Prefs.SetStickySounds(next)
	}
	y += 26
	sfxx := a.d.Prefs.Load().StickyEffects
	if next := c.Checkbox(pad, y, "Sticky effects (OFF by default): keep the effects dropdown's pick after sending. Off, an effect clears unless it declares itself sticky in effects.ini", sfxx); next != sfxx {
		a.d.Prefs.SetStickyEffects(next)
	}
	y += 26
	spre := a.d.Prefs.Load().StickyPreanims
	if next := c.Checkbox(pad, y, "Sticky preanims (OFF by default): leave the Pre checkbox ticked after sending, instead of clearing it", spre); next != spre {
		a.d.Prefs.SetStickyPreanims(next)
	}
	y += 26
	soi := a.d.Prefs.Load().SFXOnIdle
	if next := c.Checkbox(pad, y, "Always send SFX (OFF by default): play a hand-picked sound even with Pre unticked", soi); next != soi {
		a.d.Prefs.SetSFXOnIdle(next)
	}
	y += 26
	y = a.settingsDesc(pad, y, "AO plays a message's sound off its pre-animation stage, so a line sent with Pre unticked is silent at everyone else's client no matter which SFX you picked. On, the message is marked as having a pre-animation with an empty animation name and no delay, which is how the sound gets heard without anything actually playing. This is Attorney Online's own \"Always Send SFX\"; it ships off there too.", ColTextDim)
	y += 10

	y = a.settingsSection(y, w, "Messages & connection")
	mc := a.d.Prefs.Load().MessageCounter
	if next := c.Checkbox(pad, y, "Show a character count by the IC box (ON by default): turns red past ~256 chars, where many servers truncate.", mc); next != mc {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MessageCounter =

				next

		})

	}
	y += 26

	ts := a.d.Prefs.Load().ICTimestamps
	if next := c.Checkbox(pad, y, "Show local timestamps in the IC log (OFF by default): prefixes each line with the time it arrived, so you can see when people spoke.", ts); next != ts {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ICTimestamps =

				next

		})

	}
	y += 26

	ar := a.d.Prefs.Load().AutoReconnect
	if next := c.Checkbox(pad, y, "Auto-reconnect after a dropped connection (OFF by default): a drop otherwise returns you to the lobby to click Reconnect. When ON, retries the last server with backoff (which re-runs the join handshake, so you land at character select). A deliberate Disconnect never reconnects; the manual Reconnect button always works.", ar); next != ar {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AutoReconnect =

				next

		})

	}
	y += 26

	idc := a.d.Prefs.Load().InstantDisconnect
	if next := c.Checkbox(pad, y, "Instant disconnect (OFF by default): the Disconnect button asks for confirmation first, since it's easy to hit by accident. Turn this on to disconnect immediately with no prompt.", idc); next != idc {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				InstantDisconnect =

				next

		})

	}
	y += 26

	acl := a.d.Prefs.Load().AutoConnectOnLaunch
	lastName, lastURL := a.d.Prefs.LastServer()
	label := "Auto-connect to my last server on launch (OFF by default): opens straight onto the server you last used, even after a disconnect. Also bound to a Connect-to-last-server key (Controls tab)."
	if lastURL != "" {
		label = "Auto-connect on launch to \"" + lastName + "\" (OFF by default): your last server. Reconnect there with the Connect-to-last-server key (Controls tab) too."
	}
	if next := c.Checkbox(pad, y, label, acl); next != acl {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AutoConnectOnLaunch =

				next

		})

	}
	y += 30

	y = a.settingsSection(y, w, "Sound effects")
	// M11 per-SFX mute: silence an annoying emote sound effect by name. The
	// last one you heard gets a one-click toggle; the muted list is below.
	if a.lastSFXName != "" {
		on := a.d.Prefs.IsSFXMuted(a.lastSFXName)
		lbl := "Mute last SFX: " + a.lastSFXName
		if on {
			lbl = "Unmute last SFX: " + a.lastSFXName
		}
		if c.Button(sdl.Rect{X: pad, Y: y, W: c.TextWidth(lbl) + 20, H: btnH}, lbl) {
			if on {
				a.d.Prefs.UnmuteSFX(a.lastSFXName)
			} else {
				a.d.Prefs.MuteSFX(a.lastSFXName)
			}
		}
		y += 30
	} else {
		c.Label(pad, y+2, "Per-SFX mute: the last emote sound effect you hear gets a one-click Mute button here.", ColTextDim)
		y += 26
	}
	if list := a.d.Prefs.MutedSFXList(); len(list) > 0 {
		c.Label(pad, y+2, "Muted sound effects (× to unmute):", ColTextDim)
		y += 22
		for _, name := range list {
			if c.Button(sdl.Rect{X: pad + 12, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.UnmuteSFX(name)
			}
			c.LabelClipped(pad+40, y+1, 360, name, ColText)
			y += 24
		}
		y += 6
	}

	// M11 per-character blip volume: quiet a character whose typing blips are
	// too loud (their scale multiplies the global blip volume; 100% = no
	// change). The last speaker gets a quick slider; everyone you've adjusted
	// is listed below with their own slider and a × that resets to 100%.
	c.Label(pad, y+2, "Per-character blip volume (typing-sound loudness, 100% = unchanged):", ColTextDim)
	y += 22
	if a.lastBlipChar != "" {
		cur := a.d.Prefs.BlipVolumeFor(a.lastBlipChar)
		if nv := a.volumeRow(y, "Last speaker — "+a.lastBlipChar, cur); nv != cur {
			a.d.Prefs.SetBlipVolume(a.lastBlipChar, nv)
		}
		y += 28
	} else {
		c.Label(pad+12, y+1, "The last character to speak gets a quick slider here.", ColTextDim)
		y += 24
	}
	if vols := a.d.Prefs.BlipVolumes(); len(vols) > 0 {
		names := make([]string, 0, len(vols))
		for name := range vols {
			names = append(names, name)
		}
		sort.Strings(names) // stable row order (map iteration is random)
		for _, name := range names {
			if c.Button(sdl.Rect{X: pad + 12, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.SetBlipVolume(name, 100) // reset to default (100% = unchanged)
			}
			c.LabelClipped(pad+40, y+1, 124, name, ColText)
			track := sdl.Rect{X: pad + 170, Y: y + 1, W: 110, H: 16}
			if nv := int(c.Slider("blipvol:"+name, track, int32(vols[name]), 100)); nv != vols[name] {
				a.d.Prefs.SetBlipVolume(name, nv)
			}
			c.Label(track.X+track.W+8, y+1, fmt.Sprintf("%3d%%", vols[name]), ColAccent)
			y += 24
		}
		y += 6
	}

	y = a.settingsSection(y, w, "Music history")
	// M12: keep a session "recently played" jukebox history (ON by default).
	mh := a.d.Prefs.Load().MusicHistory
	if next := c.Checkbox(pad, y, "Keep a \"recently played\" music history (ON by default): the Jukebox tab lists songs played in the room so you can Save a link (into the \"Music history\" playlist), Play, or Share. Off = don't record.", mh); next != mh {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MusicHistory =

				next

		})

	}
	y += 30

	// Domain allowlist for the history: only songs from these "unique" user-hosted
	// domains are recorded (the server's own music still plays, it's just not
	// saved). Discord records audio files only. Add/remove like the muted-SFX list.
	if mh {
		c.Label(pad+16, y, "Only record songs from these hosts — others still play, just aren't saved. Add a host (catbox.moe), or a host/folder", ColTextDim)
		y += 18
		c.Label(pad+16, y, "for a server's user-rip path (e.g. miku.pizza/base/youtube → only songs under it). Discord: audio files only.", ColTextDim)
		y += 22
		a.musicHostInput, _ = c.TextField("musichost", sdl.Rect{X: pad + 16, Y: y, W: 240, H: fieldH}, a.musicHostInput, "Host or host/folder (e.g. catbox.moe)…")
		if c.Button(sdl.Rect{X: pad + 262, Y: y, W: 80, H: btnH}, "+ Add") {
			if a.d.Prefs.AddMusicHost(a.musicHostInput) {
				a.musicHostInput = ""
			} else {
				a.jukeWarn("Enter a host or host/folder (or it's already listed / at the cap).")
			}
		}
		y += fieldH + 6
		for _, h := range a.d.Prefs.MusicHostList() {
			if c.Button(sdl.Rect{X: pad + 28, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.RemoveMusicHost(h)
			}
			c.LabelClipped(pad+56, y+1, 320, h, ColText)
			y += 24
		}
		y += 6
	}

	y = a.settingsSection(y, w, "Friends")
	// Player-list friend button (ON by default): the per-row "+ Friend" /
	// "Unfriend" button. Hide it if it clutters the panel.
	sfb := a.d.Prefs.Load().ShowFriendButton
	if next := c.Checkbox(pad, y, "Show the \"+ Friend\" / \"Unfriend\" button on player-list rows (ON by default): one click adds or removes a friend.", sfb); next != sfb {
		a.d.Prefs.SetShowFriendButton(next)
	}
	y += 26
	// Highlighted friends (per server): shownames whose IC messages glow.
	fh := a.d.Prefs.Load().FriendHighlight
	if next := c.Checkbox(pad, y, "Highlight friends in the IC log (OFF by default): their messages glow. Matches the DISPLAYED name, so it can be spoofed.", fh); next != fh {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FriendHighlight =

				next

		})

	}
	y += 26
	fgp := a.d.Prefs.Load().FriendGlowPulse
	if next := c.Checkbox(pad+16, y, "Pulse the friend glow (gentle breathing animation; obeys reduce-motion)", fgp); next != fgp {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FriendGlowPulse =

				next

		})

	}
	y += 26
	fn := a.d.Prefs.Load().FriendNotify
	if next := c.Checkbox(pad+16, y, "Notify + flash the taskbar when a friend speaks (fires even from a backgrounded server tab)", fn); next != fn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FriendNotify =

				next

		})

	}
	y += 26
	fot := a.d.Prefs.Load().FriendOSToast
	if next := c.Checkbox(pad+16, y, "Also pop a DESKTOP (OS) notification — a real Windows toast (rate-limited so it can't storm)", fot); next != fot {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FriendOSToast =

				next

		})

	}
	y += 26
	fsnd := a.d.Prefs.Load().FriendSound
	if next := c.Checkbox(pad+16, y, "Play a sound when a friend speaks", fsnd); next != fsnd {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FriendSound =

				next

		})

	}
	y += 26
	c.Label(pad+16, y+4, "Friend sound:", ColTextDim)
	if next, _ := c.TextField("friendsound", sdl.Rect{X: pad + 130, Y: y, W: 480, H: fieldH}, a.d.Prefs.Load().FriendSoundFile, "custom .wav/.ogg/.mp3/.opus path (blank = built-in ping)"); next != a.d.Prefs.Load().FriendSoundFile {
		a.d.Prefs.SetFriendSoundPath(next)
	}
	y += 32
	if a.serverKey == "" {
		c.Label(pad, y+4, "Friends: connect to a server to set its highlighted shownames.", ColTextDim)
		y += 30
	} else {
		c.Label(pad, y+4, "Friends:", ColText)
		if settings.friendKey != a.serverKey { // reload buffer per server
			settings.friendInput = strings.Join(a.d.Prefs.ServerFriends(a.serverKey), ", ")
			settings.friendKey = a.serverKey
		}
		var friendCommit bool
		settings.friendInput, friendCommit = c.TextField("friends", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, settings.friendInput, "showname, showname=ffcc00, showname=ffcc00=Nickname, ... (per server)")
		if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || friendCommit {
			a.d.Prefs.SetServerFriends(a.serverKey, strings.Split(settings.friendInput, ","))
			settings.statusLine = "Friends saved for this server."
		}
		y += 28
		c.Label(pad+110, y, "name=RRGGBB sets a custom glow + name colour; name=RRGGBB=Nick adds a personal nickname (shown in the player list). Use name==Nick for a nickname with no colour. No commas in nicknames.", ColTextDim)
		y += 24
	}

	y = a.settingsSection(y, w, "Ignored players")
	if a.serverKey == "" {
		c.Label(pad, y+4, "Ignored: connect to a server to manage its ignore list.", ColTextDim)
		y += 30
	} else {
		c.Label(pad, y+4, "Ignored:", ColText)
		if settings.ignoreKey != a.serverKey { // reload buffer per server
			settings.ignoreInput = strings.Join(a.d.Prefs.ServerIgnored(a.serverKey), ", ")
			settings.ignoreKey = a.serverKey
		}
		var ignoreCommit bool
		settings.ignoreInput, ignoreCommit = c.TextField("ignored", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, settings.ignoreInput, "showname1, showname2, ... (saved per server)")
		if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || ignoreCommit {
			a.d.Prefs.SetServerIgnored(a.serverKey, strings.Split(settings.ignoreInput, ","))
			settings.statusLine = "Ignore list saved for this server."
		}
		y += 28
		c.Label(pad+110, y, "Ignored players' IC + OOC are hidden entirely (no log, no sprite, no blip). Edit here to un-ignore someone who's left. Use the Ignore button on player rows too.", ColTextDim)
		y += 24
	}

	y = a.settingsSection(y, w, "Mod tools")
	// Mod-call desktop toast (for moderators).
	mct := a.d.Prefs.Load().ModcallToast
	if next := c.Checkbox(pad, y, "Desktop notification on mod-call (OFF by default): pop a Windows toast when a modcall comes in — for mods who alt-tabbed away", mct); next != mct {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ModcallToast =

				next

		})

	}
	y += 28

	// Mod-command feedback sounds (#60): a distinct sound on ban/kick/mute —
	// fires when your own /ban /kick /mute lands in OOC, on mod actions you can
	// see, and when you personally get kicked/banned. Each action has its own
	// toggle + optional custom file (blank = the built-in default). A duty
	// signal, so it plays through Do Not Disturb. Test plays it right now.
	c.Label(pad, y, "Mod-command SFX — play a sound when a ban / kick / mute happens (blank file = built-in default):", ColTextDim)
	y += 24
	type modSFXRow struct {
		label  string
		on     bool
		path   string
		action render.ModAction
		setOn  func(bool)
		setPth func(string)
	}
	for _, r := range []modSFXRow{
		{"Ban", a.d.Prefs.Load().ModBanSFX, a.d.Prefs.Load().ModBanSoundFile, render.ModBan, a.d.Prefs.SetModBanSFX, a.d.Prefs.SetModBanSoundPath},
		{"Kick", a.d.Prefs.Load().ModKickSFX, a.d.Prefs.Load().ModKickSoundFile, render.ModKick, a.d.Prefs.SetModKickSFX, a.d.Prefs.SetModKickSoundPath},
		{"Mute", a.d.Prefs.Load().ModMuteSFX, a.d.Prefs.Load().ModMuteSoundFile, render.ModMute, a.d.Prefs.SetModMuteSFX, a.d.Prefs.SetModMuteSoundPath},
	} {
		if next := c.Checkbox(pad+16, y, r.label, r.on); next != r.on {
			r.setOn(next)
		}
		if next, _ := c.TextField("modsfx:"+r.label, sdl.Rect{X: pad + 130, Y: y, W: 380, H: fieldH}, r.path, "custom .wav/.ogg/.mp3/.opus (blank = built-in)"); next != r.path {
			r.setPth(next)
		}
		if c.Button(sdl.Rect{X: pad + 520, Y: y, W: 60, H: btnH}, "Test") {
			a.d.Audio.PlayModAction(r.action, r.path)
		}
		y += 28
	}
	return y
}

// drawSettingsAccount: per-server login, the master-list override, Discord.
// formatWaitMs renders a millisecond knob compactly ("850 ms", "1.5 s").
func formatWaitMs(ms int) string {
	if ms < 1000 {
		return strconv.Itoa(ms) + " ms"
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64) + " s"
}

// spriteLoadModeLabel is the cycle-button label for the power-user loading mode.
func spriteLoadModeLabel(mode int) string {
	switch mode {
	case config.SpriteLoadDefault:
		return "Loading mode: Default — wait for every asset before each message plays"
	case config.SpriteLoadHoldPrev:
		return "Loading mode: webAO — keep the previous sprite until the new one loads"
	case config.SpriteLoadWait:
		return "Loading mode: Hold the message — wait for the speaker sprite (timeout)"
	}
	return "Loading mode: Show nothing — blank until the sprite loads"
}

// spriteLoadModeDesc is the per-mode WHAT-IT-DOES shown under the cycle button.
func spriteLoadModeDesc(mode int) string {
	switch mode {
	case config.SpriteLoadDefault:
		return "Default: every message holds until ALL its assets have loaded — character sprites (speaker, pair, group), the background + desk, the custom chatbox, zoom speedlines, the emote SFX + blip, and any screen-effect art or realization/effect sound. Nothing pops in mid-ceremony. Shouts still skip the queue (toggle below), and catch-up (AO2's skip-to-newest, above) still fast-forwards a backlog."
	case config.SpriteLoadHoldPrev:
		return "webAO: while an uncached sprite is still downloading, the layer's last drawn sprite stays on screen until the new one lands (the old default)."
	case config.SpriteLoadWait:
		return "Hold the message: the message stays off-stage until its speaker sprite decodes, capped by the timeout below (a broken sprite only ever delays it)."
	}
	return "Show nothing: blank until the sprite lands (the original behaviour, the cold-load flash)."
}

// powerUserToggleLabel is the reveal-button label for the advanced (power-user) settings.
func powerUserToggleLabel(on bool) string {
	if on {
		return "Hide advanced options"
	}
	return "Show advanced (power-user) options"
}

// charCaseLabel is the cycle-button label for the character-folder asset casing (power-user only).
func charCaseLabel(c uint8) string {
	switch c {
	case courtroom.CharCaseFirstCap:
		return "Casing: First cap (Phoenix wright)"
	case courtroom.CharCaseTitle:
		return "Casing: Title (Phoenix Wright)"
	case courtroom.CharCaseAuto:
		return "Casing: Auto-detect (learn per server)"
	default:
		return "Casing: lowercase (default)"
	}
}

// texCompressLabel is the cycle-button label for the texture-compression mode.
func texCompressLabel(mode int) string {
	switch mode {
	case config.TexCompressDXT5:
		return "DXT5 (BC3) - ~4x smaller"
	case config.TexCompressDXT1:
		return "DXT1 (BC1) - ~8x smaller"
	default:
		return "Off (raw RGBA)"
	}
}
