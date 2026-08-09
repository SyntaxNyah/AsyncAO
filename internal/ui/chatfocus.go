package ui

// The IC input's FOCUS POLICY — the "Discord model" (user order, 2026-08-09).
//
// One rule, two halves:
//
//	(a) Typing with nothing focused lands in the IC input, INCLUDING the first
//	    keystroke. Not "the next one": the character that armed the focus is the
//	    character that gets typed, which is the whole difference between this and
//	    a plain click-to-focus box.
//	(b) Clicking a control that belongs to the message you are composing bounces
//	    focus straight back to the input, so the keyboard never gets stranded on a
//	    checkbox mid-sentence.
//
// (b) IS A TRANSCRIPTION, NOT A POLICY, and the exact membership is canon's, not
// ours. AO2 calls focus_ic_input (or ui_ic_chat_message->setFocus) from about two
// dozen places; the four lists below say, for every one of them, what AsyncAO
// does. They are spelled out because an earlier revision of this header printed
// only the first list and called it "these and only these", which was wrong about
// six sites — including on_text_color_changed, whose AsyncAO twin was already a
// shared applier one line away from the wiring.
//
// TRANSCRIBED — wired here, in EVERY bar that draws the control. This is the list
// TestEveryTranscribedCanonFocusBounceIsWired enforces:
//
//	courtroom.cpp:502   ui_pre           → focus_ic_input
//	courtroom.cpp:503   ui_flip          → focus_ic_input
//	courtroom.cpp:504   ui_additive      → on_additive_clicked → focus_ic_input (:6572)
//	courtroom.cpp:506   ui_slide_enable  → focus_ic_input  (themed bar only —
//	                    the classic bar draws no Slide checkbox)
//	courtroom.cpp:5501  on_sfx_dropdown_changed     → applySFXPick (icsfx.go)
//	courtroom.cpp:5656  on_effects_dropdown_changed → drawEffectsPicker
//	courtroom.cpp:6178  on_realization_clicked      → toggleRealization (icarms.go)
//	courtroom.cpp:6194  on_screenshake_clicked      → toggleScreenshake (icarms.go)
//	courtroom.cpp:6402  on_text_color_changed       → applyICColorPick (screens.go)
//	courtroom.cpp:5363  on_iniswap_dropdown_changed → the themed iniswap dropdown
//	                    and its remove button (theme_layout.go)
//	emotes.cpp:225      select_emote → selectEmote (screens.go) and the themed
//	                    emote grid (theme_layout.go)
//
// The last two predate this file and queue c.FocusField("ic") at the draw site
// rather than calling bounceFocusToIC, so they miss the capturingKey guard. Left
// as they are: rewriting live call sites in two other painters is not this file's
// change, and the guard only matters while a key-bind capture is armed.
//
// CANON-INERT — AO2 pointedly does NOT bounce these, so neither do we. Do not
// "fix" them:
//
//	ui_immediate — the one IC-bar checkbox AO2 leaves unconnected (a QCheckBox
//	  constructed at courtroom.cpp:331, absent from the :502-506 connect block).
//	on_pair_offset_changed (:6433), on_pair_vert_offset_changed (:6438) and
//	  on_log_limit_changed (:6428) end without a focus call.
//	the pos DROPDOWN — only pos_remove (:5316) bounces, not a pos change.
//
// SUBSUMED BY HALF (a) — canon needs the bounce, AsyncAO does not:
//
//	:1621 update_character and :4703 set_mute(false) hand the keyboard to the chat
//	box on arrival / on being un-muted. AsyncAO has no twin call because nothing is
//	focused at either moment, so the first keystroke lands in the IC box anyway.
//
// NOT TRANSCRIBED — canon bounces, AsyncAO does not (yet), one honest line each:
//
//	:505  ui_guard — the themed Guard checkbox (theme_layout.go) is an exact twin
//	      and is simply not wrapped. One line; pinned as `deferred` in the census
//	      so wiring it cannot happen without updating this table.
//	:5316 on_pos_remove_clicked — same story, the themed pos_remove button.
//	:6065/:6085/:6105/:6125 the four shouts — NOT a wrap: AsyncAO's shout buttons
//	      SEND the line (drawICShoutRow returns a modifier straight into sendIC)
//	      where AO2's only arm objection_state, so "return the keyboard to the
//	      line you are composing" is about a different line. Needs a decision.
//	:6453/:6465/:6477/:6489 WT / CE / Not Guilty / Guilty — RT packet buttons in
//	      the judge strip (court_extras.go), which composes nothing.
//	:6555 on_call_mod_clicked — AsyncAO's modcall is a float panel carrying its own
//	      reason field; a bounce would pull the keyboard out of the box the click
//	      just opened, the exact hazard icColorPickOpensWheel exists for.
//	:6408/:6420/:6426 the music / SFX / blip volume sliders — a slider is dragged,
//	      not clicked, so it needs bounceOnToggle's change-detecting analogue for a
//	      continuous value, and that seam does not exist.
//	emotes.cpp:303/:312 the emote-grid page arrows (emotescroll.go) and
//	      evidence.cpp:609 the evidence Present toggle (court_extras.go) — both are
//	      plain omissions, in painters this change does not touch.
//
// AO2 has no equivalent of (a) — it is AsyncAO's, and it is what makes the
// bare-key bind namespaces untenable, which is why they moved to Alt chords in the
// same wave (bareBindKey, qol.go).
//
// Render thread. No allocation on the settled path: the claim only runs on a
// frame that actually produced text with nothing focused.

// claimTypingForIC is half (a). Called PRE-SCREEN from App.Frame, so the focus is
// already set when the IC field draws later in the same frame and therefore
// consumes this frame's c.typed — that is what makes the FIRST keystroke land.
//
// Every refusal below is a place the keystroke belongs to something else:
//
//   - not the courtroom: there is no IC input to type into;
//   - something is already focused: the user aimed at that field;
//   - no text this frame: an arrow / F-key / modifier press is not "typing";
//   - Ctrl or Alt held: those are the chord namespaces (hotkeys, and the
//     relocated bare-key binds), and a chord must never be swallowed as text —
//     note SDL filters Ctrl chords out of TEXTINPUT anyway, so this is belt and
//     braces for the Alt half and for AltGr layouts (AltGr = Ctrl+Alt on Windows,
//     and it DOES produce text, which is why the Ctrl test comes first);
//   - a modal fence is up (an open dropdown), a key-bind capture is armed, or a
//     layout editor owns the keyboard: those surfaces are the ones being driven;
//   - the IC field did not draw on the last painted frame: a hidden IC bar (the
//     panel is toggled off, or a themed layout declares no input rect) must not
//     receive focus it cannot show, which would swallow the keystroke into a box
//     that is not on screen. That question is asked of ctx.drawnFieldSeq — the
//     list BeginDraw publishes — and NOT of fieldSeq, which this frame's draw pass
//     has not started filling yet. Asking the wrong list is not a subtlety here:
//     it made every branch below unreachable and the whole feature dead code.
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

// bounceFocusToIC is half (b): queue focus back onto the IC input. Queued rather
// than assigned (FocusField → applied at the next BeginFrame) so it survives the
// same frame's click-away unfocus no matter where the calling widget sits in draw
// order — the same reason every other FocusField caller queues.
//
// It is a no-op outside the courtroom and while a key-bind capture is armed, so a
// shared widget (the SFX picker also appears in the theme editor's preview) can
// call it unconditionally.
func (a *App) bounceFocusToIC() {
	if a.screen != ScreenCourtroom || a.capturingKey() {
		return
	}
	a.ctx.FocusField(icFieldID)
}

// bounceOnToggle is (b) for a CHECKBOX: pass the value the box just returned and
// the value the field still holds, assign the result back. A toggle that moved is
// a click, and AO2 wires those clicks straight to focus_ic_input
// (courtroom.cpp:502-506) — so:
//
//	a.icPreanim = a.bounceOnToggle(c.Checkbox(...), a.icPreanim)
//
// It exists because the alternative at seven call sites is seven hand-written
// three-line change tests, and the IC bar has TWO layouts (classic + themed) whose
// controls must not drift. Comparing rather than trusting the click is what keeps
// a mirrored field honest: Flip has two views of one bool, and the second view
// redraws with the value the first already wrote — same in, same out, no bounce.
//
// Immediate is not a caller, on purpose: see the canon table at the top.
func (a *App) bounceOnToggle(next, cur bool) bool {
	if next != cur {
		a.bounceFocusToIC()
	}
	return next
}
