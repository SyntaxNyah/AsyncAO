package ui

// wins is the floating-window registry: the geometry of every movable, resizable,
// non-blocking box in the client (floatwin.go). It is a leaf service owned by env
// because window geometry is GLOBAL — it must survive a tab park/restore, which is
// also why it is not in sessionState. Open state is not here: it is per-tab (a
// sessionState flag) or an App-level bool, whichever the panel needs.
type wins struct {
	// pairWin / modWin / cmWin / hkWin / evidWin are the Pairing, Mod dashboard, CM,
	// Hotkey-sheet, Evidence, mod-call, message, voice, ban and debug panels — each a
	// movable/resizable box (chat stays live behind it) rather than a modal.
	pairWin, modWin, cmWin, hkWin, evidWin, modcallWin, msgWin, voiceWin, banWin, debugWin floatWin
	// timerWin / loginWin / pairPopWin are the three that used to be return-to-top
	// modals (#31: "bringing up the Extras → Timer hides all the other UI"). They took
	// the whole courtroom pass for a countdown, a two-field credential form and a UID
	// box, which is exactly the wrong trade — you want to watch the scene while the
	// timer runs. Same floatWin treatment as the panels above.
	timerWin, loginWin, pairPopWin floatWin
	// piecesWin is the Hide-UI-pieces panel's floating window. It used to be nailed to
	// the bottom-right corner with no way to move it — dragging did nothing and the
	// layout editor did not offer it — so a panel that landed awkwardly could not be
	// shifted at all.
	piecesWin floatWin
	// clientWin is the pinned server's free-floating "second client" window over the
	// primary one (drag it anywhere, not a fixed split).
	clientWin floatWin
}
