package ui

import (
	"errors"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
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

func TestTickPrereleaseOncePerBuild(t *testing.T) {
	const rc2, rc3 = "v2.1.0-rc.2", "v2.1.0-rc.3"
	a := testTabApp(t)
	untick := func() { a.d.Prefs.Update(func(sn *config.Settings) { sn.UpdatePrerelease = false }) }
	ticked := func() bool { return a.d.Prefs.Load().UpdatePrerelease }

	a.tickPrereleaseOnce("v2.0.2")
	if ticked() {
		t.Fatal("a stable build must not tick pre-releases")
	}
	a.tickPrereleaseOnce(rc2)
	if !ticked() {
		t.Fatal("a pre-release build's first launch must tick pre-releases")
	}
	untick()
	a.tickPrereleaseOnce(rc2)
	if ticked() {
		t.Fatal("unticking must hold for the same build")
	}
	a.tickPrereleaseOnce(rc3)
	if !ticked() {
		t.Fatal("a different pre-release build must tick pre-releases again")
	}
}
