# Development state

A living handoff note so work can continue from any machine or session with
just this repository. Refreshed at each landing; trust the newest commit of
this file over anything older.

Last refresh: 2026-08-11 (second refresh, pre-mobile handoff). Pushed HEAD =
the "docs: add the development state handoff" commit on top of field batch 8.

## Where v1.90.0 stands

The v1.90.0 arc (started 2026-08-03) is feature complete and in field
testing. It ships the theme editor and everything around it:

- A full in-client theme editor (Settings > Theme editor): live canvas,
  drag/resize with handles, magnets, one undo stack, inspector rails with
  font family/size pickers, element rename.
- Create a theme from scratch: blank, from a preset template, or by copying
  the current theme. Drop images and animated WebP straight into the editor.
- A preset gallery: 14 styles x 21 layouts, every combination verified by a
  headless bake test.
- .aotheme import/export (internal/themepack): bounded zip transport on the
  shared safepath guards, two-drop import consent, AO2-compat export.
- 14 free drop-in themes under themes/ (repo-only distribution; the client
  links here from Settings).
- Three field batches of fixes from live testing: blip cadence matched to
  AO2's chat tick, chatbox line spacing, effect sound effects, the legacy
  roster poll removed, shout arming, pairing preview, ghost text previewing
  the queued message, chatbox crawl scrolling, nested character folders on
  local mounts, and more. Read the commit log; each message documents its
  change thoroughly.

## FINISH-THE-RELEASE RUNBOOK (the current session's job)

The desktop session handed off mid-batch. Everything needed is pushed.
Finish in this order:

1. Check out branch wip/field-batch-9. It holds the half-built batch as ONE
   WIP commit (never merge that commit as-is). Items 2-4 below are
   substantially built there; finish items 1 and 5, then land the WHOLE
   batch on main as one clean commit (squash; write a proper message in the
   log's style, no contributor names) after an adversarial review pass.
2. Gates before that commit: gofmt, go vet, go test -race -p 1 -count=1
   ./... (the videoenc mux test and the courtroom egg alloc gate are known
   load flakes; green in isolation excuses them), AND staticcheck ./...
   with ZERO findings (CI runs it; main is red until this lands).
3. Release notes: docs/release/CHANGELOG-v1.90.0-draft.md is the approved
   text. Paste its section into internal/ui/assets/CHANGELOG.md as
   "## v1.90.0-test1" (the header must EQUAL the tag; a stable section
   always sits ABOVE its -testN siblings for the updater's extractor).
   Commit that separately.
4. Push main only when the owner says push. The owner tags v1.90.0-test1
   themselves (tags trigger .github/workflows/release.yml which builds all
   release assets; the in-app updater consumes them, so tags are sacred).
5. Delete the wip branch after the clean landing.

Field batch 9's items:
1. A prominent Preview button in the theme editor: hides editing chrome and
   stages an offline sample scene (message crawling, banner scrolling) so the
   author sees the theme as a player would. Must work on a brand-new blank
   theme too. Status: in progress (the one item still being written).
2. The pairing preview reads the partner's first char.ini emote instead of
   assuming an emote named "normal" exists. Status: substantially built.
3. A small clip fix for animated text spans on the first chatbox row.
   Status: substantially built.
4. Two new easter eggs (Mint, Northgate) joining the existing five.
   Status: substantially built.
5. The CI staticcheck findings on main (9 items; CI is knowingly red on main
   until this lands): two self-comparing test assertions to repair, a
   possibly-unwired redo guard to verify, four dead functions to judge, two
   mechanical style fixes. Status: queued in the same batch.

Note for any non-desktop session: CI on main is red from the staticcheck
findings above. This is known and owned; do not ship a separate fix for it.

## What remains before release

- Field batch 9 lands (one commit).
- The release notes and the hands-on verification checklist are drafted and
  approved locally; the notes get pasted into internal/ui/assets/CHANGELOG.md
  at tag time (the header line must equal the tag; stable sections sit above
  their -testN siblings for the updater's extractor).
- The plan is a v1.90.0-test1 tag first, tested on a real server, then the
  stable tag. Tags trigger .github/workflows/release.yml which builds all
  release assets; the in-app updater consumes them.

## How to work in this repo

- Read CLAUDE.md first. The hard rules there are enforced; notably rule 8
  (race-clean gate before any commit), rule 9 (no magic numbers), rule 10
  (automation never pushes), and rule 11 (structural changes ship
  encapsulation tests).
- Full gate: gofmt + go vet + `go test -race -p 1 -count=1 ./...` with the
  CGO environment described in CLAUDE.md. On Windows, PREPEND
  C:\msys64\ucrt64\bin to PATH.
- Known flaky tests, both order/load sensitive and pre-existing: the videoenc
  mux test and the courtroom egg zero-alloc gate. Green in isolation means
  the flake, not your change; prove it with isolated -count runs.
- Landing convention: verify the exact commit content with the full gate (a
  temporary worktree at HEAD plus only the staged files works well), commit
  with an explicit path list, never `git add -A`.
- Commit messages are terse about people: contributor credits live in the
  release notes, not the log.

## Open items beyond the arc

- The theme editor and everything in this arc still needs a full hands-on
  pass on a real screen; the checklist for it is maintained locally and its
  substance is reflected in the release notes at tag time.
- AO2 theme text-layout parity landed a large improvement (line pitch,
  showname shrink-to-fit, chatbox alpha); per-theme fine-tuning continues as
  field reports arrive.
- Deferred by explicit decision: the KFO CHECK/CU/joined_area packets (the
  unhandled-packet debug lines are expected), client-side tamper detection
  (server-side instead), transmitted custom shout stingers (abuse vector).
