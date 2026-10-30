package courtroom

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Emote is one [Emotions] entry, "<n> = comment#preanim#anim#emote_mod[#desk_mod]", plus its numbered audio sections.
type Emote struct {
	Comment string
	Preanim string
	Anim    string
	Mod     int
	DeskMod int

	// Per-emote audio (read_char_ini): [SoundN] sfx ("" or "1" = silent), [SoundT] delay, [SoundL] loop, [Blips] blip.
	SFXName  string
	SFXDelay int
	SFXLoop  bool
	Blip     string

	// FRAME_* wire fields in AO2's format (courtroom.cpp:2266-2287); "" without frame sections, which KFOCompat fills.
	FrameShake   string
	FrameRealize string
	FrameSFX     string
}

// CustomShout is a 2.10 interjection; we can't list custom_objections/, so [Shouts] "<stem>_name" keys are the source.
type CustomShout struct {
	Name string
	File string
}

// CharINI is the part of char.ini AsyncAO needs.
type CharINI struct {
	Showname string
	// NeedsShowname stays raw: canon tests startsWith "false" (text_file_functions.cpp:465-468), giving a blank plate.
	NeedsShowname string
	Side          string
	Blips         string
	// Chat is the [Options] chat= misc folder holding misc/<Chat>/chatbox (get_chat); empty means the normal chatbox.
	Chat string
	// Effects is the [Options] effects= misc folder, effects.ini and art in its root
	// (text_file_functions.cpp:772, :836-839, :855-858); empty means theme effects only.
	Effects string
	// Realization is the [Options] realization= sound; it overrides effects.ini (text_file_functions.cpp:889-892,
	// :896-903), and legacy REALIZATION=1 plays it (courtroom.cpp:4175).
	Realization string
	// Scaling is the raw [Options] scaling= request, resolved by ScalingMode.
	Scaling string
	Emotes  []Emote
	// CustomName renames the base "custom" shout ([Shouts] custom_name).
	CustomName string
	// CustomShouts are the named 2.10 interjections (at most customShoutCap).
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

	// Frame-effect section suffixes (courtroom.cpp:2266-2280); INI sections match case-insensitively, so lowercase.
	frameScreenshakeSuffix = "_framescreenshake"
	frameRealizationSuffix = "_framerealization"
	frameSFXSuffix         = "_framesfx"
	// frameEffectFramesCap bounds one emote's FRAME_* pairs (hard rule 4), so a local char.ini can't grow the packet.
	frameEffectFramesCap = 256
	// charEmoteScanCap bounds the "number=" scan (hard rule 4): a hostile count would loop billions of times.
	// Raise it if needed; never remove the clamp.
	charEmoteScanCap = 8192
	// customShoutCap bounds the [Shouts] scan (hard rule 4).
	customShoutCap = 32
	// customNameSuffix marks "<stem>_name" keys in [Shouts].
	customNameSuffix = "_name"
)

// ParseCharINI parses a char.ini payload with read_char_ini semantics, tolerating missing fields.
func ParseCharINI(data []byte) (*CharINI, error) {
	ini, err := theme.ParseINI(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("courtroom: parsing char.ini: %w", err)
	}
	out := &CharINI{}
	out.Showname, _ = ini.GetSection(charINIOptionsSection, "showname")
	out.NeedsShowname, _ = ini.GetSection(charINIOptionsSection, "needs_showname")
	out.Side, _ = ini.GetSection(charINIOptionsSection, "side")
	out.Blips, _ = ini.GetSection(charINIOptionsSection, "blips")
	if out.Blips == "" {
		// Legacy key.
		out.Blips, _ = ini.GetSection(charINIOptionsSection, "gender")
	}
	out.Chat, _ = ini.GetSection(charINIOptionsSection, "chat")       // per-character chatbox skin (misc folder)
	out.Effects, _ = ini.GetSection(charINIOptionsSection, "effects") // per-character screen-effect pack (misc folder)
	out.Scaling, _ = ini.GetSection(charINIOptionsSection, "scaling") // per-character texture filter (get_scaling)
	// The character's own realization sound (courtroom.cpp:4175); see CharINI.Realization.
	out.Realization, _ = ini.GetSection(charINIOptionsSection, "realization")

	countRaw, _ := ini.GetSection(charINIEmotionsSection, "number")
	count := parse.AtoiOr(countRaw, 0)
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
			Mod:     parse.AtoiOr(parts[3], 0),
			// AO2 shows the desk when the emote omits desk_mod (get_desk_mod returns -1), so a 0 default would hide it.
			DeskMod: protocol.DeskShow,
		}
		if len(parts) > emoteFieldCount {
			e.DeskMod = parse.AtoiOr(parts[4], protocol.DeskShow)
		}
		// Per-emote audio (get_sfx_name, get_sfx_delay, get_sfx_looping, get_blipname).
		e.SFXName, _ = ini.GetSection(charINISoundNSection, key)
		e.SFXDelay = parse.AtoiOr(firstSection(ini, charINISoundTSection, key), 0)
		e.SFXLoop = firstSection(ini, charINISoundLSection, key) == "1"
		e.Blip, _ = ini.GetSection(charINIBlipsSection, key)
		// Build the FRAME_* wire fields in AO2's send format (courtroom.cpp:2266-2287).
		e.FrameShake = buildFrameField(ini, e.Preanim, e.Anim, frameScreenshakeSuffix)
		e.FrameRealize = buildFrameField(ini, e.Preanim, e.Anim, frameRealizationSuffix)
		e.FrameSFX = buildFrameField(ini, e.Preanim, e.Anim, frameSFXSuffix)
		out.Emotes = append(out.Emotes, e)
	}

	// custom_name renames the base shout; every other "<stem>_name" names a custom_objections/<stem> interjection.
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

// ShownameOrFolder is get_showname (text_file_functions.cpp:446-473) with p_emote == -1, as display calls pass
// (courtroom.cpp:2532, :3286, :3868, :4798; only the send, :2219, reads [OptionsN]).
// needs_showname is tested first, so an opt-out is a real blank name; a nil receiver answers the folder.
func (c *CharINI) ShownameOrFolder(folder string) string {
	if c == nil {
		return folder
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.NeedsShowname)), "false") {
		return "" // :465-468 — a deliberate blank, kept blank
	}
	if s := strings.TrimSpace(c.Showname); s != "" {
		return s
	}
	return folder // :470-472
}

// IdleAnim is the first [Emotions] anim, AO2's preselected emote (courtroom.cpp:1487), as many packs have no "normal".
// Nil or no emotes returns "", and the caller keeps its own convention.
func (c *CharINI) IdleAnim() string {
	if c == nil || len(c.Emotes) == 0 {
		return ""
	}
	return strings.TrimSpace(c.Emotes[0].Anim)
}

func firstSection(ini *theme.INI, section, key string) string {
	v, _ := ini.GetSection(section, key)
	return v
}

// buildFrameField assembles one FRAME_* field as on_chat_return_pressed does (courtroom.cpp:2266-2287); "" if no tags.
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

// frameSectionTags renders numeric "<frame>=<value>" entries sorted, as QSettings::allKeys does;
// remaining bounds pairs across a field's three sections (hard rule 4).
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

// ParseChatboxDefaultColor returns c0 from a misc chatbox's chat_config.ini, the colour AO2 uses for uncoloured text.
func ParseChatboxDefaultColor(data []byte) (theme.RGB, bool) {
	ini, err := theme.ParseINI(bytes.NewReader(data))
	if err != nil {
		return theme.RGB{}, false
	}
	raw, ok := ini.GetSection("", "c0")
	if !ok {
		return theme.RGB{}, false
	}
	return parseRGBTuple(raw)
}

// ParseChatboxFontsIni returns courtroom_fonts.ini's showname_color and message_color; false keeps the theme's.
func ParseChatboxFontsIni(data []byte) (showname theme.RGB, shownameSet bool, message theme.RGB, messageSet bool) {
	ini, err := theme.ParseINI(bytes.NewReader(data))
	if err != nil {
		return theme.RGB{}, false, theme.RGB{}, false
	}
	if raw, ok := ini.GetSection("", "showname_color"); ok {
		if rgb, ok := parseRGBTuple(raw); ok {
			showname, shownameSet = rgb, true
		}
	}
	if raw, ok := ini.GetSection("", "message_color"); ok {
		if rgb, ok := parseRGBTuple(raw); ok {
			message, messageSet = rgb, true
		}
	}
	return
}

// parseRGBTuple parses AO2's "r, g, b" colour; ok=false on any malformed part.
func parseRGBTuple(raw string) (theme.RGB, bool) {
	const channels = 3
	parts := strings.Split(raw, ",")
	if len(parts) < channels {
		return theme.RGB{}, false
	}
	var rgb [channels]uint8
	for i := range channels {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return theme.RGB{}, false
		}
		rgb[i] = uint8(value.Clamp(n, 0, math.MaxUint8))
	}
	return theme.RGB{R: rgb[0], G: rgb[1], B: rgb[2]}, true
}
