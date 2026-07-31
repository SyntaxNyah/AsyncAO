package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The menu bar's CONTENT (#14 / #26 / #29 / #35). menubar_test.go pins the widget —
// fences, input grammar, alloc budget. These pin what the rows actually do: that each
// one reaches the same shipped code path as the affordance it replaced, that the
// lobby's utility row really shed those buttons, and that a row which cannot act says
// so instead of silently doing nothing.

// --- Servers: the migrated lobby buttons ---------------------------------------------

// TestMenuServersRowsReachTheLobby pins that the three lobby rows work from a screen
// that draws no lobby. They are reachable from the courtroom (the tab strip
// advertises "click to browse the lobby"), so a Phone Book flip or a focused
// direct-connect field that landed on a screen drawing neither would be a silent
// no-op — exactly what the disabled-not-absent rule exists to prevent.
func TestMenuServersRowsReachTheLobby(t *testing.T) {
	a := stageMenuBarApp(t)
	a.screen = ScreenCourtroom

	a.fireMenuItem(menuRow(t, menuServersTitle, "Phone Book"))
	if a.screen != ScreenLobby {
		t.Fatalf("the Phone Book row must go to the lobby first, screen=%v", a.screen)
	}
	if !a.phoneBookPage {
		t.Error("and it must actually switch the page on")
	}
	if !menuRow(t, menuServersTitle, "Phone Book").checked(a, 0) {
		t.Error("the row must then read as checked")
	}

	// Direct connect leaves the Phone Book page, because the field only draws on the
	// all-servers page and FocusField silently drops an id nothing draws.
	a.screen = ScreenCourtroom
	a.fireMenuItem(menuRow(t, menuServersTitle, "Direct connect…"))
	if a.screen != ScreenLobby || a.phoneBookPage {
		t.Fatalf("Direct connect must land on the all-servers lobby page (screen=%v phoneBook=%v)", a.screen, a.phoneBookPage)
	}
	if a.ctx.focusNext != directConnectFieldID {
		t.Errorf("Direct connect must queue focus onto %q, got %q", directConnectFieldID, a.ctx.focusNext)
	}
}

// TestMenuServersPhoneBookSharesTheLobbySetter pins the no-second-copy rule: the
// header button and the menu row both call setPhoneBookPage, which is what drops the
// stale row selection when the list is re-filtered. A menu row that only flipped the
// bool would leave selServer pointing at a row that is no longer there.
func TestMenuServersPhoneBookSharesTheLobbySetter(t *testing.T) {
	a := stageMenuBarApp(t)
	a.selServer, a.descLines, a.lobbyScroll = 4, []string{"stale"}, 120

	a.fireMenuItem(menuRow(t, menuServersTitle, "Phone Book"))
	if a.selServer != -1 || a.descLines != nil || a.lobbyScroll != 0 {
		t.Errorf("the page swap must clear the selection/description/scroll: sel=%d desc=%v scroll=%d",
			a.selServer, a.descLines, a.lobbyScroll)
	}

	// Idempotent: asking for the page you are already on must not wipe a selection
	// the user is reading (the Direct-connect row leaves the page unconditionally).
	a.selServer = 7
	a.setPhoneBookPage(true)
	if a.selServer != 7 {
		t.Errorf("a no-op page set cleared the selection (sel=%d)", a.selServer)
	}
}

// TestMenuServersRefreshAndPingGateOnTheirOwnState pins the two rows whose shipped
// helpers refuse to re-enter: RefreshServers while a fetch is in flight, startPinging
// while a sweep is. Both would be dead clicks, so both dim instead — and for the ping
// row the dimming is also the only progress feedback a static menu row can give (the
// button it replaced said "Pinging…" in its label).
func TestMenuServersRefreshAndPingGateOnTheirOwnState(t *testing.T) {
	a := stageMenuBarApp(t)
	refresh := menuRow(t, menuServersTitle, "Refresh server list")
	ping := menuRow(t, menuServersTitle, "Sort by connect time")

	if !a.menuItemEnabled(refresh) || !a.menuItemEnabled(ping) {
		t.Fatal("an idle lobby must offer both rows")
	}
	a.lobbyFetching = true
	if a.menuItemEnabled(refresh) {
		t.Error("Refresh must dim while a fetch is in flight — RefreshServers coalesces it away")
	}
	a.lobbyFetching = false
	a.pinging = true
	if a.menuItemEnabled(ping) {
		t.Error("the connect-time row must dim while a sweep runs — startPinging refuses a second one")
	}
	a.pinging = false

	// The check tracks pingMode, and turning it OFF re-sorts immediately (pingMode
	// alone only picks the comparator for the NEXT sort).
	a.pingMode = true
	a.selServer = 3
	if !ping.checked(a, 0) {
		t.Error("the row must read as checked while the lobby is in connect-time order")
	}
	a.fireMenuItem(ping)
	if a.pingMode {
		t.Fatal("firing the row again must leave connect-time order")
	}
	if a.selServer != -1 {
		t.Errorf("leaving connect-time order must re-sort (applyServerSort), sel=%d", a.selServer)
	}
}

// TestLobbyListActionsAreOnTheLobby pins where the SERVER-LIST actions live.
//
// Refresh and the connect-time sort came back out of the menu by request: they act
// on the server list, so a menu carrying them is dead weight the moment you are in
// a server, and a refresh button belongs beside the thing it refreshes. The
// cluster's count MUST match the number of cursor steps drawLobby takes, because
// that constant is what turns "does the row fit?" into arithmetic instead of a
// measured pass.
//
// Both surfaces survive on purpose and call the same setters, so they cannot
// drift — this test is what stops one of them quietly growing a second copy.
func TestLobbyListActionsAreOnTheLobby(t *testing.T) {
	// The HEADER cluster keeps exactly one button. Refresh and Sort deliberately do
	// NOT join it: that row is shared with the Privacy/Glossary/What's New trio, and
	// three buttons there squeezed the trio to nothing at MinWindowW — which would
	// strand What's New, whose only other home is that row. They live on the list's
	// own strip instead. TestLobbyHeaderRowSheds... is what caught that.
	if lobbyUtilBtnCount != 1 {
		t.Errorf("the header cluster lays out %d buttons, want 1 — Refresh/Sort belong on the list strip", lobbyUtilBtnCount)
	}
	if !strings.Contains(readSourceFile(t, "screens.go"), "lobbyListActionsDropY") {
		t.Error("the server list's action strip is gone — Refresh/Sort would be menu-only again")
	}
	// Still reachable from the menu, for the keyboard and for muscle memory.
	for _, tc := range []struct{ menuTitle, label string }{
		{menuServersTitle, "Refresh server list"},
		{menuServersTitle, "Sort by connect time"},
		{menuServersTitle, "Phone Book"},
		{menuExtrasTitle, "Settings"},
		{menuHelpTitle, "About AsyncAO"},
		{menuHelpTitle, "Chat logs"},
	} {
		menuRow(t, tc.menuTitle, tc.label) // fatals when the row is missing
	}
	// The Servers menu keeps only what is useful FROM a server. Anything else there
	// is the dead weight this change set out to remove.
	for _, row := range menuServersItems {
		switch row.label {
		case "Disconnect", "Quit AsyncAO", "Direct connect…",
			"Refresh server list", "Sort by connect time", "Phone Book", "":
		default:
			t.Errorf("unexpected Servers row %q — decide whether it is lobby-only and move it", row.label)
		}
	}
}

// --- View: theme fit (#29) -----------------------------------------------------------

// TestMenuKeepAspectRatioReusesTheFitSetter pins the checkable's semantics AND that it
// does not fork SetThemeFit: unticking goes to Stretch (the one mode that distorts),
// re-ticking returns to the SHIPPED DEFAULT, and both invalidate the layout cache
// like every other fit mutation.
func TestMenuKeepAspectRatioReusesTheFitSetter(t *testing.T) {
	a := stageMenuBarApp(t)
	keep := menuRow(t, "View", "Keep aspect ratio")

	if !keep.checked(a, 0) {
		t.Fatalf("the shipped default (%d) keeps the aspect ratio, so the row must start checked", config.DefaultThemeFit)
	}
	a.themeLay.valid = true
	a.fireMenuItem(keep)
	if got := a.d.Prefs.ThemeFitMode(); got != config.ThemeFitStretch {
		t.Errorf("unticking must select Stretch, got mode %d", got)
	}
	if a.themeLay.valid {
		t.Error("a fit mutation must invalidate the theme layout cache")
	}
	if keep.checked(a, 0) {
		t.Error("Stretch is the mode that does NOT keep the aspect ratio")
	}

	a.themeLay.valid = true
	a.fireMenuItem(keep)
	if got := a.d.Prefs.ThemeFitMode(); got != config.DefaultThemeFit {
		t.Errorf("re-ticking must return to the shipped default %d, got %d", config.DefaultThemeFit, got)
	}
	if a.themeLay.valid {
		t.Error("a fit mutation must invalidate the theme layout cache")
	}

	// Every aspect-keeping mode reads as checked, so the tick never lies about a
	// mode the Theme fit submenu can select.
	for _, mode := range []int{config.ThemeFitNative, config.ThemeFitLetterbox, config.ThemeFitCrop, config.ThemeFitCustom} {
		a.d.Prefs.SetThemeFit(mode)
		if !keep.checked(a, 0) {
			t.Errorf("mode %d preserves the canvas shape, so the row must read as checked", mode)
		}
	}
}

// TestMenuViewPlainBackdropsSharesTheSetting pins that the View row is the same
// preference the Settings screen writes, not a second flag.
func TestMenuViewPlainBackdropsSharesTheSetting(t *testing.T) {
	a := stageMenuBarApp(t)
	row := menuRow(t, "View", "Plain backdrops")
	before := a.d.Prefs.PlainLobbyOn()
	if row.checked(a, 0) != before {
		t.Fatal("the row must read the live preference")
	}
	a.fireMenuItem(row)
	if a.d.Prefs.PlainLobbyOn() == before {
		t.Error("the row must flip the shipped PlainLobby preference")
	}
}

// --- Window (#35) --------------------------------------------------------------------

// TestMenuWindowThemeDesignSizeFollowsTheSharedOffer pins that the row reuses
// themeDesignSizeOffer rather than re-deriving "does this theme declare a canvas" —
// the same predicate the Settings button and the automatic snap-on-import consult, so
// all three agree about when a theme canvas is meaningful.
func TestMenuWindowThemeDesignSizeFollowsTheSharedOffer(t *testing.T) {
	a := stageMenuBarApp(t)
	row := menuRow(t, "Window", "Theme's design size")

	if a.menuItemEnabled(row) {
		t.Error("with no theme canvas the row must be offered DISABLED, not act silently")
	}
	a.themeRectsOrig = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 256, H: 192},
	}
	a.d.Prefs.SetThemeLayout(true)
	if !a.menuItemEnabled(row) {
		t.Error("a theme that declares a canvas (with the themed layout on) must enable the row")
	}
	_, _, ok := a.themeDesignSizeOffer()
	if ok != a.menuItemEnabled(row) {
		t.Error("the row's predicate and themeDesignSizeOffer must be the same answer")
	}
	// The themed courtroom layout being OFF means the canvas is not what is drawn,
	// so the offer — and the row — must withdraw together.
	a.d.Prefs.SetThemeLayout(false)
	if a.menuItemEnabled(row) {
		t.Error("with the themed layout off the canvas is not on screen: the row must dim")
	}
}

// TestMenuWindowRowsExist pins the three sizing rows #35 asked for. They are pure
// one-line calls into applyWindowSize-backed helpers, which is the point: the clamp,
// the un-maximize and the persist are NOT reimplemented here, so there is nothing
// else to assert about them that windowsize_test.go does not already own.
func TestMenuWindowRowsExist(t *testing.T) {
	for _, label := range []string{"Restore default size", "Theme's design size", "Fit to screen", "Fullscreen"} {
		if row := menuRow(t, "Window", label); row.act == nil {
			t.Errorf("the %q row has no action", label)
		}
	}
}

// --- Extras: the toolbox migration (#26) ---------------------------------------------

// TestMenuExtrasCarriesEveryToolboxCommand is the migration itself: every command the
// floating chip strip offers is reachable from the menu, so the strip is no longer
// the only way to reach Theater / Edit layout / the per-piece panel — which is what
// made the reporter keep pressing the theme's "Call Mod" button underneath it.
//
// The Pin chip is deliberately NOT mirrored: it latches the FLOATING strip open, a
// property of a surface the menu replaces rather than a command.
func TestMenuExtrasCarriesEveryToolboxCommand(t *testing.T) {
	for _, label := range []string{"Theater mode", "Edit layout…", "Hide UI pieces…", "Floating toolbox"} {
		menuRow(t, menuExtrasTitle, label)
	}
	// The layout editor must stay reachable whatever happens to the strip.
	if menuRow(t, menuExtrasTitle, "Edit layout…").act == nil {
		t.Fatal("the layout editor must be reachable from the menu bar")
	}
}

// TestMenuExtrasTogglesTheFloatingToolbox pins the row that makes the migration
// worth something: the strip can now be switched off from a surface that exists on
// every screen. Before the menu bar the only ways to hide it were the very chips it
// was covering, a hotkey, or the Settings screen.
func TestMenuExtrasTogglesTheFloatingToolbox(t *testing.T) {
	a := stageMenuBarApp(t)
	a.seedHiddenFromPrefs() // the real App seeds the hidden-chrome map at boot, before any frame
	row := menuRow(t, menuExtrasTitle, "Floating toolbox")

	if !row.checked(a, 0) {
		t.Fatal("the strip ships visible, so the row must start checked")
	}
	a.fireMenuItem(row)
	if !a.panelHidden(panelToolbox) {
		t.Fatal("unticking must hide the floating toolbox through the shipped hidden-chrome set")
	}
	if row.checked(a, 0) {
		t.Error("and the row must then read as unchecked")
	}
	a.fireMenuItem(row)
	if a.panelHidden(panelToolbox) {
		t.Error("re-ticking must bring the strip back")
	}
}

// TestMenuExtrasCourtroomRowsDimElsewhere pins the gate that stops the bar
// soft-locking itself. An armed layout editor owns the top band, so the strip stops
// painting entirely; arming one from the lobby — a screen that never draws the editor,
// because both editors live inside drawCourtroom — would leave no way out.
func TestMenuExtrasCourtroomRowsDimElsewhere(t *testing.T) {
	a := stageMenuBarApp(t)
	rows := []string{"Theater mode", "Edit layout…", "Hide UI pieces…"}

	a.screen = ScreenLobby
	for _, label := range rows {
		row := menuRow(t, menuExtrasTitle, label)
		if a.menuItemEnabled(row) {
			t.Errorf("%q must be disabled off the courtroom", label)
		}
		a.menuOpen = menuExtrasTitle
		a.fireMenuItem(row)
		if !a.menuBarOpen() {
			t.Errorf("%q fired while disabled", label)
		}
		a.closeMenuBar()
	}
	if a.classicEdit || a.layoutEdit {
		t.Fatal("a disabled Edit layout row armed an editor the lobby never draws — the bar would be inert forever")
	}

	a.screen = ScreenCourtroom
	for _, label := range rows {
		if !a.menuItemEnabled(menuRow(t, menuExtrasTitle, label)) {
			t.Errorf("%q must be live in the courtroom", label)
		}
	}
}

// --- Help ----------------------------------------------------------------------------

// TestMenuHelpOpensEachDocument pins the Help rows, including the two that carry a
// section index as their arg (the reason menuItem has an arg at all — it keeps the
// handlers non-capturing package-level functions).
func TestMenuHelpOpensEachDocument(t *testing.T) {
	a := stageMenuBarApp(t)
	a.screen = ScreenLobby

	// The two args are positions in helpSectionNames, so a reorder there would
	// silently swap the two rows' documents.
	if len(helpSectionNames) <= helpTabPrivacy ||
		helpSectionNames[helpTabPrivacy] != "Privacy" || helpSectionNames[helpTabGlossary] != "Glossary" {
		t.Fatalf("the Help rows' section indexes no longer match helpSectionNames %v", helpSectionNames)
	}

	a.fireMenuItem(menuRow(t, menuHelpTitle, "Privacy"))
	if a.screen != ScreenHelp || a.helpTab != helpTabPrivacy {
		t.Errorf("Privacy → screen %v tab %d, want Help/%d", a.screen, a.helpTab, helpTabPrivacy)
	}
	if a.prevScreen != ScreenLobby {
		t.Errorf("prevScreen = %v, want the screen we came from — Esc/Back rely on it", a.prevScreen)
	}
	// Re-picking a section while already on Help must not overwrite prevScreen, or
	// backing out would bounce between Help and itself.
	a.fireMenuItem(menuRow(t, menuHelpTitle, "Glossary"))
	if a.helpTab != helpTabGlossary {
		t.Errorf("Glossary → tab %d, want %d", a.helpTab, helpTabGlossary)
	}
	if a.prevScreen != ScreenLobby {
		t.Errorf("prevScreen = %v after a second Help pick, want it untouched", a.prevScreen)
	}

	a.screen, a.prevScreen = ScreenLobby, ScreenLobby
	a.fireMenuItem(menuRow(t, menuHelpTitle, "Chat logs"))
	if a.screen != ScreenLogs {
		t.Errorf("Chat logs → screen %v, want the log browser", a.screen)
	}
	a.screen = ScreenLobby
	a.fireMenuItem(menuRow(t, menuHelpTitle, "For server owners…"))
	if a.screen != ScreenServerHelp {
		t.Errorf("For server owners → screen %v, want the upgrade guide", a.screen)
	}
}

// TestAsyncAOChromeLivesOutsideTheCanvas pins issue #21 rule (c): AsyncAO's own
// controls do not draw inside a theme's design canvas.
//
// A row of ★ Extras / Hotkeys / Restyle / Mod / CM chips used to be pinned at the
// raw window's bottom-left whenever a theme was active. Under aceattorney2x that
// is inside music_list = 0,323,216,277, so it painted AND clicked through the
// theme's own jukebox. No design key existed for a theme author to move it, so the
// commands moved out of the canvas entirely.
func TestAsyncAOChromeLivesOutsideTheCanvas(t *testing.T) {
	// Every command the deleted chip row carried must still be reachable.
	for _, row := range []struct{ menuTitle, label string }{
		{menuExtrasTitle, "AsyncAO Extras box"},
		{menuExtrasTitle, "Restyle character…"},
		{menuExtrasTitle, "Mod dashboard"},
		{menuExtrasTitle, "CM area controls"},
		{menuHelpTitle, "Hotkeys"}, // the chip row's sibling; Help is its home
	} {
		menuRow(t, row.menuTitle, row.label) // fatals when the row is missing
	}
	// And the row itself must stay gone, along with the memo that existed only to
	// keep its tooltip alloc-free.
	src, err := os.ReadFile("theme_layout.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"drawThemedExtrasButton", "extrasTipText", "extrasTipBody"} {
		if strings.Contains(string(src), gone+"(") {
			t.Errorf("theme_layout.go still calls %q — AsyncAO chrome is back inside the design canvas", gone)
		}
	}
}

// TestFxAndEmojiAreReachableUnderATheme pins the regression that the themed IC-bar
// rewrite introduced and an adversarial review caught.
//
// Text FX and the emoji picker are the only two IC controls with NO AO2 design
// key. The themed bar draws them solely at the asyncao_ic_fx / asyncao_ic_emoji
// override rects, which no stock AO2 theme declares — and the cram row that used
// to host them was deleted. For one commit they were unreachable under every real
// theme: no menu row, no hotkey, no Extras-box entry. icEffect is sticky session
// state, so an effect armed in the classic layout would then wrap every line with
// no way to clear it.
func TestFxAndEmojiAreReachableUnderATheme(t *testing.T) {
	a := stageMenuBarApp(t)
	for _, label := range []string{"Text FX…", "Emoji picker…"} {
		row := menuRow(t, menuExtrasTitle, label) // fatals when missing
		if row.act == nil {
			t.Errorf("%q has no action", label)
		}
	}
	// The rows must actually toggle the pickers, not merely exist.
	fx := menuRow(t, menuExtrasTitle, "Text FX…")
	before := a.showFxPicker
	fx.act(a, fx.arg)
	if a.showFxPicker == before {
		t.Error("the Text FX row did not toggle the picker")
	}
	em := menuRow(t, menuExtrasTitle, "Emoji picker…")
	beforeEm := a.showEmojiPicker
	em.act(a, em.arg)
	if a.showEmojiPicker == beforeEm {
		t.Error("the Emoji row did not toggle the picker")
	}
}

// TestFxPickerAnchorsOnScreenWithoutAButton pins the other half: opened from the
// menu there is no FX button on screen, so fxBtnRect is the zero value and the
// on-screen clamps would pin the list to the top-left corner.
func TestFxPickerAnchorsOnScreenWithoutAButton(t *testing.T) {
	a := stageMenuBarApp(t)
	a.fxBtnRect = sdl.Rect{} // never drawn: a theme that declares no asyncao_ic_fx
	const w, h = 1280, 720
	r := a.fxPickerRect(w, h)
	if r.X <= 8 && r.Y <= 8 {
		t.Errorf("picker pinned to the corner at %+v — it should centre when it has no anchor", r)
	}
	if r.X < 0 || r.Y < 0 || r.X+r.W > w || r.Y+r.H > h {
		t.Errorf("picker %+v is off-screen in a %dx%d window", r, w, h)
	}
}

// TestModAndCMRowsGreyOutRatherThanVanish pins the one behaviour change in moving
// those two commands. The chips DISAPPEARED when you did not hold the role, which
// reads as the feature not existing; a menu row that greys out says the command is
// there and the server has not granted it.
func TestModAndCMRowsGreyOutRatherThanVanish(t *testing.T) {
	a := stageMenuBarApp(t)
	for _, label := range []string{"Mod dashboard", "CM area controls"} {
		row := menuRow(t, menuExtrasTitle, label)
		if row.enabled == nil {
			t.Errorf("%q has no enabled predicate — it would always be clickable", label)
			continue
		}
		if row.enabled(a, row.arg) {
			t.Errorf("%q is enabled without the role", label)
		}
	}
}

// TestWhatsNewLeftTheHelpMenu pins where the release notes live. They moved to the
// LOBBY header beside Privacy and Glossary, because notes are read between sessions
// rather than mid-trial, and the unread badge the Help row carried sat on the Help
// TITLE where it overlapped the last glyph and read as a rendering defect.
//
// Hotkeys and About AsyncAO deliberately STAY in the menu: they are useful from any
// screen, which release notes are not.
func TestWhatsNewLeftTheHelpMenu(t *testing.T) {
	for _, row := range menuHelpItems {
		if row.label == "What's New" {
			t.Error("What's New is back in the Help menu — it belongs on the lobby header")
		}
	}
	for _, keep := range []string{"Hotkeys", "About AsyncAO"} {
		menuRow(t, menuHelpTitle, keep) // fatals when the row is missing
	}
}

// TestMarkChangelogSeenStampsTheBuild pins the half of the old badge mechanism that
// survived: opening the notes records which build was read. Nothing displays
// "unread" any more, but the stamp is the only record of it and a future
// notification would need it.
func TestMarkChangelogSeenStampsTheBuild(t *testing.T) {
	a := stageMenuBarApp(t)
	const stale = "0.0.0-not-this-build"
	a.d.Prefs.SetChangelogSeen(stale)

	a.markChangelogSeen()
	if got := a.d.Prefs.ChangelogSeenVersion(); got == stale {
		t.Error("markChangelogSeen must stamp the current build as seen")
	}
}

// TestMenuBarDrawsNoUnreadBadge pins the removal. A green dot rode the Help title
// and hung off its top-right corner, so it overlapped the title's last glyph and
// read as a rendering defect in the strip rather than as a notification. It also
// nagged about something nobody asked to be told. This guards the whole mechanism
// staying gone rather than just the draw call: a reintroduced badge would need one
// of these symbols back.
func TestMenuBarDrawsNoUnreadBadge(t *testing.T) {
	src, err := os.ReadFile("menubar.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"drawUnreadDot", "menuBarBadgeAnchor", "menuBarBadgeDropY", "menuBarBadgeInsetX", "changelogUnread"} {
		if strings.Contains(string(src), gone) {
			t.Errorf("menubar.go still references %q — the unread badge is back on the strip", gone)
		}
	}
}
