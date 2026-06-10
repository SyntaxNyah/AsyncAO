package courtroom

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Emote is one [Emotions] entry from a character's char.ini:
// "<n> = comment#preanim#anim#emote_mod[#desk_mod]".
type Emote struct {
	Comment string
	Preanim string
	Anim    string
	Mod     int
	DeskMod int
}

// CharINI is the slice of char.ini AsyncAO needs: emote list, default side,
// blip set, showname.
type CharINI struct {
	Showname string
	Side     string
	Blips    string
	Emotes   []Emote
}

const (
	charINIOptionsSection  = "options"
	charINIEmotionsSection = "emotions"
	emoteFieldCount        = 4
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
		raw, ok := ini.GetSection(charINIEmotionsSection, fmt.Sprintf("%d", i))
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
		}
		if len(parts) > emoteFieldCount {
			e.DeskMod = atoiOr(parts[4], 0)
		}
		out.Emotes = append(out.Emotes, e)
	}
	return out, nil
}
