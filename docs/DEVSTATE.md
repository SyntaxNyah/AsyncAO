# Development state

A living handoff note so work can continue from any machine or session with
just this repository. Refreshed at each landing; trust the newest commit of
this file over anything older.

Last refresh: 2026-08-11, HEAD = the field batch 8 commit.

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

## In flight right now

Field batch 9 (not yet landed at this refresh):
1. A prominent Preview button in the theme editor: hides editing chrome and
   stages an offline sample scene (message crawling, banner scrolling) so the
   author sees the theme as a player would. Must work on a brand-new blank
   theme too.
2. The pairing preview reads the partner's first char.ini emote instead of
   assuming an emote named "normal" exists.
3. A small clip fix for animated text spans on the first chatbox row.
4. Two new easter eggs (Mint, Northgate) joining the existing five.

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
