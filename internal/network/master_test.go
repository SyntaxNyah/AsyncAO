package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// sampleMasterJSON is a real master-server response shape (trimmed): one
// wss+ws server, one ws-only server, and two legacy TCP-only servers.
const sampleMasterJSON = `[
  {
    "ip": "securevanilla.aceattorneyonline.com",
    "port": 27106,
    "ws_port": 2095,
    "wss_port": 2096,
    "players": 13,
    "name": "AO Official Server (Vanilla)",
    "description": "The official server of Attorney Online (16+).",
    "hbcounter": 49208
  },
  {
    "ip": "135.148.43.158",
    "port": 27016,
    "ws_port": 50000,
    "players": 0,
    "name": "Killing Fever Online",
    "description": "KFO for short."
  },
  {
    "ip": "195.133.73.173",
    "port": 50000,
    "players": 0,
    "name": "ENIGMA RP",
    "description": "Redacted is Expunged"
  },
  {
    "ip": "212.220.202.246",
    "port": 15000,
    "players": 7,
    "name": "Yu Meiren shadowhost 4!",
    "description": "We love casting spells."
  },
  {
    "ip": "aofoc.ru",
    "port": 27017,
    "ws_port": 50001,
    "players": 3,
    "name": "Fair of Contradictions[RU]",
    "description": "Добро пожаловать"
  }
]`

func TestParseServerList(t *testing.T) {
	entries, err := ParseServerList([]byte(sampleMasterJSON))
	if err != nil {
		t.Fatalf("ParseServerList: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(entries))
	}
	official := entries[0]
	if official.IP != "securevanilla.aceattorneyonline.com" || official.WSSPort != 2096 || official.WSPort != 2095 || official.Players != 13 {
		t.Errorf("official entry parsed wrong: %+v", official)
	}
}

func TestServerSecurityTiers(t *testing.T) {
	entries, _ := ParseServerList([]byte(sampleMasterJSON))

	cases := []struct {
		name     string
		want     ServerSecurity
		joinable bool
		wsURL    string
	}{
		{"AO Official Server (Vanilla)", SecurityWSS, true, "wss://securevanilla.aceattorneyonline.com:2096"},
		{"Killing Fever Online", SecurityWS, true, "ws://135.148.43.158:50000"},
		{"ENIGMA RP", SecurityLegacyTCP, false, ""},
		{"Yu Meiren shadowhost 4!", SecurityLegacyTCP, false, ""},
		{"Fair of Contradictions[RU]", SecurityWS, true, "ws://aofoc.ru:50001"},
	}
	byName := map[string]ServerEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Fatalf("server %q missing", tc.name)
		}
		if got := e.Security(); got != tc.want {
			t.Errorf("%s: Security = %v, want %v", tc.name, got, tc.want)
		}
		if got := e.Joinable(); got != tc.joinable {
			t.Errorf("%s: Joinable = %v, want %v", tc.name, got, tc.joinable)
		}
		if got := e.WebSocketURL(); got != tc.wsURL {
			t.Errorf("%s: WebSocketURL = %q, want %q", tc.name, got, tc.wsURL)
		}
	}
}

func TestSortServersPinsLegacyToBottom(t *testing.T) {
	entries, _ := ParseServerList([]byte(sampleMasterJSON))
	SortServers(entries)

	// Joinable sorted by players desc: Official (13) > FoC (3) > KFO (0).
	wantOrder := []string{
		"AO Official Server (Vanilla)",
		"Fair of Contradictions[RU]",
		"Killing Fever Online",
		// Legacy pinned last regardless of player count (Yu Meiren has 7):
		"Yu Meiren shadowhost 4!",
		"ENIGMA RP",
	}
	for i, want := range wantOrder {
		if entries[i].Name != want {
			t.Errorf("position %d = %q, want %q (full order: %v)", i, entries[i].Name, want, names(entries))
			break
		}
	}
	for _, e := range entries[3:] {
		if e.Joinable() {
			t.Errorf("joinable server %q sorted into the legacy tail", e.Name)
		}
	}
}

func names(entries []ServerEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func TestFetchServerList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleMasterJSON))
	}))
	defer srv.Close()

	entries, err := FetchServerList(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchServerList: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("entries = %d, want 5", len(entries))
	}
}

func TestUnsupportedReasonMentionsUpgrade(t *testing.T) {
	// The lobby copy must tell legacy server owners what to do.
	for _, needle := range []string{"not supported", "upgrade", "WebSockets"} {
		if !containsFold(UnsupportedReason, needle) {
			t.Errorf("UnsupportedReason missing %q: %s", needle, UnsupportedReason)
		}
	}
}

func TestParseDirectAddress(t *testing.T) {
	cases := []struct {
		in     string
		secure bool
		want   string
		ok     bool
	}{
		{"ws://10.0.0.5:50001", false, "ws://10.0.0.5:50001", true},
		{"wss://private.example.com:443", false, "wss://private.example.com:443", true},
		{"10.0.0.5:50001", false, "ws://10.0.0.5:50001", true},
		{"private.example.com:2096", true, "wss://private.example.com:2096", true},
		{"  host.example.org:8080  ", false, "ws://host.example.org:8080", true},
		{"no-port.example.com", false, "", false},
		{"http://example.com:80", false, "", false},
		{"ws://missing-port.example.com", false, "", false},
		{"", false, "", false},
		{"host:notaport", false, "", false},
	}
	for _, tc := range cases {
		got, err := ParseDirectAddress(tc.in, tc.secure)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("ParseDirectAddress(%q,%v) = %q,%v; want %q", tc.in, tc.secure, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("ParseDirectAddress(%q,%v) accepted invalid input as %q", tc.in, tc.secure, got)
		}
	}
}

func TestDirectEntry(t *testing.T) {
	e, err := DirectEntry("My Private Server", "wss://hidden.example.com:2096", "Invite only.")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Favorite || e.Security() != SecurityWSS || e.WebSocketURL() != "wss://hidden.example.com:2096" {
		t.Errorf("DirectEntry = %+v", e)
	}
	if e.Description != "Invite only." {
		t.Errorf("DirectEntry dropped the description: %+v", e)
	}
	if _, err := DirectEntry("bad", "tcp://x:1", ""); err == nil {
		t.Error("DirectEntry accepted a non-ws URL")
	}
}

func TestMergeFavoritesAndSort(t *testing.T) {
	entries, _ := ParseServerList([]byte(sampleMasterJSON))
	favs := []config.FavoriteServer{
		{Name: "KFO ★", URL: "ws://135.148.43.158:50000"},
		{Name: "Secret Hideout", URL: "wss://secret.example.com:443", Description: "Invite only."},
	}
	entries = MergeFavorites(entries, favs)
	SortServers(entries)

	if len(entries) != 6 {
		t.Fatalf("entries = %d, want 6 (5 master + 1 direct)", len(entries))
	}
	// Favorites pinned to the very top, ahead of higher-player servers.
	top2 := map[string]bool{entries[0].Name: true, entries[1].Name: true}
	if !top2["Killing Fever Online"] || !top2["Secret Hideout"] {
		t.Errorf("top of list = %v, want both favorites first", names(entries)[:3])
	}
	if !entries[0].Favorite || !entries[1].Favorite {
		t.Error("top entries not flagged favorite")
	}
	for _, e := range entries {
		if e.Name == "Secret Hideout" && e.Description != "Invite only." {
			t.Errorf("phone-book description lost in merge: %+v", e)
		}
		if e.Name == "Killing Fever Online" && e.Description == "" {
			t.Error("master-list description lost on a starred server")
		}
	}
	// Legacy still pinned to the bottom, after everything.
	if entries[len(entries)-1].Joinable() || entries[len(entries)-2].Joinable() {
		t.Errorf("bottom of list = %v, want the two legacy servers", names(entries))
	}
}

func containsFold(haystack, needle string) bool {
	h, n := []rune(haystack), []rune(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			a, b := h[i+j], n[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
