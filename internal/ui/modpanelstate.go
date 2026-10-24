package ui

// modPanelState owns modUI state.
type modPanelState struct {
	showModDash      bool     // the mod (ban/kick) dashboard panel is open
	showCMPanel      bool     // the separate CM (area control) panel is open
	cmRosterScroll   int32    // CM panel roster scroll offset
	bulkBoxUIDs      []string // frozen snapshot of the ticked UIDs when a bulk box opens
	modTemplatesEdit bool     // the ban box's reason-template editor is open (× removes a chip)
	modDurEdit       bool     // the ban box's custom-duration editor is open (× removes a chip)
	modDurInput      string   // the "add custom duration" field's draft ("45m", "2 days", …)
	modReason        string
}
