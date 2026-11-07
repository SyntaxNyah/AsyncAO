package courtroom

import (
	"testing"
	"time"
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
		t.Fatalf("before the background loads the desk is %q, want the position's own %q", room.Scene.DeskBase, want)
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

func TestDeskOfFollowsAO2sPairs(t *testing.T) {
	for bg, desk := range map[string]string{
		"defenseempty": "defensedesk", "prosecutorempty": "prosecutiondesk", "witnessempty": "stand",
		"judgestand": "judgedesk", "wit": "wit_overlay", "lobby": "lobby_overlay",
	} {
		if got := DeskOf(bg); got != desk {
			t.Errorf("DeskOf(%q) = %q, want %q", bg, got, desk)
		}
	}
}
