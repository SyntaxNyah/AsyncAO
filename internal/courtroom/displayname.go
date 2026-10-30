package courtroom

// AO2 resolves a display name through get_showname in three widgets; this is that one chain, for log and plate.

import "github.com/SyntaxNyah/AsyncAO/internal/protocol"

// IniShowname answers get_showname (text_file_functions.cpp:446); known with "" is a real answer, not unresolved.
type IniShowname func(char string) (showname string, known bool)

// DisplayName applies AO2's precedence (courtroom.cpp:3285-3290): forceChar ignores the sender's showname, not the ini.
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
