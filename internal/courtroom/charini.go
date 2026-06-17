package courtroom

import (
	"bytes"
	"fmt"
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
	Emotes   []Emote
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

	countRaw, _ := ini.GetSection(charINIEmotionsSection, "number")
	count := atoiOr(countRaw, 0)
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
