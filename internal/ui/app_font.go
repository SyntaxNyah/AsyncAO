package ui

import ()

// fontState is App's "font override pipeline" banner group lifted into a named component: the fields
// used to sit flat on App, readable at depth 1 from every ui file. They are
// reached as a.font.<field> now.
type fontState struct {
	fontRes chan fontLoad
	// themeFontFile is the active theme's bundled font path (.ttf/.otf), resolved
	// off-thread on theme apply; applyFontConfig uses it when no manual font / the
	// dyslexia toggle is set (#6).
	themeFontFile string
	// themeFaceNames / panelFontMissing are Settings-screen diagnostics for the
	// per-panel font overrides: the file each element resolved to, and the
	// families that resolved to NOTHING. Landed on theme apply and read only
	// while the Settings screen is open — never on a draw path.
	themeFaceNames   []string
	panelFontMissing []string
	// themeFontWarn is the ready-made sentence about the THEME's own unresolved
	// families. Kept after the toast has faded so the Settings screen can still
	// explain it — the toast is gone by the time anyone goes looking for why the
	// font is wrong.
	themeFontWarn string
	// fontWarnDlg is the missing-fonts modal. On App and NOT on sessionState,
	// unlike disconnectDlg: that dialog belongs to one tab's frozen session, while
	// a theme is global to the client — one theme, one warning, whichever tab
	// happens to be in front when it applies.
	fontWarnDlg fontWarnDialog
	// serverNoticeDlg is the server's-own-words modal (servernotice.go): a kick/ban
	// reason, a lockdown message, a BB popup notice. On App for the same reason
	// fontWarnDlg is, only more sharply: resetSessionState rebuilds sessionState
	// wholesale on DISCONNECT, so a removal reason parked there would be erased by
	// the very teardown that produced it, before the lobby ever drew.
	serverNoticeDlg serverNoticeDialog
	// chromeMeasure is the shared modal-wrap metric, built once on first use because
	// a closure allocates and it is consulted every frame a modal is open. See
	// chromeWrapMeasure (servernotice.go).
	chromeMeasure func(string) int32
	// panelSizeEdit is the RAW text of each per-panel size box while it is being
	// typed in, keyed by the AO2 element id. It exists because a half-typed number
	// is not a size, and clamping one into range mid-keystroke made anything below
	// ten impossible to enter. Read only while that box has focus; the saved
	// preference is the truth otherwise, so nothing here has to be cleaned up.
	panelSizeEdit map[string]string
	// Color-emoji fallback face: the system emoji font (e.g. Segoe UI Emoji) is read
	// off-thread the FIRST time a message needs it (emojiLoadStarted gates the one
	// read), landing on emojiFontRes → ctx.SetEmojiFont. Lazy so a user who never
	// types emoji never pays the 11.9 MB read.
	emojiFontRes     chan []byte
	emojiLoadStarted bool
	// Broad-Unicode TEXT fallback: system fonts (Segoe UI, Ebrima, Nirmala, Segoe UI
	// Symbol) covering scripts the embedded font lacks (Cyrillic→Tifinagh→Indic→
	// symbols). Read off-thread the FIRST time a message has a non-ASCII rune
	// (fallbackLoadStarted gates the one read), landing on fallbackFontRes →
	// ctx.SetFallbackFonts. Lazy so a pure-ASCII session never loads them and the
	// single-font fast path (pickSet len==1) is untouched — zero perf change.
	fallbackFontRes     chan [][]byte
	fallbackLoadStarted bool
	// CJK tier (Han/Kana + Hangul), read off-thread only when a CJK letter is drawn —
	// independent latch so the broad load can't swallow it (a Cyrillic name usually
	// comes first). 13-35 MB, so kept out of the common non-ASCII path.
	cjkFontRes     chan [][]byte
	cjkLoadStarted bool
	// System-font census (fontcensus.go): the LAST resort, reached only when a rune
	// nothing in the curated chain covers actually reaches the draw. The render
	// thread sends those runes on censusReq and installs whatever face comes back on
	// censusRes; the goroutine that walks the machine's font directories is started
	// on the first miss, so a session that never hits one never scans anything.
	censusReq     chan rune
	censusRes     chan []byte
	censusStarted bool
	missingBuf    []rune
	// Custom missingno placeholder (user-chosen image path, default = the embedded
	// AO2 glitch). The file read + decode runs OFF-THREAD (rule §17.2 — no
	// synchronous disk I/O on the render path); the decoded page lands on
	// errorSpriteRes and pollErrorSprite UploadPinned's it over render.MissingKey
	// on the render thread. errorSpriteErr carries the inline Settings error label
	// on a failed load (kept for the Settings row to show). See missingno.go.
	errorSpriteRes chan errorSpriteLoad
	errorSpriteErr string
}
