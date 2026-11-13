package ui

// The IC input's focus policy. (a) Typing with nothing focused lands in the IC input, including the first keystroke.
// (b) Clicking a control of the message being composed bounces focus back to the input. AO2 has no equivalent of (a).
// (b) follows AO2's focus_ic_input calls. Wired, in every bar that draws the control: courtroom.cpp:502 ui_pre,
// courtroom.cpp:503 ui_flip, courtroom.cpp:504 ui_additive (:6572), courtroom.cpp:506 ui_slide_enable (themed bar only;
// the classic bar draws no Slide checkbox), courtroom.cpp:5501 SFX dropdown (applySFXPick), courtroom.cpp:5656 effects
// dropdown (drawEffectsPicker), courtroom.cpp:6178 realization, courtroom.cpp:6194 screenshake, courtroom.cpp:6402 text
// colour (applyICColorPick), courtroom.cpp:6065/:6085/:6105/:6125 the four shouts (toggleShout, in both IC bars and on
// the hotkeys), courtroom.cpp:5363 iniswap dropdown and remove button, emotes.cpp:225 select_emote (selectEmote, themed
// emote grid). The last two queue c.FocusField("ic") at the draw site, so they miss the capturingKey guard. AO2 does
// not bounce these, so neither do we: ui_immediate (courtroom.cpp:331, absent from courtroom.cpp:502-506),
// on_pair_offset_changed (:6433), on_pair_vert_offset_changed (:6438), on_log_limit_changed (:6428), a pos dropdown
// change. Covered by (a): :1621 update_character and :4703 set_mute(false), since nothing is focused at those moments.
// AO2 bounces these but we do not: :505 ui_guard, :5316 on_pos_remove_clicked, :6453/:6465/:6477/:6489 the judge strip,
// :6555 on_call_mod_clicked (our modcall float has its own reason field, which a bounce would leave), :6408/:6420/:6426
// volume sliders (dragged, so bounceOnToggle does not fit), emotes.cpp:303/:312 emote page arrows, evidence.cpp:609
// evidence Present toggle.

// claimTypingForIC is half (a), called pre-screen from App.Frame so the IC field consumes this frame's c.typed. Each
// refusal is a place the keystroke belongs elsewhere: not the courtroom; a focused field; no text this frame; Ctrl or
// Alt chords (AltGr = Ctrl+Alt on Windows and does produce text); an open dropdown, key-bind capture or layout editor.
// A hidden IC field must not take focus; this asks ctx.drawnFieldSeq, not fieldSeq, which this frame has not filled
// yet.
func (a *App) claimTypingForIC() {
	c := a.ctx
	if c.focusID != "" || c.typed == "" || a.screen != ScreenCourtroom {
		return
	}
	if c.ctrlHeld || c.altHeld || c.modalOn {
		return
	}
	if a.capturingKey() || a.classicEdit || a.layoutEdit {
		return
	}
	if !c.fieldDrewLastFrame(icFieldID) {
		return
	}
	c.focusID = icFieldID
	c.caretOn, c.caretAcc = true, 0 // land visible, exactly as the Tab cycle does
}

// bounceFocusToIC is half (b): queued via FocusField so it survives the same frame's click-away unfocus in any draw
// order. A no-op outside the courtroom and during key-bind capture, so shared widgets can call it unconditionally.
func (a *App) bounceFocusToIC() {
	if a.screen != ScreenCourtroom || a.capturingKey() {
		return
	}
	a.ctx.FocusField(icFieldID)
}

// bounceOnToggle is (b) for a checkbox: pass the box's value and the field's, and assign the result back. It compares
// rather than trusting the click because Flip has two views of one bool, and the second redraws the same value.
func (a *App) bounceOnToggle(next, cur bool) bool {
	if next != cur {
		a.bounceFocusToIC()
	}
	return next
}
