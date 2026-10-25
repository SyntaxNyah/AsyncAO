package courtroom

// THE IC DISPLAY-NAME CHAIN — one concern, one file, one copy.
// AO2 resolves "what name goes next to this message" three times, in three
// widgets, and gets the same answer every time because all three call
// get_showname:
// This is the chain, exported so the log can drive the same one the plate does
// rather than keep a second opinion about it.

import "github.com/SyntaxNyah/AsyncAO/internal/protocol"

// IniShowname answers AOApplication::get_showname (text_file_functions.cpp:446)
// for a character folder: the char.ini [Options] showname, the folder name when
// none is declared, or "" for needs_showname=false.
// A known EMPTY string is a real answer (needs_showname=false) and must not be
// confused with the unresolved case: that is why this returns two values
// instead of leaning on emptiness.
type IniShowname func(char string) (showname string, known bool)

// DisplayName is AO2's precedence, in canon's order:
// dropping to the folder (courtroom.cpp:3285-3290), so "show character names"
// means "ignore what the SENDER claims", not "ignore the character's own ini".
func DisplayName(msg *protocol.ChatMessage, forceChar bool, ini IniShowname) string {
	if msg == nil {
		return ""
	}
	if !forceChar && msg.Showname != "" {
		return msg.Showname
	}
	if ini != nil {
		if name, known := ini(msg.CharName); known {
			return name
		}
	}
	return msg.CharName
}
