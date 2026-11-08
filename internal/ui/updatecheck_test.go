package ui

import (
	"errors"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

func newUpdateCheckApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.updateRes = make(chan updateCheckResult, 1)
	a.updateChecking = true
	return a
}

func TestPollUpdateUpToDateClearsStaleOffer(t *testing.T) {
	a := newUpdateCheckApp(t)
	a.updateRel = &update.Release{Version: "9.9.9"}
	a.updateRes <- updateCheckResult{}
	a.pollUpdate()
	if a.updateChecking || a.updateCheckedAt.IsZero() {
		t.Fatalf("checking = %v checkedAt = %v, want a finished check", a.updateChecking, a.updateCheckedAt)
	}
	if a.updateRel != nil || a.updateShow {
		t.Fatal("an up-to-date answer must drop the earlier offer")
	}
}

func TestPollUpdateFoundOpensModal(t *testing.T) {
	a := newUpdateCheckApp(t)
	a.updateRes <- updateCheckResult{rel: &update.Release{Version: "9.9.9", Prerelease: true}}
	a.pollUpdate()
	if a.updateRel == nil || !a.updateShow {
		t.Fatal("a found release must be kept and shown")
	}
}

func TestPollUpdateErrorKeepsOfferAndRecordsReason(t *testing.T) {
	a := newUpdateCheckApp(t)
	offer := &update.Release{Version: "9.9.9"}
	a.updateRel = offer
	a.updateRes <- updateCheckResult{err: errors.New("offline")}
	a.pollUpdate()
	if a.updateCheckErr != "offline" {
		t.Fatalf("updateCheckErr = %q", a.updateCheckErr)
	}
	if a.updateRel != offer {
		t.Fatal("a failed check must not drop the offer already found")
	}
}

func TestStartUpdateCheckSkipsWhileInstalling(t *testing.T) {
	a := newUpdateCheckApp(t)
	a.updateChecking = false
	a.updateStaged = true
	a.startUpdateCheck()
	if a.updateChecking {
		t.Fatal("a check must not start once an update is staged")
	}
}
