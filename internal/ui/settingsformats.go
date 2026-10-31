package ui

import (
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Settings > Formats: every control that decides which file type is requested from a server.

// imageTypeNames are the asset classes with a user-tunable image format, in tab order.
var imageTypeNames = []string{
	config.TypeCharIcon,
	config.TypeCharSprite,
	config.TypeBackground,
	config.TypeDeskOverlay,
	config.TypeShoutBubble,
	config.TypeEmoteButton,
	config.TypeMisc,
}

// audioTypeNames are the asset classes with a user-tunable legacy-audio fallback chain.
var audioTypeNames = []string{config.TypeSFX, config.TypeMusic, config.TypeBlip}

const (
	// learnedCol* are x offsets from the form origin for the learned table's columns.
	learnedColName    = 8
	learnedColLearned = 150
	learnedColOrder   = 250
	// learnedRowH is one learned-table row's height.
	learnedRowH = 20
	// learnedExtGapPx separates extensions when a class has learned more than one.
	learnedExtGapPx = 6
	// formatPresetGap separates the per-server "pin one format" preset buttons.
	formatPresetGap = 6
)

// Row labels are built once at init so the per-frame draw does not allocate.
var (
	// formatPresetLabels / formatPresetTips are display names and hover text, parallel to config.OptionalImageFormats.
	formatPresetLabels, formatPresetTips = buildFormatPresets()
	// typeRowLabels maps an asset-type name to its "<name>:" row label.
	typeRowLabels = buildTypeRowLabels()
	// audioFallbackLabels are the legacy-audio checkbox labels, parallel to audioTypeNames.
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

// pinnedProfileJSON builds an extensions.json declaring one extension for every manifest class.
// It is a manifest rather than a preference because per-server profiles already persist and re-apply on reconnect.
func pinnedProfileJSON(ext string) string {
	return `{"charicon_extensions":["` + ext + `"],` +
		`"emote_extensions":["` + ext + `"],` +
		`"emotions_extensions":["` + ext + `"],` +
		`"background_extensions":["` + ext + `"]}`
}

// noteSearchRow registers a button with the settings search; Checkbox does this itself.
func (a *App) noteSearchRow(label string, y int32) {
	if a.ctx.onRow != nil {
		a.ctx.onRow(label, y)
	}
}

// formatsTabPointer draws the jump button to the Formats tab. Returns the next y.
func (a *App) formatsTabPointer(y int32) int32 {
	c := a.ctx
	pad := a.formX
	const label = "Open the Formats tab"
	a.noteSearchRow(label, y)
	if c.Button(sdl.Rect{X: pad, Y: y, W: 220, H: btnH}, label) {
		// Takes effect next frame: drawSettings already took the old tab's scroll pointer.
		settings.tab = tabFormats
	}
	return y + btnH + 8
}

// formatOrderText returns each asset type's effective probe list, indexed by assets.AssetType.
// Cached against the format generation so a steady frame allocates nothing.
func formatOrderText(p *config.AssetPreferences) []string {
	gen := p.FormatGeneration()
	if settings.fmtOrderOK && settings.fmtOrderGen == gen {
		return settings.fmtOrderText
	}
	settings.fmtOrderText = settings.fmtOrderText[:0]
	for _, name := range config.TypeNames {
		settings.fmtOrderText = append(settings.fmtOrderText, strings.Join(p.Load().FormatList(name), " "))
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

	// Casing is the other cause of a server fetching nothing; its control lives on the Power user tab.
	y = a.settingsDesc(pad, y, "Still nothing loading for a server, in any format? The other cause is the character-folder capitalisation in asset URLs — that control is on the Power user tab under \"Character folder casing\".", ColTextDim)
	y += 10
	return y
}

// drawFormatsExplainer explains the one-probe-per-asset model.
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

// drawFormatsThisServer draws what decides the current server's formats and the per-server buttons.
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

	// Most specific source first: pinned profile, built-in vanilla list, server list, then defaults.
	// "Not checked yet" stays apart from "no list" so players aren't sent to a fix they don't need.
	hasCustom := a.d.Prefs.Load().ExtProfile(host) != ""
	st, known := a.manifestStatus[host]
	source, col := "", ColTextDim
	switch {
	case hasCustom:
		source, col = "You have pinned this server's formats. That wins over everything else, on this server only.", ColAccent
	case a.extProfileFor(host) != "":
		source, col = "Using the built-in official-vanilla format list for this server (it serves classic PNG art).", ColAccent
	case !a.d.Prefs.Load().AutoDetectFormats:
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
		// The resolver has no per-host wipe; InvalidateAll would drop every other server's state.
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
	// A pin overrides what was learned; clear it so a stale type can't answer first.
	a.d.Prefs.ClearLearnedHost(host)
	for t := assets.AssetType(0); t < assets.AssetTypeCount; t++ {
		a.d.Resolver.Invalidate(host, t)
	}
	a.manifestFor = ""     // allow a re-seed of the connected origin
	a.fetchManifestAsync() // seeds from the pinned profile, no network round-trip
	settings.statusLine = "Pinned " + label + " for " + host + " — art re-requests in that format."
}

// drawLearnedTable shows what host has taught AsyncAO per asset class beside the default probe order.
// Allocation-free: LearnedList returns interned strings, drawn as separate labels.
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

// drawFormatsAutoDetect draws the extensions.json toggle and the desk opt-out.
func (a *App) drawFormatsAutoDetect(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Automatic detection")
	auto := a.d.Prefs.Load().AutoDetectFormats
	if next := c.Checkbox(pad, y, "Read the server's format list (extensions.json) when connecting (recommended, on by default)", auto); next != auto {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AutoDetectFormats =

				next

		})

		if next {
			a.manifestFor = "" // re-check the current server right away
			a.fetchManifestAsync()
		}
	}
	y += 24
	y = a.settingsDesc(pad, y, "On: AsyncAO reads the list the server publishes and asks for exactly what it ships, so the single request per asset is right first time. Off: the server's list is ignored and the per-type formats below decide everything — turn it off only if you are forcing formats by hand.", ColTextDim)
	y += 8

	deskWebP := !a.d.Prefs.Load().DeskFollowManifest
	if next := c.Checkbox(pad, y, "Always use WebP for desks, even when the server's list says otherwise", deskWebP); next != deskWebP {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DeskFollowManifest =

				!next

		})

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

// drawFormatsPerType draws the image probe order per asset class and the legacy audio chains.
func (a *App) drawFormatsPerType(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Formats per asset type")
	auto := a.d.Prefs.Load().AutoDetectFormats
	if auto {
		y = a.settingsDesc(pad, y, "These are your fallback settings for servers that publish no format list — while automatic detection is on, a server that publishes one overrides them. Defaults: character icons PNG, chatbox skins PNG then WebP, everything else WebP.", ColTextDim)
		y += 4
		orders := formatOrderText(a.d.Prefs)
		for _, typeName := range imageTypeNames {
			if typeName == config.TypeMisc {
				// Misc (chatbox skins) is never in extensions.json and mixes formats per pack, so it stays editable.
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
		enabled := a.d.Prefs.Load().TypeFallbacksEnabled(typeName)
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

// drawTypeFormatRow draws the per-type format checkboxes; the order is the type's default, then extras.
func (a *App) drawTypeFormatRow(typeName string, y int32) int32 {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y+2, typeRowLabels[typeName], ColText)
	x := pad + 110

	current := a.d.Prefs.Load().FormatOrder(typeName)
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

	// Probe-order chips: with 2+ formats ticked, a click moves that chip one place earlier.
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

// drawFormatsFallbacks draws the global fallback toggle, the only control here that affects every server.
func (a *App) drawFormatsFallbacks(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Fallbacks (last resort)")
	global := a.d.Prefs.Load().GlobalFallbacksEnabled
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

// drawFormatsLearnedTools draws the global learned-format resets and the export/import.
func (a *App) drawFormatsLearnedTools(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Learned formats")
	y = a.settingsDesc(pad, y, "Every server AsyncAO has loaded art from is remembered, so the next visit asks correctly from the first request. These act on ALL servers at once — to fix a single one, use \"This server\" above.", ColTextDim)
	y += 6

	// A server repack or stale CDN needs the learned format and the cached file dropped together;
	// either alone leaves the other stuck.
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
	const exportLabel, importLabel = "Export learned formats", "Import learned formats"
	a.noteSearchRow(exportLabel, y)
	a.noteSearchRow(importLabel, y)
	if c.Button(sdl.Rect{X: pad + 200, Y: y, W: 190, H: btnH}, exportLabel) {
		exportLearnedAsync(a.d)
	}
	if c.Button(sdl.Rect{X: pad + 400, Y: y, W: 190, H: btnH}, importLabel) {
		importLearnedAsync(a.d)
	}
	y += btnH + 4
	y = a.settingsDesc(pad, y, "Export writes the whole table beside the program so it can be shared or carried to another machine; import merges one in, which warms every server it covers before you ever visit them.", ColTextDim)
	y += 12
	return y
}
