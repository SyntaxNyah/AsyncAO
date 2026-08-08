package ui

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Settings → Formats. Everything that decides WHICH file type AsyncAO asks a
// server for lives here, and nowhere else.
//
// It exists because those controls were previously split three ways — the
// per-server profile and the audio fallbacks on Assets, the probe order,
// per-type fallbacks, autodetect and desk policy on Power user, the
// learned-format tools on Assets → Cache — so a player whose emotes rendered as
// nothing had to already know the answer in order to find it. The zero-fallback
// default (exactly one network probe per asset) is deliberate and unchanged;
// what changes is that its consequence is now explained, its per-server escape
// hatch is one click, and the state it learned is visible.

// imageTypeNames are the asset classes whose image format is user-tunable, in
// the order they're listed on this tab (icons first — that's the wall of art a
// player sees before anything else loads).
var imageTypeNames = []string{
	config.TypeCharIcon,
	config.TypeCharSprite,
	config.TypeBackground,
	config.TypeDeskOverlay,
	config.TypeShoutBubble,
	config.TypeEmoteButton,
	config.TypeMisc,
}

// audioTypeNames are the asset classes whose legacy-audio fallback chain
// (.ogg/.wav/.mp3 after .opus) is user-tunable.
var audioTypeNames = []string{config.TypeSFX, config.TypeMusic, config.TypeBlip}

const (
	// learnedColName / learnedColLearned / learnedColOrder are the column x
	// offsets (relative to the form origin) of the read-only per-server learned
	// table: asset class, what this server actually taught us, and what would be
	// probed if it hadn't. Named because three labels must line up across ten
	// rows; a bare literal in each would drift the moment one is edited.
	learnedColName    = 8
	learnedColLearned = 150
	learnedColOrder   = 250
	// learnedRowH is one learned-table row's height — tighter than a settings
	// row because the table is read-only text, not clickable widgets.
	learnedRowH = 20
	// learnedExtGapPx separates the extensions in the "learned here" cell when a
	// class has learned more than one. Small: the ladder must read as one cell,
	// not spill toward the neighbouring "otherwise asks for" column at x=250.
	learnedExtGapPx = 6
	// formatPresetGap separates the per-server "pin one format" preset buttons.
	formatPresetGap = 6
)

// Every label this tab draws repeatedly is derived ONCE here rather than in the
// draw. The settings screen has no allocation gate (the benchmark suite covers
// the courtroom render loop, not this page), so a `name + ":"` inside a per-type
// loop would allocate on every frame the tab is open and nothing would fail —
// building the strings at package init makes that impossible instead of unlikely.
var (
	// formatPresetLabels / formatPresetTips are the display name and hover text
	// of each config.OptionalImageFormats entry (".webp" → "WebP"), parallel to
	// that slice.
	formatPresetLabels, formatPresetTips = buildFormatPresets()
	// typeRowLabels maps an asset-type name to its "<name>:" row label.
	typeRowLabels = buildTypeRowLabels()
	// audioFallbackLabels are the legacy-audio checkbox labels, parallel to
	// audioTypeNames.
	audioFallbackLabels = buildAudioFallbackLabels()
)

func buildFormatPresets() (labels, tips []string) {
	labels = make([]string, len(config.OptionalImageFormats))
	tips = make([]string, len(config.OptionalImageFormats))
	for i, ext := range config.OptionalImageFormats {
		name := strings.ToUpper(strings.TrimPrefix(ext, "."))
		if name == "WEBP" { // the only one whose conventional spelling isn't all-caps
			name = "WebP"
		}
		labels[i] = name
		tips[i] = "Ask this server for " + name + " art (character icons, sprites, emote buttons, backgrounds). Applies instantly, and only here."
	}
	return labels, tips
}

func buildTypeRowLabels() map[string]string {
	out := make(map[string]string, len(config.TypeNames))
	for _, name := range config.TypeNames {
		out[name] = name + ":"
	}
	return out
}

func buildAudioFallbackLabels() []string {
	out := make([]string, len(audioTypeNames))
	for i, name := range audioTypeNames {
		out[i] = name + ": also try .ogg / .wav / .mp3 after .opus"
	}
	return out
}

// pinnedProfileJSON builds a per-server format profile: an extensions.json
// document that declares ONE extension for every class the manifest system
// knows (icons, character art, emote buttons, backgrounds). Applying it seeds
// this host's learned table instantly — no network round-trip, so the very
// first probe after it is already right — and it overrides both the server's
// own manifest and the global default, for that host only.
//
// It is deliberately a manifest rather than a new preference: the per-server
// profile mechanism already exists, already persists, and already re-applies on
// every reconnect (fetchManifestAsync). A second mechanism would be a second
// place to look, which is the bug this tab is fixing.
func pinnedProfileJSON(ext string) string {
	return `{"charicon_extensions":["` + ext + `"],` +
		`"emote_extensions":["` + ext + `"],` +
		`"emotions_extensions":["` + ext + `"],` +
		`"background_extensions":["` + ext + `"]}`
}

// noteSearchRow registers a non-checkbox control with the settings gather-search
// (#26). Checkbox does this itself; buttons don't, and the one-click fixes on
// this tab are buttons — an unfindable fix is the problem, not the solution.
func (a *App) noteSearchRow(label string, y int32) {
	if a.ctx.onRow != nil {
		a.ctx.onRow(label, y)
	}
}

// formatsTabPointer draws the "the format controls live on their own tab now"
// jump button used by the Assets and Power user tabs. A pointer, never a copy:
// duplicating a control is how the two tabs ended up disagreeing about where
// formats belong. Returns the next y.
func (a *App) formatsTabPointer(y int32) int32 {
	c := a.ctx
	pad := a.formX
	const label = "Open the Formats tab"
	a.noteSearchRow(label, y)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 220, H: btnH}, label) {
		// Takes effect next frame: the body being drawn belongs to the old tab
		// and drawSettings already took that tab's scroll pointer.
		settings.tab = tabFormats
	}
	return y + btnH + 8
}

// formatOrderText returns each asset type's EFFECTIVE probe list as display
// text, indexed by assets.AssetType (config.TypeNames is declared in the enum's
// order, pinned by TestTypeNamesMatchConfig). The result is cached against the
// preferences' format generation, so a steady frame costs one atomic load and
// no allocation — see settingsState.fmtOrderText for why that matters here.
func formatOrderText(p *config.AssetPreferences) []string {
	gen := p.FormatGeneration()
	if settings.fmtOrderOK && settings.fmtOrderGen == gen {
		return settings.fmtOrderText
	}
	settings.fmtOrderText = settings.fmtOrderText[:0]
	for _, name := range config.TypeNames {
		settings.fmtOrderText = append(settings.fmtOrderText, strings.Join(p.FormatList(name), " "))
	}
	settings.fmtOrderGen, settings.fmtOrderOK = gen, true
	return settings.fmtOrderText
}

// drawSettingsFormats renders the Formats tab.
func (a *App) drawSettingsFormats(y, _ int32) int32 {
	pad := a.formX
	w := a.formW2()

	y = a.drawFormatsExplainer(y, w)
	y = a.drawFormatsThisServer(y, w)
	y = a.drawFormatsAutoDetect(y, w)
	y = a.drawFormatsPerType(y, w)
	y = a.drawFormatsFallbacks(y, w)
	y = a.drawFormatsLearnedTools(y, w)

	// Casing is the OTHER reason a whole server fetches nothing, and it is not a
	// format control, so it gets a signpost instead of a second home here.
	y = a.settingsDesc(pad, y, "Still nothing loading for a server, in any format? The other cause is the character-folder capitalisation in asset URLs — that control is on the Power user tab under \"Character folder casing\".", ColTextDim)
	y += 10
	return y
}

// drawFormatsExplainer states the streaming model in plain language: one probe
// per asset, why, what it costs, and what the escape hatch costs. Requirement:
// a player should understand this without reading the source.
func (a *App) drawFormatsExplainer(y, w int32) int32 {
	pad := a.formX
	y = a.settingsSection(y, w, "How formats work")
	y = a.settingsDesc(pad, y, "AO servers don't all ship art in the same file type: one serves .webp sprites, the next .png, an older one .gif or .apng. AsyncAO asks for exactly ONE of them per image — one request, no guessing — because that single request per asset is what makes a room load fast instead of trickling in.", ColTextDim)
	y += 6
	y = a.settingsDesc(pad, y, "Most of the time you never think about it. A server that publishes a format list (extensions.json) is read automatically on connect and every request is right first time; and once any asset does load, the format it used is remembered for that server.", ColTextDim)
	y += 6
	y = a.settingsDesc(pad, y, "A server that publishes NO list is the case that can bite: AsyncAO asks for its preferred type, and if that server doesn't have it, the image is simply missing — emote buttons and sprites are the usual casualties. That is not a bug to wait out, it's one switch. Set the format for that server below and it's fixed for that server, permanently, without slowing down every other server you play on.", ColTextDim)
	y += 6
	y = a.settingsDesc(pad, y, "The trade, stated honestly: one request per asset is the fastest possible cold load but it has no second chance, so a wrong guess shows nothing. Letting AsyncAO try more types (\"fallbacks\", further down) always finds the art but spends an extra request per miss on every asset it applies to — which is why it is off by default and why the per-server fix above it is the better trade.", ColTextDim)
	y += 12
	return y
}

// drawFormatsThisServer is the scoped fix: what is deciding the CURRENT
// server's formats, what it has actually learned there, and the per-server
// buttons that change it. Formats are learned per host, so art failing to load
// is almost always one server's problem — solving it with a global flip would
// slow down every other server to fix this one.
func (a *App) drawFormatsThisServer(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "This server")

	host := hostOfURL(a.urls.Origin())
	if host == "" {
		y = a.settingsDesc(pad, y, "Not connected to a server yet. Join one and this section shows what it serves, what AsyncAO learned there, and the one-click fixes for it.", ColTextDim)
		y += 10
		return y
	}

	c.Label(pad, y, "Asset server:", ColTextDim)
	c.LabelClipped(pad+learnedColLearned, y, a.formW-learnedColLearned, host, ColText)
	y += 22

	// What is deciding this host's formats, most specific wins first: a profile
	// pinned here, the built-in official-vanilla example, then the server's own
	// format list, then nothing (the per-type defaults, learned as art loads).
	// The "not checked yet" case is stated as such rather than folded into "no
	// list" — claiming a server publishes nothing when we simply haven't asked
	// would send a player straight to a fix they don't need.
	hasCustom := a.d.Prefs.ExtProfile(host) != ""
	st, known := a.manifestStatus[host]
	source, col := "", ColTextDim
	switch {
	case hasCustom:
		source, col = "You have pinned this server's formats. That wins over everything else, on this server only.", ColAccent
	case a.extProfileFor(host) != "":
		source, col = "Using the built-in official-vanilla format list for this server (it serves classic PNG art).", ColAccent
	case !a.d.Prefs.FormatAutoDetect():
		source = "Automatic detection is off, so this server's own format list is not being read — the per-type formats below decide everything."
	case known && st == seedApplied:
		source, col = "This server publishes a format list and AsyncAO read it — nothing here needs touching.", ColAccent
	case known && st == seedAbsent:
		source = "This server publishes NO format list, so AsyncAO uses the per-type formats below. If art is missing here, pin the type this server actually serves."
	case known && st == seedFailed:
		source, col = "This server's format list could not be read — formats are being learned per request instead.", ColDanger
	default:
		source = "AsyncAO hasn't read a format list for this server yet; formats are being learned as art loads."
	}
	y = a.settingsDesc(pad, y, source, col)
	y += 8

	y = a.drawLearnedTable(y, host)

	// One-click, scoped to this server: pin the type it serves.
	y = a.settingsDesc(pad, y, "Art missing on this server? Tell AsyncAO which file type it serves. This applies instantly, to THIS server only, and sticks across reconnects — every other server keeps its own settings and its speed.", ColText)
	y += 4
	const pinLabel = "Pin this server's image format"
	a.noteSearchRow(pinLabel, y)
	c.Label(pad, y+4, pinLabel+":", ColText)
	y += 24
	bx, by := pad, y
	for i, ext := range config.OptionalImageFormats {
		label := formatPresetLabels[i]
		bw := c.TextWidth(label) + labelBtnPadX*2
		if bx > pad && bx+bw > pad+a.formW {
			bx, by = pad, by+btnH+formatPresetGap
		}
		r := sdl.Rect{X: bx, Y: by, W: bw, H: btnH}
		if c.Button(r, label) {
			a.applyServerFormatPin(host, ext, label)
		}
		c.Tooltip(r, formatPresetTips[i])
		bx += bw + formatPresetGap
	}
	y = by + btnH + 8

	// The ready-made example + the undo. Both existed before under Assets →
	// "Server format profile"; they belong beside the pins.
	const vanillaLabel = "Use the official-vanilla format list"
	a.noteSearchRow(vanillaLabel, y)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 300, H: btnH}, vanillaLabel) {
		a.d.Prefs.SetExtProfile(host, assets.BundledVanillaManifestJSON)
		a.manifestFor = ""     // allow a re-seed of the connected origin
		a.fetchManifestAsync() // apply instantly
		settings.statusLine = "Applied the official-vanilla format list to " + host + "."
	}
	if hasCustom {
		if c.Button(sdl.Rect{X: pad + 310, Y: y, W: 200, H: btnH}, "Unpin (back to automatic)") {
			a.d.Prefs.SetExtProfile(host, "")
			a.manifestFor = ""
			a.fetchManifestAsync()
			settings.statusLine = "Unpinned " + host + " — back to this server's own format list."
		}
	}
	y += btnH + 6

	const reprobeLabel = "Forget what AsyncAO learned here"
	a.noteSearchRow(reprobeLabel, y)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 300, H: btnH}, reprobeLabel) {
		n := a.d.Prefs.ClearLearnedHost(host)
		// Drop the in-memory table's entries for this host too, one class at a
		// time: the resolver has no per-host wipe, and InvalidateAll would throw
		// away every OTHER server's warm state to fix this one.
		for t := assets.AssetType(0); t < assets.AssetTypeCount; t++ {
			a.d.Resolver.Invalidate(host, t)
		}
		a.manifestFor = ""
		a.fetchManifestAsync()
		settings.statusLine = fmt.Sprintf("Forgot %d remembered format(s) for %s — they re-derive on the next request.", n, host)
	}
	y += btnH + 4
	y = a.settingsDesc(pad, y, "Use this if the server repacked its art: it drops only THIS server's remembered formats (every other server keeps its warm state) and re-reads its format list.", ColTextDim)
	y += 12
	return y
}

// applyServerFormatPin pins one image extension for host via the per-server
// profile and re-seeds immediately, so the change is live on the next request.
func (a *App) applyServerFormatPin(host, ext, label string) {
	a.d.Prefs.SetExtProfile(host, pinnedProfileJSON(ext))
	// A pin is an explicit override of whatever was learned: clear this host's
	// stale entries so a previously-learned (wrong) type can't answer first.
	a.d.Prefs.ClearLearnedHost(host)
	for t := assets.AssetType(0); t < assets.AssetTypeCount; t++ {
		a.d.Resolver.Invalidate(host, t)
	}
	a.manifestFor = ""     // allow a re-seed of the connected origin
	a.fetchManifestAsync() // seeds from the pinned profile, no network round-trip
	settings.statusLine = "Pinned " + label + " for " + host + " — art re-requests in that format."
}

// drawLearnedTable renders the read-only view of what host has actually taught
// AsyncAO, per asset class, beside what would be probed without it. This is the
// fastest way to see WHY an asset is missing: a class showing a type the server
// doesn't serve is the whole diagnosis.
//
// A class can have learned MORE than one type — a server whose extensions.json
// declares two formats for a class really does serve both, and the whole point
// of keeping the list is that the second is reachable when the first 404s. The
// preferred one (probed first) is drawn in the accent colour, the rest of the
// ladder dimmed behind it.
//
// Zero allocation per frame by construction: Resolver.LearnedList is one atomic
// load plus a map lookup and returns interned extension strings, drawn as
// separate labels rather than joined (joining would allocate every frame), and
// the probe-order column comes from the generation-cached formatOrderText.
func (a *App) drawLearnedTable(y int32, host string) int32 {
	c := a.ctx
	pad := a.formX
	c.Label(pad+learnedColName, y, "asset", ColTextDim)
	c.Label(pad+learnedColLearned, y, "learned here", ColTextDim)
	c.Label(pad+learnedColOrder, y, "otherwise asks for", ColTextDim)
	y += learnedRowH
	orders := formatOrderText(a.d.Prefs)
	for t := assets.AssetType(0); t < assets.AssetTypeCount; t++ {
		c.Label(pad+learnedColName, y, t.Name(), ColTextDim)
		if exts := a.d.Resolver.LearnedList(host, t); len(exts) > 0 {
			x := pad + learnedColLearned
			for i, ext := range exts {
				col := ColAccent // the preferred format — the one actually probed
				if i > 0 {
					col = ColTextDim // the recovery ladder behind it
				}
				x += c.Label(x, y, ext, col) + learnedExtGapPx
			}
		} else {
			c.Label(pad+learnedColLearned, y, "—", ColTextDim)
		}
		if int(t) < len(orders) {
			c.LabelClipped(pad+learnedColOrder, y, a.formW-learnedColOrder, orders[t], ColTextDim)
		}
		y += learnedRowH
	}
	y += 4
	y = a.settingsDesc(pad, y, "\"Learned here\" is the file type this server actually served for that kind of asset; \"—\" means nothing of that kind has loaded yet. A server can serve more than one — the highlighted type is the one asked for first, the dimmed ones are tried if it isn't there, so a server that mixes (some characters PNG, some WebP) works either way. A dash that never fills in — on emote buttons, say — is the sign that the type being asked for isn't the one the server has.", ColTextDim)
	y += 10
	return y
}

// drawFormatsAutoDetect owns the automatic path: the extensions.json probe and
// the one class (desks) that can be opted out of it.
func (a *App) drawFormatsAutoDetect(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Automatic detection")
	auto := a.d.Prefs.FormatAutoDetect()
	if next := c.Checkbox(pad, y, "Read the server's format list (extensions.json) when connecting (recommended, on by default)", auto); next != auto {
		a.d.Prefs.SetFormatAutoDetect(next)
		if next {
			a.manifestFor = "" // re-check the current server right away
			a.fetchManifestAsync()
		}
	}
	y += 24
	y = a.settingsDesc(pad, y, "On: AsyncAO reads the list the server publishes and asks for exactly what it ships, so the single request per asset is right first time. Off: the server's list is ignored and the per-type formats below decide everything — turn it off only if you are forcing formats by hand.", ColTextDim)
	y += 8

	deskWebP := !a.d.Prefs.DeskFollowsManifest()
	if next := c.Checkbox(pad, y, "Always use WebP for desks, even when the server's list says otherwise", deskWebP); next != deskWebP {
		a.d.Prefs.SetDeskFollowManifest(!next)
		a.d.Prefs.ClearLearnedType(config.TypeDeskOverlay) // re-derive on next probe
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
		if !next { // now following the list: re-seed from the current server
			a.manifestFor = ""
			a.fetchManifestAsync()
		}
	}
	y += 24
	y = a.settingsDesc(pad, y, "Off (the default): desks follow the server's list like every other class. On: desks stay pinned to WebP — desks share a manifest class with backgrounds, so this covers servers whose list is right for backgrounds but wrong for desks.", ColTextDim)
	y += 12
	return y
}

// drawFormatsPerType is the manual layer: the image probe order per asset class
// (or a read-only view of it while automatic detection is on) and the legacy
// audio chains.
func (a *App) drawFormatsPerType(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Formats per asset type")
	auto := a.d.Prefs.FormatAutoDetect()
	if auto {
		y = a.settingsDesc(pad, y, "These are your fallback settings for servers that publish no format list — while automatic detection is on, a server that publishes one overrides them. Defaults: character icons PNG, chatbox skins PNG then WebP, everything else WebP.", ColTextDim)
		y += 4
		orders := formatOrderText(a.d.Prefs)
		for _, typeName := range imageTypeNames {
			if typeName == config.TypeMisc {
				// Chatbox-skin art is never declared by extensions.json and mixes
				// formats pack-by-pack on real servers, so the misc probes stay
				// adjustable even with automatic detection on.
				y = a.drawTypeFormatRow(typeName, y)
				continue
			}
			c.Label(pad, y+2, typeRowLabels[typeName], ColTextDim)
			if t, ok := assets.TypeFromName(typeName); ok && int(t) < len(orders) {
				c.LabelClipped(pad+110, y+2, a.formW-110, orders[t], ColTextDim)
			}
			y += 26
		}
		y += 4
		y = a.settingsDesc(pad, y, "Chatbox skins (Misc) stay editable above because no server declares them — real servers mix .png and .webp there pack by pack. To edit the rest by hand, turn off automatic detection.", ColTextDim)
		y += 10
	} else {
		y = a.settingsDesc(pad, y, "Automatic detection is OFF, so these decide every request. Tick only the types your servers actually serve — each extra one is an extra request per asset that misses. With two or more ticked, click a chip in the \"order:\" row to move it earlier.", ColTextDim)
		y += 4
		for _, typeName := range imageTypeNames {
			y = a.drawTypeFormatRow(typeName, y)
		}
		y += 10
	}

	y = a.settingsDesc(pad, y, "Sound follows the same rule: AsyncAO asks for .opus first because it is what modern packs ship. Tick a class here to also try the older types when .opus isn't there.", ColTextDim)
	y += 4
	for i, typeName := range audioTypeNames {
		enabled := a.d.Prefs.TypeFallbacksEnabled(typeName)
		if next := c.Checkbox(pad, y, audioFallbackLabels[i], enabled); next != enabled {
			a.d.Prefs.SetTypeFallbacks(typeName, next)
			a.d.Resolver.InvalidateAll()
			a.d.Resolver.WarmFromPrefs()
		}
		y += 24
	}
	y += 12
	return y
}

// drawTypeFormatRow renders the per-type format checkboxes; ticking builds a
// new format order: the type's default first, then enabled extras in the
// OptionalImageFormats order.
func (a *App) drawTypeFormatRow(typeName string, y int32) int32 {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y+2, typeRowLabels[typeName], ColText)
	x := pad + 110

	current := a.d.Prefs.FormatOrder(typeName)
	enabled := map[string]bool{}
	for _, ext := range current {
		enabled[ext] = true
	}

	changed := false
	for _, ext := range config.OptionalImageFormats {
		on := enabled[ext]
		next := c.Checkbox(x, y, ext, on)
		if next != on {
			enabled[ext] = next
			changed = true
		}
		x += c.TextWidth(ext) + 46
	}
	if changed {
		def := config.DefaultFormatOrder(typeName)
		order := make([]string, 0, len(config.OptionalImageFormats))
		for _, ext := range def {
			if enabled[ext] {
				order = append(order, ext)
			}
		}
		for _, ext := range config.OptionalImageFormats {
			if enabled[ext] && !containsExt(order, ext) {
				order = append(order, ext)
			}
		}
		if len(order) == 0 {
			order = def // never allow zero probes
		}
		a.d.Prefs.SetFormatOrder(typeName, order)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}

	// Probe-order chips: with 2+ formats ticked, clicking a chip promotes
	// it one slot toward "probed first" (zero-fallback order is the user's
	// to arrange — ticking chooses the set, chips choose the order).
	if len(current) > 1 && !changed {
		c.Label(x+12, y+2, "order:", ColTextDim)
		cx := x + 12 + c.TextWidth("order:") + 8
		for i, ext := range current {
			bw := c.TextWidth(ext) + 14
			if c.Button(sdl.Rect{X: cx, Y: y, W: bw, H: 22}, ext) && i > 0 {
				order := append([]string(nil), current...)
				order[i-1], order[i] = order[i], order[i-1]
				a.d.Prefs.SetFormatOrder(typeName, order)
				a.d.Resolver.InvalidateAll()
				a.d.Resolver.WarmFromPrefs()
			}
			cx += bw + 6
		}
	}
	return y + 26
}

func containsExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

// drawFormatsFallbacks is the global last resort, presented as one: it is the
// only control here that changes behaviour on EVERY server, and the per-server
// pin above solves the common case without that cost.
func (a *App) drawFormatsFallbacks(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Fallbacks (last resort)")
	global := a.d.Prefs.GlobalFallbacks()
	if next := c.Checkbox(pad, y, "If the preferred type isn't there, try the older ones too (off by default, affects EVERY server)", global); next != global {
		a.d.Prefs.SetGlobalFallbacks(next)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}
	y += 24
	y = a.settingsDesc(pad, y, "On: a miss is retried down the chain (WebP → APNG → GIF → PNG), so art is found almost whatever a server ships — at the cost of extra requests for every asset that misses, on every server, which is felt most on a cold room load. Off (default): one request per asset, and a miss stays missing.", ColTextDim)
	y += 6
	y = a.settingsDesc(pad, y, "Reach for this only when a server's art is inconsistent asset by asset. If it's one server that simply uses a different type, pin that server's format under \"This server\" above — same result, none of the cost.", ColTextDim)
	y += 12
	return y
}

// drawFormatsLearnedTools owns the learned-format state: the global resets and
// the portable export/import. They used to sit among the disk-cache buttons,
// which is a different kind of state entirely.
func (a *App) drawFormatsLearnedTools(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Learned formats")
	y = a.settingsDesc(pad, y, "Every server AsyncAO has loaded art from is remembered, so the next visit asks correctly from the first request. These act on ALL servers at once — to fix a single one, use \"This server\" above.", ColTextDim)
	y += 6

	// Behaviour is unchanged from its previous home on Assets → Cache (#36, Dag):
	// a server-side repack or a stale CDN needs the learned format AND the cached
	// file dropped together — either one alone leaves the other stuck, so the
	// wrongly-probed format can never re-derive. The learned-format RACE that once
	// produced the same symptom on its own is fixed at the root (manager.go tryBase
	// no longer blanks the shared per-host learned slot during a stale re-probe);
	// this button remains for the repack/CDN case no code path can pre-empt.
	const fixLabel = "Fix stuck / repeated images (all servers)"
	a.noteSearchRow(fixLabel, y)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 320, H: btnH}, fixLabel) {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Cleared learned formats + disk cache — images re-derive on next use."
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Cleared learned formats; disk clear failed: " + err.Error()
		}
	}
	y += btnH + 4
	y = a.settingsDesc(pad, y, "Use if art has gone wrong everywhere — for example every emote button showing the same picture. It drops the remembered formats and the cached files together, so both re-derive from scratch.", ColTextDim)
	y += 8

	const clearLabel = "Clear learned formats"
	a.noteSearchRow(clearLabel, y)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 190, H: btnH}, clearLabel) {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Learned formats cleared."
	}
	// Learned-format portability: one player's warm state seeds another's.
	const exportLabel, importLabel = "Export learned formats", "Import learned formats"
	a.noteSearchRow(exportLabel, y)
	a.noteSearchRow(importLabel, y)
	if c.Button(sdl.Rect{X: pad + 200, Y: y, W: 190, H: btnH}, exportLabel) {
		exportLearnedAsync(a)
	}
	if c.Button(sdl.Rect{X: pad + 400, Y: y, W: 190, H: btnH}, importLabel) {
		importLearnedAsync(a)
	}
	y += btnH + 4
	y = a.settingsDesc(pad, y, "Export writes the whole table beside the program so it can be shared or carried to another machine; import merges one in, which warms every server it covers before you ever visit them.", ColTextDim)
	y += 12
	return y
}
