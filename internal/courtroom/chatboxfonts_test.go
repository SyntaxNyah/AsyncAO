package courtroom

import "testing"

// TestParseChatboxDefaultColor pins the chat_config.ini default-colour parse: c0 is
// the palette index 0 AO2 uses for uncoloured text (black on a white chatbox).
func TestParseChatboxDefaultColor(t *testing.T) {
	rgb, ok := ParseChatboxDefaultColor([]byte("c0 = 0, 0, 0\nc0_name = Black\n"))
	if !ok || rgb.R != 0 || rgb.G != 0 || rgb.B != 0 {
		t.Errorf("c0 = %+v ok=%v, want black", rgb, ok)
	}
	if _, ok := ParseChatboxDefaultColor([]byte("c1 = 0, 247, 0\n")); ok {
		t.Error("a chat_config.ini without c0 must not yield a default colour")
	}
	if _, ok := ParseChatboxDefaultColor([]byte("c0 = 0, nope, 0\n")); ok {
		t.Error("a malformed c0 must not count as set")
	}
}

// TestParseChatboxFontsIni pins the courtroom_fonts.ini parse: showname_color and
// message_color are root-level "r, g, b" tuples.
func TestParseChatboxFontsIni(t *testing.T) {
	showname, nameOK, _, msgOK := ParseChatboxFontsIni([]byte("showname_color = 230, 73, 115\n"))
	if !nameOK || showname.R != 230 || showname.G != 73 || showname.B != 115 {
		t.Errorf("showname = %+v ok=%v, want (230,73,115)", showname, nameOK)
	}
	if msgOK {
		t.Error("no message_color must leave messageSet false")
	}

	_, n2, m2, m2ok := ParseChatboxFontsIni([]byte("message_color = 1, 2, 3\n"))
	if n2 {
		t.Error("no showname_color must leave shownameSet false")
	}
	if !m2ok || m2.R != 1 || m2.G != 2 || m2.B != 3 {
		t.Errorf("message = %+v ok=%v, want (1,2,3)", m2, m2ok)
	}
}

// TestChatboxIniURLs pins the casing chains: lowercase identity first, then the
// authored folder case, and — for courtroom_fonts.ini — the authored FILE case too.
func TestChatboxIniURLs(t *testing.T) {
	u := NewURLBuilder("http://cdn.example.com/base/")
	got := u.ChatboxFontsIni("EndlessMonday/Hana")
	want := []string{
		"http://cdn.example.com/base/misc/endlessmonday/hana/courtroom_fonts.ini",
		"http://cdn.example.com/base/misc/endlessmonday/hana/Courtroom_Fonts.ini",
		"http://cdn.example.com/base/misc/EndlessMonday/Hana/courtroom_fonts.ini",
		"http://cdn.example.com/base/misc/EndlessMonday/Hana/Courtroom_Fonts.ini",
	}
	if len(got) != len(want) {
		t.Fatalf("ChatboxFontsIni = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ChatboxFontsIni[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	cc := u.ChatboxChatConfigIni("EndlessMonday/Hana")
	wantCC := []string{
		"http://cdn.example.com/base/misc/endlessmonday/hana/chat_config.ini",
		"http://cdn.example.com/base/misc/EndlessMonday/Hana/chat_config.ini",
	}
	if len(cc) != len(wantCC) {
		t.Fatalf("ChatboxChatConfigIni = %v, want %v", cc, wantCC)
	}
	for i := range wantCC {
		if cc[i] != wantCC[i] {
			t.Errorf("ChatboxChatConfigIni[%d] = %q, want %q", i, cc[i], wantCC[i])
		}
	}

	if u.ChatboxChatConfigIni("  ") != nil {
		t.Error("a blank folder must yield no URLs")
	}
}
