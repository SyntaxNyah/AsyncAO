package ui

// The outgoing MS IMMEDIATE (pre_no_interrupt) decision — one concern, one place.
// which is issue #122. Extracted so the rule is drivable on its own
// (icimmediate_test.go) rather than only through the 200-line send builder.

// outgoingImmediate is the MS Immediate field this message ships. immediate is the
// IC-row "Immediate" toggle (App.icImmediate); preanim is the "Pre" toggle
// (App.icPreanim). True only when both are on — preanim alone decides whether a
// preanimation exists to be made non-interrupting.
func outgoingImmediate(immediate, preanim bool) bool {
	return immediate && preanim
}
