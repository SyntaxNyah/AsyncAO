package courtroom

// This file owns ONE concern: the single door out of a one-shot preanimation,
// and the record of who walked through it.
//
// Why it exists. "The preanimation is randomly interrupted" is unreproducible
// by construction — four different things can end the animation, three of them
// asynchronous (a render callback, a 404 warning, a bound expiring), and the
// stage looks identical afterwards whichever one fired. Nothing recorded WHICH,
// so every report was a guess. Funnelling the release through endPreanim makes
// the answer a value the debug surface can read (LastPreanimEnd) or a hook can
// stream (OnPreanimEnd), at zero cost when nobody is listening.
//
// The funnel is also the fix for a real defect, not just instrumentation:
// releasing the one-shot was four scattered `PlayOnce = false` writes, so
// NotifyPreanimDone — the callback that CUTS the animation — was the only
// notifier of the pair without the staleness guard its sibling
// NotifyPreanimStarted carries (see the guard there). One door means one place
// where that check has to be right.

// PreanimEndReason names the thing that ended the one-shot preanimation that
// was on stage. It is diagnostic only: the stage result is the same for every
// reason (the speaker drops to the talk loop or to idle), so nothing branches
// on it and adding a reason cannot change behavior.
type PreanimEndReason int

const (
	// PreanimEndNone is the zero value: no preanimation has ended in this room
	// yet. Never reported by endPreanim.
	PreanimEndNone PreanimEndReason = iota
	// PreanimEndFinished — the render side reported the one-shot's last frame
	// (NotifyPreanimDone). This is the ONLY healthy reason for a preanim that
	// had art; every other one cut it short.
	PreanimEndFinished
	// PreanimEndTimeout — the bound expired before any "done" arrived:
	// PreanimTimeout for a still-decoding preanim, or the duration
	// NotifyPreanimStarted reported plus preanimPlaybackSlack for one that
	// played. Seeing this on a preanim that DID decode is the signature of the
	// "randomly interrupted" report.
	PreanimEndTimeout
	// PreanimEndMissing — the asset manager proved the preanim can never
	// resolve (NotifyAssetMissing; AO2 skips a missing preanim synchronously,
	// ../AO2-Client/src/courtroom.cpp play_preanim).
	PreanimEndMissing
	// PreanimEndTextSettled — the message's text finished and no immediate-mode
	// decoration was running, so the settled message parks the speaker on idle.
	PreanimEndTextSettled
	// PreanimEndNewMessage — the next message took the stage while this
	// animation was still playing (AO2's unpack_chatmessage resets anim_state).
	PreanimEndNewMessage
	// PreanimEndStageWiped — an area change / handshake wiped the stage out
	// from under the animation.
	PreanimEndStageWiped
	// preanimEndReasonCount bounds the reason table below (rule §17.4: every
	// table has a named cap, and String() can never index past it).
	preanimEndReasonCount
)

// preanimEndNames is indexed by PreanimEndReason; kept beside the constants so
// a new reason without a name fails the table-length test, not silently at
// runtime.
var preanimEndNames = [preanimEndReasonCount]string{
	PreanimEndNone:        "none",
	PreanimEndFinished:    "finished",
	PreanimEndTimeout:     "timeout",
	PreanimEndMissing:     "missing",
	PreanimEndTextSettled: "text-settled",
	PreanimEndNewMessage:  "new-message",
	PreanimEndStageWiped:  "stage-wiped",
}

// String names the reason for logs and the debug panel.
func (r PreanimEndReason) String() string {
	if r < 0 || r >= preanimEndReasonCount {
		return "unknown"
	}
	return preanimEndNames[r]
}

// endPreanim is the ONE door out of a one-shot preanimation: it clears the
// PlayOnce latch, records why, and notifies any listener. Callers keep owning
// what the speaker shows NEXT (talk loop vs idle) — that differs per site and
// is not this seam's concern.
//
// Idempotent by design: with no one-shot on stage there is nothing to end, so a
// second call (a wipe right after a new message, a 404 arriving after the
// natural finish) reports nothing rather than overwriting the real reason with
// a bookkeeping one.
//
// Game thread only, like every other Courtroom method.
func (c *Courtroom) endPreanim(reason PreanimEndReason) {
	if !c.Scene.Speaker.PlayOnce {
		return
	}
	c.Scene.Speaker.PlayOnce = false
	c.preanimEnd = reason
	if c.OnPreanimEnd != nil {
		c.OnPreanimEnd(reason)
	}
}

// LastPreanimEnd reports why the most recent one-shot preanimation in this room
// ended (PreanimEndNone before the first one). Read-only diagnostic: the
// courtroom never branches on it.
func (c *Courtroom) LastPreanimEnd() PreanimEndReason { return c.preanimEnd }
