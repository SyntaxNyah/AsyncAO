package courtroom

// PreanimEndReason names what ended a one-shot preanimation; it is diagnostic only and nothing branches on it.
type PreanimEndReason int

const (
	// PreanimEndNone means no preanimation has ended in this room yet.
	PreanimEndNone PreanimEndReason = iota
	// PreanimEndFinished: the render side reported the last frame, the only healthy reason for a preanim with art.
	PreanimEndFinished
	// PreanimEndTimeout: PreanimTimeout expired while decoding, or duration plus preanimPlaybackSlack while playing.
	PreanimEndTimeout
	// PreanimEndMissing: the preanim can never resolve (AO2 skips it synchronously, courtroom.cpp play_preanim).
	PreanimEndMissing
	// PreanimEndTextSettled: the text finished with no immediate-mode decoration running, so the speaker parks on idle.
	PreanimEndTextSettled
	// PreanimEndNewMessage: the next message took the stage (AO2's unpack_chatmessage resets anim_state).
	PreanimEndNewMessage
	// PreanimEndStageWiped: an area change or handshake wiped the stage.
	PreanimEndStageWiped
	// preanimEndReasonCount sizes the name table (hard rule 4).
	preanimEndReasonCount
)

// preanimEndNames is indexed by PreanimEndReason.
var preanimEndNames = [preanimEndReasonCount]string{
	PreanimEndNone:        "none",
	PreanimEndFinished:    "finished",
	PreanimEndTimeout:     "timeout",
	PreanimEndMissing:     "missing",
	PreanimEndTextSettled: "text-settled",
	PreanimEndNewMessage:  "new-message",
	PreanimEndStageWiped:  "stage-wiped",
}

func (r PreanimEndReason) String() string {
	if r < 0 || r >= preanimEndReasonCount {
		return "unknown"
	}
	return preanimEndNames[r]
}

// endPreanim is the only way out of a one-shot; callers choose what the speaker shows next. Game thread only.
func (c *Courtroom) endPreanim(reason PreanimEndReason) {
	if !c.Scene.Speaker.PlayOnce {
		return
	}
	c.Scene.Speaker.PlayOnce = false
	c.preanimEnd = reason
	if c.Env != nil {
		c.Env.OnPreanimEnd(reason)
	}
}

// LastPreanimEnd reports why the last preanimation here ended; diagnostic only.
func (c *Courtroom) LastPreanimEnd() PreanimEndReason { return c.preanimEnd }
