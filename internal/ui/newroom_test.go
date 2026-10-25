package ui

// The ROOM-CONSTRUCTION seam's gates (newroom.go).
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
// The split exists because a fixture that owns its mount privately can only ever
// serve misses: every consumer of roomFactoryApp before this one drove the
// fetch's FAILURE path, which is exactly how the char.ini→cache mapping shipped
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
			return a.link.room
		},
	}, {
		name: "the pinned split pane (pinToSplit)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			tab := &courtTab{state: sessionState{srvMeta: serverMetaState{serverName: "Pinned"}, link: linkState{sess: &courtroom.Session{}}}}
			a.tabs = append(a.tabs, tab)
			a.pinToSplit(tab)
			return a.splitRoom
		},
		stop: func(a *App) { a.clearSplit() },
	}, {
		name: "a replay (startReplay)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.startReplay(gateScene(), "gate")
			return a.sel.replayRoom
		},
		stop: func(a *App) { a.sel.replaying = false },
	}, {
		// The same entry point, in the other of its two modes: a Preview launched
		// from the scene maker is authoring, so it takes the exemption the export
		// it is previewing takes. Without this row the makerOpen branch of
		// startReplay is production code no gate reaches.
		name: "a replay previewed from the scene maker (startReplay, makerOpen)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.sel.makerOpen = true
			a.startReplay(gateScene(), "gate")
			return a.sel.replayRoom
		},
		stop:      func(a *App) { a.sel.replaying, a.sel.makerOpen = false, false },
		authoring: true,
	}, {
		name: "the scene maker's preview (ensureMakerPreview)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			// A BACKGROUND line, not a message: the preview feeds the selected
			// event into the room it just built, and a message would drive the
			// blip mixer, which this fixture has no SDL audio device for. The room
			// is built and wired either way, which is what is under test.
			a.sel.makerScene = &sceneRecording{
				Version: recordingVersion,
				Origin:  gateOrigin,
				Events:  []recEvent{{Kind: int(courtroom.EventBackground), Text: "courtroom"}},
			}
			a.sel.makerSel = 0
			a.ensureMakerPreview()
			return a.sel.makerPreviewRoom
		},
		stop:      func(a *App) { a.sel.makerScene, a.sel.makerPreviewRoom = nil, nil },
		authoring: true,
	}, {
		// The theme editor's canned Preview. A VIEWING room, not an authoring one:
		name: "the theme editor's canned preview (enterEditorPreview)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			if a.elemBake.themeSidecar == nil {
				a.elemBake.themeSidecar = theme.NewSidecar()
			}
			if !a.armThemeEditor(ScreenSettings) {
				t.Fatal("armThemeEditor refused a theme that has a sidecar — the preview cannot be reached")
			}
			a.enterEditorPreview()
			if a.te == nil || a.te.preview == nil {
				t.Fatal("enterEditorPreview staged no preview")
			}
			return a.te.preview.state.link.room
		},
		stop: func(a *App) {
			a.exitEditorPreview()
			a.te = nil
		},
	}, {
		name: "the comic/video export (startSceneExport)",
		drive: func(t *testing.T, a *App) *courtroom.Courtroom {
			a.startSceneExport(gateScene(), "gate", exportComic)
			if a.sel.gif == nil {
				t.Fatal("startSceneExport did not begin an export — the fixture cannot reach the export room")
			}
			return a.sel.gif.room
		},
		stop: func(a *App) {
			a.sel.gif, a.sel.gifExporting = nil, false
			a.endExportScaleBracket()
		},
		authoring: true,
	}}
}

// TestEveryRoomTheAppBuildsIsWiredAndPrefSeeded is the headline gate: EVERY mode
// that stages messages gets the same wiring and the same preferences.
// It exists because two things were true at once and neither was visible. Two of
// the five rooms — the pinned split pane and the comic/video export — were built
// raw and never reached wireRoomOverlay, so a screen effect that played in the
// The preference is driven BOTH WAYS on purpose. Off alone would pass on a room
// hardcoded to false; on alone would pass on the bug this replaces. Together
// they can only pass if the room actually read the preference.
func TestEveryRoomTheAppBuildsIsWiredAndPrefSeeded(t *testing.T) {
	for _, want := range []bool{false, true} {
		for _, p := range stagedRoomPaths() {
			t.Run(p.name+"/effects="+boolWord(want), func(t *testing.T) {
				a, cleanup := roomFactoryApp(t)
				defer cleanup()
				a.d.Prefs.Update(func(sn *config.Settings) { sn.ScreenEffects = want })
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
	a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = true })
	defer a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = false })

	a.startSceneExport(gateScene(), "gate", exportComic)
	if a.sel.gif == nil || a.sel.gif.room == nil {
		t.Fatal("no export room")
	}
	if a.sel.gif.room.ReduceMotion {
		t.Error("the export room inherited ReduceMotion — an export must carry what the author wrote, not what this viewer set")
	}
	a.sel.gif, a.sel.gifExporting = nil, false
	a.endExportScaleBracket()

	a.sel.makerScene = &sceneRecording{
		Version: recordingVersion,
		Origin:  gateOrigin,
		Events:  []recEvent{{Kind: int(courtroom.EventBackground), Text: "courtroom"}},
	}
	a.sel.makerSel = 0
	a.ensureMakerPreview()
	if a.sel.makerPreviewRoom == nil {
		t.Fatal("no preview room")
	}
	if a.sel.makerPreviewRoom.ReduceMotion {
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
