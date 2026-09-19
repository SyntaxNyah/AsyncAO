package ui

// The outgoing MS IMMEDIATE (pre_no_interrupt) decision — one concern, one place.
//
// AO2's pre_no_interrupt flag only means anything when ui_pre is also on: a line
// whose "Pre" toggle is off plays no preanimation, so there is nothing for
// "Immediate" to make non-interrupting. Shipping IMMEDIATE=1 on its own (Pre off)
// still read as an immediate preanimation on receivers that key off the raw flag,
// which is issue #122. Extracted so the rule is drivable on its own
// (icimmediate_test.go) rather than only through the 200-line send builder.

// outgoingImmediate is the MS Immediate field this message ships. immediate is the
// IC-row "Immediate" toggle (App.icImmediate); preanim is the "Pre" toggle
// (App.icPreanim). True only when both are on — preanim alone decides whether a
// preanimation exists to be made non-interrupting.
func outgoingImmediate(immediate, preanim bool) bool {
	return immediate && preanim
}
