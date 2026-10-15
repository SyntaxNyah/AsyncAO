package ui

// The ROOM-CONSTRUCTION seam's gates (newroom.go).
//
// Five of them, and they catch five different deletions:
//
//   - TestEveryRoomTheAppBuildsIsWiredAndPrefSeeded drives every PRODUCTION
//     entry point and fails if any room comes back unwired, pref-deaf, or —
//     for the three authoring surfaces — pref-INFECTED. It catches gutting
//     newRoom and it catches gutting applyAuthoringVisualsToRoom.
//   - TestAuthoringRoomsSurviveTheMasterEffectsOffSwitch covers the other route
//     to the same three fields: the single master toggle that ORs itself into
//     all of them at once.
//   - TestExportKeepsItsDeliberateReduceMotionOverride pins the ruled deviation
//     on its own, unchanged since the seam landed.
//   - TestRoomConstructionHasExactlyOneCallSite fails if anything in this
//     package builds a courtroom outside that seam. It catches ADDING a sixth
//     mode the old way, which is how the split pane and the export room ended
//     up without an overlay engine in the first place.
//   - TestRoomConstructionSitesDoNotHandRollTheVisualGates fails if a
//     construction site assigns an effect gate itself instead of calling the
//     authoring seam. That is the exact shape of the defect: a lone
//     `ReduceMotion = false` beside a room that has already been handed the
//     viewer's ScreenEffects renders nothing, silently.
//
// None restates what the wiring does; all five call it.

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// roomFactoryApp is a settled courtroom plus the one dependency the maker
// preview refuses to run without (a Manager). Everything else the five paths
// touch — renderer, texture store, viewport, session, preferences — comes from
// the shared fixture, so this gate drives the same App the alloc gates do.
func roomFactoryApp(t *testing.T) (*App, func()) {
	t.Helper()
	a, _, cleanup := roomFactoryAppMounted(t)
	return a, cleanup
}

// roomFactoryAppMounted is roomFactoryApp with the Manager's local mount FOLDER
// handed back, so a gate can write a real asset file into it and drive the byte
// path end to end (write characters/<x>/char.ini, point urls at
// assets.LocalOriginFor of the same folder, and Manager.FetchRaw reads it).
//
// The split exists because a fixture that owns its mount privately can only ever
// serve misses: every consumer of roomFactoryApp before this one drove the
// fetch's FAILURE path, which is exactly how the char.ini→cache mapping shipped
// with no end-to-end coverage at all (deleting `res.idle = ini.IdleAnim()` — and
// `res.showname` beside it — left the whole package green).
func roomFactoryAppMounted(t *testing.T) (*App, string, func()) {
	t.Helper()
	a, cleanup := stageSettledCourtroom(t)
	mount := t.TempDir()
	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    assets.NewLocalFetcher([]string{mount}),
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			AutoDetectFormats =

			false

	})

	// no manifest fetch interposes on the export path
	return a, mount, cleanup
}

// gateOrigin is deliberately non-http: the replay pre-warm and the export's
// format seed both key on "http", so this keeps every path synchronous and
// network-free while still going through the real function.
const gateOrigin = "local://gate/"

// gateScene is the smallest recording every recorded-scene path accepts.
func gateScene() *sceneRecording {
	return &sceneRecording{
		Version: recordingVersion,
		Origin:  gateOrigin,
		Events: []recEvent{{
			Kind:    int(courtroom.EventMessage),
			Message: &protocol.ChatMessage{CharName: "Witch", Emote: "normal", Side: "def", Message: "Objection!"},
		}},
	}
}

// stagedRoomPath is one production way a *courtroom.Courtroom comes into
// existence, together with the real call that makes it happen.
//
// The list is the POINT of this gate: a mode that is not here is a mode nothing
// checks. Seven rows, six entry points — a replay appears twice because it is two
// modes (viewing, and previewing for the maker). What stops a seventh from being
// added without joining the list is TestRoomConstructionHasExactlyOneCallSite
// (nothing may build a room another way) plus roomConstructionFuncs below
// (the staging sites are pinned by name).
type stagedRoomPath struct {
	name string
	// drive runs the real entry point and returns the room it produced.
	drive func(t *testing.T, a *App) *courtroom.Courtroom
	// stop undoes anything the drive left running. nil when there is nothing.
	stop func(a *App)
	// authoring marks a room that renders what the AUTHOR wrote rather than what
	// this viewer prefers to see (docs/wip/OOP-AUDIT.md DO-NOT-FIX 14). Such a
	// room is pref-EXEMPT on the three effect gates — it must come back with
	// effects ON, motion ON and sprite styles SHOWN however the viewer's
	// preferences are set — and pref-driven on everything else. Every other room
	// is pref-driven throughout.
	authoring bool
}

func stagedRoomPaths() []stagedRoomPath {
	return []stagedRoomPath{{
		name: "the live courtroom (buildRoom)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.buildRoom()
			return a.room
		},
	}, {
		name: "the pinned split pane (pinToSplit)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			tab := &courtTab{state: sessionState{serverName: "Pinned", sess: &courtroom.Session{}}}
			a.tabs = append(a.tabs, tab)
			a.pinToSplit(tab)
			return a.splitRoom
		},
		stop: func(a *App) { a.clearSplit() },
	}, {
		name: "a replay (startReplay)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.startReplay(gateScene(), "gate")
			return a.replayRoom
		},
		stop: func(a *App) { a.replaying = false },
	}, {
		// The same entry point, in the other of its two modes: a Preview launched
		// from the scene maker is authoring, so it takes the exemption the export
		// it is previewing takes. Without this row the makerOpen branch of
		// startReplay is production code no gate reaches.
		name: "a replay previewed from the scene maker (startReplay, makerOpen)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.makerOpen = true
			a.startReplay(gateScene(), "gate")
			return a.replayRoom
		},
		stop:      func(a *App) { a.replaying, a.makerOpen = false, false },
		authoring: true,
	}, {
		name: "the scene maker's preview (ensureMakerPreview)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			// A BACKGROUND line, not a message: the preview feeds the selected
			// event into the room it just built, and a message would drive the
			// blip mixer, which this fixture has no SDL audio device for. The room
			// is built and wired either way, which is what is under test.
			a.makerScene = &sceneRecording{
				Version: recordingVersion,
				Origin:  gateOrigin,
				Events:  []recEvent{{Kind: int(courtroom.EventBackground), Text: "courtroom"}},
			}
			a.makerSel = 0
			a.ensureMakerPreview()
			return a.makerPreviewRoom
		},
		stop:      func(a *App) { a.makerScene, a.makerPreviewRoom = nil, nil },
		authoring: true,
	}, {
		// The theme editor's canned Preview. A VIEWING room, not an authoring one:
		// its whole job is to show the author what a PLAYER sees, so the viewer's
		// effect prefs must reach it (and requirement 6 — reduce motion freezes the
		// demo — is that same pref arriving, not an exemption from it).
		name: "the theme editor's canned preview (enterEditorPreview)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			if a.themeSidecar == nil {
				a.themeSidecar = theme.NewSidecar()
			}
			if !a.armThemeEditor(ScreenSettings) {
				t.Fatal("armThemeEditor refused a theme that has a sidecar — the preview cannot be reached")
			}
			a.enterEditorPreview()
			if a.te == nil || a.te.preview == nil {
				t.Fatal("enterEditorPreview staged no preview")
			}
			return a.te.preview.state.room
		},
		stop: func(a *App) {
			a.exitEditorPreview()
			a.te = nil
		},
	}, {
		name: "the comic/video export (startSceneExport)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.startSceneExport(gateScene(), "gate", exportComic)
			if a.gif == nil {
				t.Fatal("startSceneExport did not begin an export — the fixture cannot reach the export room")
			}
			return a.gif.room
		},
		stop: func(a *App) {
			a.gif, a.gifExporting = nil, false
			a.endExportScaleBracket()
		},
		authoring: true,
	}}
}

// TestEveryRoomTheAppBuildsIsWiredAndPrefSeeded is the headline gate: EVERY mode
// that stages messages gets the same wiring and the same preferences.
//
// It exists because two things were true at once and neither was visible. Two of
// the five rooms — the pinned split pane and the comic/video export — were built
// raw and never reached wireRoomOverlay, so a screen effect that played in the
// tab you were watching did not play in the pane beside it and did not appear in
// any exported comic. And FOUR of the five never received the ScreenEffects
// preference at all, so they ran on NewCourtroom's hardcoded `true`: with the
// setting OFF, a replay or a maker preview still resolved effect rosters,
// fetched misc manifests, decoded art and uploaded pages — the exact cost
// TestVisualGateOffCostsNoResolve forbids, on rooms that test cannot see.
//
// The preference is driven BOTH WAYS on purpose. Off alone would pass on a room
// hardcoded to false; on alone would pass on the bug this replaces. Together
// they can only pass if the room actually read the preference.
//
// The AUTHORING rooms are the mirror image, and they are why the exemption is a
// column in the table rather than an assertion the reader has to infer. A comic
// export, a maker preview and a maker-launched replay render what the recording
// says: pushing the viewer's effect prefs onto them takes the shake, the flash
// and the overlays out of a file that outlives this session — and the export
// asks for them in writing, at its call site in gifexport.go. So the same
// two preference settings that MUST reach the three viewing rooms MUST NOT
// reach the three authoring ones, and one loop proves both.
func TestEveryRoomTheAppBuildsIsWiredAndPrefSeeded(t *testing.T) {
	for _, want := range []bool{false, true} {
		for _, p := range stagedRoomPaths() {
			t.Run(p.name+"/effects="+boolWord(want), func(t *testing.T) {
				a, cleanup := roomFactoryApp(t)
				defer cleanup()
				a.d.Prefs.SetScreenEffects(want)
				// Driven together with ScreenEffects, and read INVERTED (the pref
				// says "hide"), so each value of want exercises one side of the
				// authoring exemption: at want=false a pref-driven room must go
				// dark while an authoring one stays lit, and at want=true a
				// pref-driven room must drop received sprite styles while an
				// authoring one keeps them.
				a.d.Prefs.Update(func(
					sn *config.
						Settings) {

					sn.
						HideSpriteStyles =

						want

				})

				// Driven too, so the "not exempt" half of the exemption is not a
				// vacuous false==false: the naming pref must reach all six rooms.
				a.d.Prefs.Update(func(
					sn *config.
						Settings) {

					sn.
						ForceCharNames =

						want

				})

				a.d.Prefs.Update(func(
					sn *config.
						Settings) {

					sn.
						DisableEffects =

						false

				})

				// the master off-switch is not what is under test here

				room := p.drive(t, a)
				if p.stop != nil {
					defer p.stop(a)
				}
				if room == nil {
					t.Fatal("the path produced no room at all")
				}
				// The overlay engine + asset probes (effectspicker.go / the render &
				// audio stores). Without these an authored effect resolves to nothing
				// and the wait/strict-sequencing gates never hold.
				if room.Assets == nil {
					t.Error("Assets is nil — the overlay engine, residency probes and scaling filter are all missing")
				}
				// The char.ini tier (charmeta.go): the same speaker must blip,
				// skin and scale identically in every mode.
				if room.Blips == nil {
					t.Error("Blips is nil — the speaker's own blip set is ignored in this mode")
				}
				if room.Chars == nil {
					t.Error("Chars is nil — the speaker's own skin/ink/showname/realization is ignored in this mode")
				}
				// The visual gates: pref-driven for a viewing room, exempt for an
				// authoring one. ForceCharNames is pref-driven in BOTH — it
				// suppresses nothing the author wrote, it only renames the chip
				// from data the recording still carries (newroom.go says so).
				wantFX, wantHide := want, want
				if p.authoring {
					wantFX, wantHide = true, false
				}
				if room.ScreenEffects != wantFX {
					if p.authoring {
						t.Errorf("ScreenEffects = %v on an AUTHORING room with the preference at %v — the viewer's toggle reached a room that renders what the AUTHOR wrote, so this export/preview ships with no shake, no flash and no overlays (effectsVisible = ScreenEffects && !ReduceMotion, so ReduceMotion = false alone cannot save it)",
							room.ScreenEffects, want)
					} else {
						t.Errorf("ScreenEffects = %v with the preference at %v — this room never received the user's setting, so it does effect work they switched off (or skips work they asked for)",
							room.ScreenEffects, want)
					}
				}
				if room.HideSpriteStyles != wantHide {
					if p.authoring {
						t.Errorf("HideSpriteStyles = %v on an AUTHORING room with the preference at %v — this viewer's off-switch stripped another speaker's recorded sprite style out of an authored scene",
							room.HideSpriteStyles, want)
					} else {
						t.Errorf("HideSpriteStyles = %v with the preference at %v — this room never received the user's setting",
							room.HideSpriteStyles, want)
					}
				}
				if p.authoring && room.ReduceMotion {
					t.Error("ReduceMotion is set on an AUTHORING room — an export or preview carries the motion the author wrote, not this viewer's accessibility choice")
				}
				if room.ForceCharNames != a.d.Prefs.Load().ForceCharNames {
					t.Errorf("ForceCharNames = %v with the preference at %v — the naming pref is not part of the authoring exemption and must reach every room",
						room.ForceCharNames, a.d.Prefs.Load().ForceCharNames)
				}
			})
		}
	}
}

// boolWord keeps the sub-test names readable without a fmt call.
func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// TestExportKeepsItsDeliberateReduceMotionOverride pins the ruled deviation
// (docs/wip/OOP-AUDIT.md DO-NOT-FIX 14): an export renders the AUTHORED effects,
// not the viewer's accessibility preference, and the seam must not normalise
// that away. The maker preview makes the same claim for the same reason.
func TestExportKeepsItsDeliberateReduceMotionOverride(t *testing.T) {
	a, cleanup := roomFactoryApp(t)
	defer cleanup()
	a.d.Prefs.SetReduceMotion(true)
	defer a.d.Prefs.SetReduceMotion(false)

	a.startSceneExport(gateScene(), "gate", exportComic)
	if a.gif == nil || a.gif.room == nil {
		t.Fatal("no export room")
	}
	if a.gif.room.ReduceMotion {
		t.Error("the export room inherited ReduceMotion — an export must carry what the author wrote, not what this viewer set")
	}
	a.gif, a.gifExporting = nil, false
	a.endExportScaleBracket()

	a.makerScene = &sceneRecording{
		Version: recordingVersion,
		Origin:  gateOrigin,
		Events:  []recEvent{{Kind: int(courtroom.EventBackground), Text: "courtroom"}},
	}
	a.makerSel = 0
	a.ensureMakerPreview()
	if a.makerPreviewRoom == nil {
		t.Fatal("no preview room")
	}
	if a.makerPreviewRoom.ReduceMotion {
		t.Error("the maker preview inherited ReduceMotion — authoring must SHOW what is being authored, or the preview is a lie")
	}
}

// TestAuthoringRoomsSurviveTheMasterEffectsOffSwitch is the second half of the
// exemption, and it needs its own gate because the master off-switch reaches the
// three gates by a DIFFERENT route: applyVisualPrefsToRoom ORs EffectsDisabled
// into all three at once, so a viewer who has never touched the individual
// toggles can still empty their own export. The exemption has to win over that
// too, and "effects visible" is the property, so this asserts the pair the room
// actually reads (ScreenEffects && !ReduceMotion) rather than one field.
func TestAuthoringRoomsSurviveTheMasterEffectsOffSwitch(t *testing.T) {
	// The comic/video export, the maker preview and a maker-launched replay.
	// Counted, because a table-driven test over a column that has quietly become
	// all-false runs zero sub-tests and reports success.
	const wantAuthoringRooms = 3
	authoring := 0
	for _, p := range stagedRoomPaths() {
		if !p.authoring {
			continue
		}
		authoring++
		t.Run(p.name, func(t *testing.T) {
			a, cleanup := roomFactoryApp(t)
			defer cleanup()
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					DisableEffects =

					true

			})

			defer a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					DisableEffects =

					false

			})

			room := p.drive(t, a)
			if p.stop != nil {
				defer p.stop(a)
			}
			if room == nil {
				t.Fatal("the path produced no room at all")
			}
			if !room.ScreenEffects || room.ReduceMotion {
				t.Errorf("ScreenEffects=%v ReduceMotion=%v under the master off-switch — every authored shake, flash and overlay is gone from this authoring room",
					room.ScreenEffects, room.ReduceMotion)
			}
			if room.HideSpriteStyles {
				t.Error("HideSpriteStyles is set under the master off-switch — the authored scene lost its recorded sprite styles")
			}
		})
	}
	if authoring != wantAuthoringRooms {
		t.Errorf("%d authoring rooms in stagedRoomPaths(), want %d — a surface that renders what the author wrote lost its exemption (or gained one)", authoring, wantAuthoringRooms)
	}
}
