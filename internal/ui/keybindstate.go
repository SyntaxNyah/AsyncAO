package ui

// keyBindState owns courtkeys state.
type keyBindState struct {
	// --- character keybinds (per server) ---
	// bindingFor is the character a wardrobe key-capture is armed for
	// ("" = none); charKeys/charKeysRev cache this server's binds for
	// per-frame lookups (refreshed on connect + edits only).
	bindingFor  string
	hkCapture   string            // settings Controls tab: hotkey id a key-capture is armed for ("" = none)
	charKeys    map[string]string // key name → character
	charKeysRev map[string]string // character → key name (badges)
	// --- showname keybinds (M6, global) ---
	// shownameBindFor is the showname a Settings key-capture is armed for
	// ("" = none); shownameKeys/Rev cache the global binds for per-frame
	// lookups (refreshed on connect + edits only, like charKeys).
	shownameBindFor string
	shownameKeys    map[string]string // key name → showname
	shownameKeysRev map[string]string // showname → key name (badges)
	// #126 custom style presets: stylePresetBindFor is the preset NAME awaiting a key-capture
	// ("" = none); stylePresetNameInput is the Style box's "name this mood" field.
	stylePresetBindFor   string
	stylePresetNameInput string
	// IC quick-phrases (ic_phrase.go): a bare key sends a canned IC line.
	// icPhraseBindFor is the phrase a Settings key-capture is armed for ("" = none);
	// icPhraseKeys caches the global key→phrase binds for the per-frame dispatch.
	icPhraseBindFor string
	icPhraseKeys    map[string]string // key name → IC phrase
	// ghostWarm dedupes the pair-panel ghost editor's sprite prefetches.
	ghostWarm   map[string]string
	showHotkeys bool
	hkCache     []hkEntry // hotkey cheat-sheet rows, rebuilt once per open (not per frame)
}
