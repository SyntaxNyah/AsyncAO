package courtroom

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Emote is one [Emotions] entry from a character's char.ini:
// "<n> = comment#preanim#anim#emote_mod[#desk_mod]", enriched with the
// per-emote audio sections keyed by the same 1-based number.
type Emote struct {
	Comment string
	Preanim string
	Anim    string
	Mod     int
	DeskMod int

	// Per-emote audio (AO2-Client read_char_ini):
	// [SoundN] sfx name ("" or "1" = silent), [SoundT] delay ticks,
	// [SoundL] looping flag (2.8), [Blips] per-emote blip override (2.9.1).
	SFXName  string
	SFXDelay int
	SFXLoop  bool
	Blip     string

	// Networked frame-synced effects (#17): the three FRAME_* wire fields for
	// THIS emote, pre-assembled at parse in AO2's exact format
	// "<pre>[|<f>=<v>…]^(b)<anim>[|…]^(a)<anim>[|…]^" (courtroom.cpp:2266-2287).
	// Empty when the char.ini authors no [<emote>_FrameScreenshake] /
	// [<emote>_FrameRealization] / [<emote>_FrameSFX] sections — the send path
	// then ships "" (unchanged wire) and KFOCompat still fills its template.
	FrameShake   string
	FrameRealize string
	FrameSFX     string
}

// CustomShout is one 2.10 custom interjection: Name is the menu label,
// File the art stem under characters/<char>/custom_objections/. A
// streaming client cannot list that directory, so the char.ini [Shouts]
// "<stem>_name" keys are the discoverable source (AO2-Client uses them as
// display-name overrides for the dir scan it can do locally).
type CustomShout struct {
	Name string
	File string
}

// CharINI is the slice of char.ini AsyncAO needs: emote list, default side,
// blip set, showname, custom shouts.
type CharINI struct {
	Showname string
	Side     string
	Blips    string
	// Chat is the [Options] chat= misc folder: the character's own chatbox
	// skin lives at misc/<Chat>/chatbox (AO2-Client get_chat). Empty = the
	// client's normal chatbox.
	Chat   string
	Emotes []Emote
	// CustomName renames the character's base "custom" shout ([Shouts]
	// custom_name); empty when the ini doesn't define it.
	CustomName string
	// CustomShouts are the named 2.10 interjections (≤ customShoutCap).
	CustomShouts []CustomShout
}

const (
	charINIOptionsSection  = "options"
	charINIEmotionsSection = "emotions"
	charINISoundNSection   = "soundn"
	charINISoundTSection   = "soundt"
	charINISoundLSection   = "soundl"
	charINIBlipsSection    = "blips"
	charINIShoutsSection   = "shouts"
	emoteFieldCount        = 4

	// Networked frame-effect section suffixes (#17): AO2-Client reads
	// [<emote>_FrameScreenshake] / [<emote>_FrameRealization] / [<emote>_FrameSFX]
	// where <emote> is the pre-emote name, "(b)"+anim, and "(a)"+anim
	// (courtroom.cpp:2266-2280). Section names are matched case-insensitively by
	// the INI reader, so the suffixes are lowercase here.
	frameScreenshakeSuffix = "_framescreenshake"
	frameRealizationSuffix = "_framerealization"
	frameSFXSuffix         = "_framesfx"
	// frameEffectFramesCap bounds how many "<frame>=<value>" pairs one emote's
	// FRAME_* section may contribute to the outgoing wire, so our OWN char.ini
	// with a pathological section can't build an unbounded packet (§17.4). Well
	// above any real preanim's frame count.
	frameEffectFramesCap = 256
	// charEmoteScanCap bounds the [Emotions] "number=" scan (rule §17.4). The
	// count is attacker-controlled server bytes — a hostile "number=2000000000"
	// otherwise loops ~2e9 times, each allocating an fmt.Sprintf key: a hang +
	// GC storm from one malformed char.ini (caught by FuzzParseCharINI). Chosen
	// well clear of any REAL char.ini: single characters carry at most a few
	// hundred emotes, and even a merged "ensemble"/megachar sheet stacking many
	// casts stays comfortably under this — 8192 is an order of magnitude past the
	// largest shipped set while still bounding the scan to a trivial cost (a
	// claimed count above it simply stops reading extra rows, which would have to
	// exist as [Emotions] keys anyway). If a genuine char legitimately needs more,
	// raise this named constant; do not remove the clamp.
	charEmoteScanCap = 8192
	// customShoutCap bounds the [Shouts] scan (rule §17.4).
	customShoutCap = 32
	// customNameSuffix marks "<stem>_name" keys in [Shouts].
	customNameSuffix = "_name"
)

// ParseCharINI parses a char.ini payload (AO2-Client read_char_ini
// semantics, tolerant of missing fields).
func ParseCharINI(data []byte) (*CharINI, error) {
	ini, err := theme.ParseINI(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("courtroom: parsing char.ini: %w", err)
	}
	out := &CharINI{}
	out.Showname, _ = ini.GetSection(charINIOptionsSection, "showname")
	out.Side, _ = ini.GetSection(charINIOptionsSection, "side")
	out.Blips, _ = ini.GetSection(charINIOptionsSection, "blips")
	if out.Blips == "" {
		// Legacy key.
		out.Blips, _ = ini.GetSection(charINIOptionsSection, "gender")
	}
	out.Chat, _ = ini.GetSection(charINIOptionsSection, "chat") // per-character chatbox skin (misc folder)

	countRaw, _ := ini.GetSection(charINIEmotionsSection, "number")
	count := atoiOr(countRaw, 0)
	if count > charEmoteScanCap {
		count = charEmoteScanCap // hostile/garbage "number=" can't drive an unbounded scan (§17.4)
	}
	for i := 1; i <= count; i++ {
		key := fmt.Sprintf("%d", i)
		raw, ok := ini.GetSection(charINIEmotionsSection, key)
		if !ok {
			continue
		}
		parts := strings.Split(raw, "#")
		if len(parts) < emoteFieldCount {
			continue
		}
		e := Emote{
			Comment: parts[0],
			Preanim: parts[1],
			Anim:    parts[2],
			Mod:     atoiOr(parts[3], 0),
			// AO2 shows the desk when the emote omits its optional 5th (desk_mod)
			// field (get_desk_mod → -1, a non-hide value); only an explicit 0/3/5
			// hides it. Defaulting to 0 here would wrongly hide the desk for the
			// majority of emotes, which have no desk_mod field.
			DeskMod: protocol.DeskShow,
		}
		if len(parts) > emoteFieldCount {
			e.DeskMod = atoiOr(parts[4], protocol.DeskShow)
		}
		// Per-emote audio rides sections keyed by the same number
		// (get_sfx_name / get_sfx_delay / get_sfx_looping / get_blipname).
		e.SFXName, _ = ini.GetSection(charINISoundNSection, key)
		e.SFXDelay = atoiOr(firstSection(ini, charINISoundTSection, key), 0)
		e.SFXLoop = firstSection(ini, charINISoundLSection, key) == "1"
		e.Blip, _ = ini.GetSection(charINIBlipsSection, key)
		// Networked frame effects (#17): assemble the three FRAME_* wire fields for
		// this emote from its [<emote>_Frame*] sections, matching AO2's send format
		// exactly (courtroom.cpp:2266-2287, emotes_to_check = {pre, "(b)"+anim,
		// "(a)"+anim}). Empty ("") when no such sections exist, so the outgoing wire
		// is unchanged for the (vast majority of) char.inis without frame data.
		e.FrameShake = buildFrameField(ini, e.Preanim, e.Anim, frameScreenshakeSuffix)
		e.FrameRealize = buildFrameField(ini, e.Preanim, e.Anim, frameRealizationSuffix)
		e.FrameSFX = buildFrameField(ini, e.Preanim, e.Anim, frameSFXSuffix)
		out.Emotes = append(out.Emotes, e)
	}

	// [Shouts]: custom_name renames the base custom shout; every other
	// "<stem>_name" key names a custom_objections/<stem> interjection.
	out.CustomName, _ = ini.GetSection(charINIShoutsSection, "custom_name")
	for key, val := range ini.SectionKeys(charINIShoutsSection) {
		if len(out.CustomShouts) >= customShoutCap {
			break
		}
		stem, found := strings.CutSuffix(key, customNameSuffix)
		if !found || stem == "" || stem == "custom" || val == "" {
			continue
		}
		out.CustomShouts = append(out.CustomShouts, CustomShout{Name: val, File: stem})
	}
	return out, nil
}

// firstSection is GetSection collapsed to its value.
func firstSection(ini *theme.INI, section, key string) string {
	v, _ := ini.GetSection(section, key)
	return v
}

// buildFrameField assembles one outgoing FRAME_* wire field (#17) for an emote,
// mirroring AO2-Client on_chat_return_pressed (courtroom.cpp:2266-2287): three
// "^"-terminated sections for the pre-emote, the "(b)" talk emote and the "(a)"
// idle emote, each "<emote>[|<frame>=<value>…]". The frame pairs come from the
// char.ini [<emote><suffix>] section (read_ini_tags → "<key>=<value>", joined by
// "|"). Returns "" when NONE of the three sections carry a tag, so a char.ini
// without frame data leaves the wire byte-identical to before (the send path then
// ships "" and KFOCompat still fills its template).
func buildFrameField(ini *theme.INI, pre, anim, suffix string) string {
	emotes := [...]string{pre, "(b)" + anim, "(a)" + anim}
	var b strings.Builder
	any := false
	remaining := frameEffectFramesCap
	for _, name := range emotes {
		b.WriteString(name)
		if tags := frameSectionTags(ini, name+suffix, &remaining); tags != "" {
			b.WriteByte('|')
			b.WriteString(tags)
			any = true
		}
		b.WriteByte('^')
	}
	if !any {
		return "" // no frame data for this emote — keep the current empty wire
	}
	return b.String()
}

// frameSectionTags renders a [<section>]'s "<frame>=<value>" entries as a
// "|"-joined, frame-number-sorted string (QSettings::allKeys returns keys sorted,
// so a receiver sees a stable order). *remaining bounds the total pairs across the
// three sections of one field (§17.4) so our own hostile char.ini can't build an
// unbounded packet. Only numeric keys are emitted (AO2's frame_data.at(0).toInt).
func frameSectionTags(ini *theme.INI, section string, remaining *int) string {
	keys := ini.SectionKeys(section)
	if len(keys) == 0 {
		return ""
	}
	type pair struct {
		frame int
		value string
	}
	pairs := make([]pair, 0, len(keys))
	for k, v := range keys {
		n, err := strconv.Atoi(strings.TrimSpace(k))
		if err != nil || n < 0 {
			continue // non-numeric key: not a frame trigger
		}
		pairs = append(pairs, pair{frame: n, value: v})
	}
	if len(pairs) == 0 {
		return ""
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].frame < pairs[j].frame })
	var b strings.Builder
	for i, p := range pairs {
		if *remaining <= 0 {
			break // cap reached: drop the rest (bounded packet)
		}
		if i > 0 && b.Len() > 0 {
			b.WriteByte('|')
		}
		b.WriteString(strconv.Itoa(p.frame))
		b.WriteByte('=')
		b.WriteString(p.value)
		*remaining--
	}
	return b.String()
}
