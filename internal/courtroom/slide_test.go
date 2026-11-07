package courtroom

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	slideTestFrom     = 640
	slideTestTo       = 2560
	slideTestDuration = 600 * time.Millisecond
	slideTestStep     = 16 * time.Millisecond
	slideTestDesign   = `[court:def]
origin = 640
slide_ms_pro = 600
[court:pro]
origin = 2560
`
)

// slideRoom is a room on a court panorama whose court has loaded, with design.ini read.
func slideRoom(t *testing.T) *Courtroom {
	t.Helper()
	room, sess, _, _ := newCourtroomRig(t)
	sess.Background = "hall"
	design, err := theme.ParseINI(strings.NewReader(slideTestDesign))
	if err != nil {
		t.Fatal(err)
	}
	court := room.urls.Background("hall", courtPanorama)
	room.Assets = &designFake{assetFake: assetFake{files: map[string]string{court: court + ".webp"}}, design: design, read: true}
	room.Scene.Position = "def"
	room.setBackground("hall", "", false)
	room.Update(slideTestStep)
	return room
}

func say(room *Courtroom, char, side string, slide bool) {
	msg := waitMsg(char, "normal", "hi")
	msg.Side, msg.Slide = side, slide
	room.begin(msg)
}

func TestAMoveOnTheCourtSlidesFromTheLastSpeaker(t *testing.T) {
	room := slideRoom(t)
	say(room, "Phoenix", "def", false)
	phoenix := room.Scene.Speaker.IdleBase
	say(room, "Edgeworth", "pro", true)

	sl := room.Scene.Slide
	if room.Phase() != PhaseSlide || !sl.Active {
		t.Fatalf("phase %d, slide %+v: a move between two court positions with a duration must slide", room.Phase(), sl)
	}
	if sl.From != slideTestFrom || sl.To != slideTestTo || sl.Total != slideTestDuration {
		t.Errorf("slide %d→%d over %v, want %d→%d over %v", sl.From, sl.To, sl.Total, slideTestFrom, slideTestTo, slideTestDuration)
	}
	if sl.Previous.Active != phoenix || room.Scene.Speaker.Active != room.Scene.Speaker.IdleBase {
		t.Errorf("the last speaker holds %q and the new one shows %q, want both idle", sl.Previous.Active, room.Scene.Speaker.Active)
	}
	for elapsed := time.Duration(0); elapsed <= slideTestDuration; elapsed += slideTestStep {
		room.Update(slideTestStep)
	}
	if room.Phase() == PhaseSlide || room.Scene.Slide.Active {
		t.Fatal("the slide must end after its duration and the message carry on")
	}
}

func TestTheLastPairIsHeldThroughTheSlide(t *testing.T) {
	room := slideRoom(t)
	msg := waitMsg("Phoenix", "normal", "hi")
	msg.Side = "def"
	msg.Pair = protocol.PairInfo{CharID: 1, Name: "Maya", Emote: "normal", Order: protocol.PairSpeakerBehind, HasOrder: true}
	room.begin(msg)
	maya := room.Scene.Pair.IdleBase
	say(room, "Edgeworth", "pro", true)

	sl := room.Scene.Slide
	if !sl.Active || !sl.PreviousPaired || sl.PreviousPair.Active != maya || sl.PreviousInFront {
		t.Fatalf("slide %+v: the last pair must be held idle, behind order kept", sl)
	}
}

func TestNoSlideWithoutEverythingAO2Asks(t *testing.T) {
	cases := map[string]func(room *Courtroom){
		"the message did not ask": func(room *Courtroom) { say(room, "Edgeworth", "pro", false) },
		"same position":           func(room *Courtroom) { say(room, "Edgeworth", "def", true) },
		"no duration back": func(room *Courtroom) {
			say(room, "Edgeworth", "pro", true)
			room.Update(slideTestDuration)
			say(room, "Phoenix", "def", true)
		},
		"motion reduced": func(room *Courtroom) { room.ReduceMotion = true; say(room, "Edgeworth", "pro", true) },
	}
	for name, run := range cases {
		room := slideRoom(t)
		say(room, "Phoenix", "def", false)
		run(room)
		if room.Phase() == PhaseSlide {
			t.Errorf("%s: slid", name)
		}
	}
}

func TestTheDropdownOffersAO2sPositionsWithArt(t *testing.T) {
	design, err := theme.ParseINI(strings.NewReader("positions = court:jud, lobby\n"))
	if err != nil {
		t.Fatal(err)
	}
	all := BackgroundPositions(design)
	if !slices.Contains(all, "court:jud") || !slices.Contains(all, "lobby") || all[0] != "def" {
		t.Fatalf("positions %q, want the legacy ones first, then design.ini's", all)
	}
	urls := NewURLBuilder("https://cdn/base/")
	for _, c := range []struct {
		pos, file string
		want      bool
	}{
		{"def", "", true},
		{"def", "def.png", true},
		{"def", "defenseempty.webp", true},
		{"def", "witnessempty.webp", false},
		{"wit", "wit.png", true},
		{"lobby", "wit.png", false},
		{"court:jud", "court.webp", true},
	} {
		file := ""
		if c.file != "" {
			file = urls.Background("hall", c.file)
		}
		if got := PositionHasArt(c.pos, file, design); got != c.want {
			t.Errorf("%s loaded as %q: has art %v, want %v", c.pos, c.file, got, c.want)
		}
	}
}
