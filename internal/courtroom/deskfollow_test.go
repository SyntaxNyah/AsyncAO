package courtroom

import (
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const deskFollowStep = 16 * time.Millisecond

func TestTheDeskFollowsTheBackgroundNameThatLoaded(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	files := map[string]string{}
	room.Assets = assetFake{files: files}
	room.Scene.Position = "def"
	room.setBackground("court", "", false)
	bg := room.Scene.BackgroundBase
	if want := room.urls.Background("court", "defensedesk"); room.Scene.DeskBase != want {
		t.Fatalf("before the background loads the desk is %q, want the likely %q at once, as AO2 has one", room.Scene.DeskBase, want)
	}

	for _, c := range []struct{ file, desk string }{
		{"witnessempty.webp", "stand"},
		{"wit.png", "wit_overlay"},
		{"defenseempty.webp", "defensedesk"},
	} {
		files[bg] = room.urls.Background("court", c.file)
		room.Update(deskFollowStep)
		if want := room.urls.Background("court", c.desk); room.Scene.DeskBase != want {
			t.Errorf("background %s gave desk %q, want %q", c.file, room.Scene.DeskBase, want)
		}
	}
}

func TestANewPositionDropsTheDeskOfTheOldOnesFallback(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	files := map[string]string{}
	room.Assets = assetFake{files: files}
	room.Scene.Position = "def"
	room.setBackground("court", "", false)
	files[room.Scene.BackgroundBase] = room.urls.Background("court", "witnessempty.webp")
	room.Update(deskFollowStep)

	// Both positions fall back to the same file, so the desk must still be worked out for the new one.
	room.Scene.Position = "pro"
	room.setBackground("court", "", false)
	files[room.Scene.BackgroundBase] = room.urls.Background("court", "witnessempty.webp")
	room.Update(deskFollowStep)
	if want := room.urls.Background("court", "stand"); room.Scene.DeskBase != want {
		t.Fatalf("desk %q, want %q", room.Scene.DeskBase, want)
	}
}

func TestDesignIniPlacesAPositionOnTheCourtAndNamesItsDesk(t *testing.T) {
	design, err := theme.ParseINI(strings.NewReader(`[court:def]
origin = 640
[overlays]
court = benches
`))
	if err != nil {
		t.Fatal(err)
	}
	urls := NewURLBuilder("https://cdn/base/")
	s := urls.PositionScenery("hall", "def", design)
	if s.Background != urls.Background("hall", courtPanorama) || s.Alts[0] != urls.Background("hall", "def") {
		t.Fatalf("names %q then %q: a position design.ini places on the court must ask for court, then its own file",
			s.Background, s.Alts)
	}
	if got := DeskOf(courtPanorama, design); got != "benches" {
		t.Errorf("court's desk = %q, want design.ini's overlay", got)
	}
	if got := DeskOf(courtPanorama, nil); got != "court_overlay" {
		t.Errorf("court's desk without design.ini = %q", got)
	}
	if other := urls.PositionScenery("hall", "pro", design); other.Background != urls.Background("hall", "pro") {
		t.Errorf("pro is not on the court in this design.ini, but asks %q first", other.Background)
	}
}

func TestTheSceneIsAskedAgainOnceDesignIniArrives(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	design, err := theme.ParseINI(strings.NewReader(`[court:def]
origin = 640
`))
	if err != nil {
		t.Fatal(err)
	}
	fake := &designFake{}
	room.Assets = fake
	room.Scene.Position = "def"
	room.setBackground("hall", "", false)
	if want := room.urls.Background("hall", "def"); room.Scene.BackgroundBase != want {
		t.Fatalf("before design.ini the background is %q, want %q", room.Scene.BackgroundBase, want)
	}
	fake.design, fake.read = design, true
	room.Update(deskFollowStep)
	if want := room.urls.Background("hall", courtPanorama); room.Scene.BackgroundBase != want {
		t.Fatalf("after design.ini the background is %q, want the court %q", room.Scene.BackgroundBase, want)
	}
}

// designFake is an assetFake whose design.ini arrives when the test says.
type designFake struct {
	assetFake
	design *theme.INI
	read   bool
}

func (f *designFake) BackgroundDesign(string) (*theme.INI, bool) { return f.design, f.read }

func TestDesignIniPlacesTheScenery(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	design, err := theme.ParseINI(strings.NewReader(designPlacement))
	if err != nil {
		t.Fatal(err)
	}
	fake := &designFake{design: design, read: true}
	room.Assets = fake
	room.Scene.Position = "def"
	room.setBackground("hall", "", false)
	room.Update(deskFollowStep)
	sc := room.Scene
	if !sc.SceneryStretch || sc.SceneryScaling != ScalingPixel {
		t.Errorf("stretch %v scaling %v, want design.ini's", sc.SceneryStretch, sc.SceneryScaling)
	}
	if !sc.SceneryPanorama || sc.SceneryOrigin != designCourtOrigin {
		t.Errorf("panorama %v at %d, want the court origin %d", sc.SceneryPanorama, sc.SceneryOrigin, designCourtOrigin)
	}
	// The court is missing and def.png loaded: the origin comes from [def], as AO2 reads it from the plain position.
	fake.files = map[string]string{sc.BackgroundBase: room.urls.Background("hall", "def") + ".png"}
	room.Update(deskFollowStep)
	if room.Scene.SceneryOrigin != designOwnOrigin {
		t.Errorf("origin %d after def.png loaded, want [def]'s %d", room.Scene.SceneryOrigin, designOwnOrigin)
	}
}

const (
	designCourtOrigin = 640
	designOwnOrigin   = 320
	designPlacement   = `stretch = true
scaling = pixel
[court:def]
origin = 640
[def]
origin = 320
`
)
