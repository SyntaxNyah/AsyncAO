package ui

// AO2's pre_no_interrupt only means anything with ui_pre on: with Pre off there is no preanimation to make
// non-interrupting, yet receivers that key off the raw flag still read IMMEDIATE=1 as an immediate preanimation.

// outgoingImmediate is the MS Immediate field: true only when the "Immediate" and "Pre" toggles are both on.
func outgoingImmediate(immediate, preanim bool) bool {
	return immediate && preanim
}
