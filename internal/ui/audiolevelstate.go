package ui

// audioLevelState owns audio state.
type audioLevelState struct {
	// dl is the opt-in single-asset downloader (off by default). The pause flag
	// shared with the worker lives in App proper (a.dlPaused), NOT here — this
	// struct is copied by value on tab park/activate, which can't copy an
	// atomic, and the worker needs one stable global flag anyway.
	dl downloader
	// sfxMuted is the SFX mute (Mute SFX hotkey); showHotkeys toggles the F1
	// hotkey cheat-sheet overlay; musicDucked tracks whether music is
	// currently ducked under a playing message (transition-driven).
	// volume strip (#10). All four PERSIST (config.AssetPreferences'
	sfxMuted    bool
	masterMuted bool
	musicMuted  bool
	blipMuted   bool
}
