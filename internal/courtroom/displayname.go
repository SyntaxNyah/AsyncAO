package courtroom

// THE IC DISPLAY-NAME CHAIN — one concern, one file, one copy.
//
// AO2 resolves "what name goes next to this message" three times, in three
// widgets, and gets the same answer every time because all three call
// get_showname:
//
//	courtroom.cpp:3283-3292  initialize_chatbox  → the NAME PLATE
//	courtroom.cpp:2528-2538  log_chatmessage     → the IC LOG
//	courtroom.cpp:3868       append_ic_text      → the log's own render
//
// AsyncAO draws the same two surfaces (Scene.ShownameText and the icLog entry)
// from two functions in two packages, and they had drifted into agreeing only
// by accident: both implemented rungs 1 and 3 and neither implemented rung 2,
// so a character whose char.ini declares a showname wore its raw FOLDER name —
// including, on a nested pack, the folder PATH — in both places.
//
// This is the chain, exported so the log can drive the same one the plate does
// rather than keep a second opinion about it.

import "github.com/SyntaxNyah/AsyncAO/internal/protocol"

// IniShowname answers AOApplication::get_showname (text_file_functions.cpp:446)
// for a character folder: the char.ini [Options] showname, the folder name when
// none is declared, or "" for needs_showname=false.
//
// known reports whether the char.ini is RESOLVED. AO2 has no such flag because
// it reads the ini off disk synchronously; a streaming client cannot, so
// known=false means "not fetched yet / not wired" and the chain degrades to the
// folder name — which is also what canon answers for an unreadable ini, so the
// degradation is a canon state rather than an invented one.
//
// A known EMPTY string is a real answer (needs_showname=false) and must not be
// confused with the unresolved case: that is why this returns two values
// instead of leaning on emptiness.
type IniShowname func(char string) (showname string, known bool)

// DisplayName is AO2's precedence, in canon's order:
//
//  1. the sender's CUSTOM showname off the wire (MS field 15) — skipped when
//     forceChar, AsyncAO's inverse of AO2's custom_shownames option
//  2. the speaker's char.ini showname (get_showname)
//  3. the character FOLDER name
//
// forceChar suppresses rung 1 only. Canon is explicit about that: with
// custom_shownames off, initialize_chatbox still calls get_showname rather than
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
