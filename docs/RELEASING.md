# CI and releases

## What runs when

Contributor CI and releases are separate pipelines that share only `checks.yml` and `build.yml`.

| Event | What runs |
|---|---|
| Push to a branch with no open PR | Package on Windows, Linux and macOS, with no smoke test. Test builds appear under Actions, the run, Artifacts, for 14 days, and report version `dev`, so they never self-update. Run the local checks in `CLAUDE.md` yourself. |
| Draft PR commit | The same packaging. |
| Commit on a PR that is ready for review, manual CI run | gofmt, vet, staticcheck, golangci-lint, race tests on all three OSes, benchmark alloc gates, packaging and a smoke test on all three OSes. |
| Merge or push to `main` | Nothing, unless it releases (below). |
| Only docs changed (`docs/`, root `*.md`, `LICENSE`) | Nothing. |
| A release starts (below) | All the checks again, then packaging, then publishing. |

`ci-ok` summarises contributor CI. It is the status to require on PRs, and it
keeps working when the jobs behind it change. Requiring it takes repo admin
access.

## Layout

- `.github/workflows/ci.yml` chooses the checks for branches and PRs.
- `.github/workflows/checks.yml` holds lint and tests, `build.yml` holds packaging. CI and releases share both.
- `.github/workflows/release.yml` publishes; `security.yml` runs `govulncheck` weekly, outside `ci-ok`.
- `.github/actions/setup-build` installs Go and the C libraries. It is the one place the Go and SDL versions are set for CI.
- `scripts/package.sh build|pack|smoke` is the one build path, and also runs locally.
- `packaging/release-assets.txt` lists every asset a release publishes.

## Cutting a release

Normally, add a `## vX.Y.Z - date` section at the top of
`internal/ui/assets/CHANGELOG.md` in a PR. Then use any of these. They all start
the same pipeline:

- **Merge it to `main`.** A CHANGELOG change with a new newest version releases
  right away. Merges that don't touch the file start nothing.
- **Push a tag:** `git tag vX.Y.Z && git push origin vX.Y.Z`.
- **Press the button:** Actions, Release, Run workflow, or
  `gh workflow run release.yml -f version=vX.Y.Z`. Use this for prereleases and retries.

The pipeline runs the checks, builds and signs every platform, checks
the assets against the manifest, uploads them to a draft and publishes it.
Publishing creates the tag, if a pushed tag didn't already.

A stable version must be on `main`. Without a CHANGELOG section it still releases,
with a warning: the notes fall back to the tag annotation and the in-app What's
New won't list it. A hyphenated
version (`v2.1.0-test.1`) publishes a prerelease and may come from any branch:
stable clients never see it, the in-app experimental channel does.

The checks run again because a green PR only tested its own merge: several PRs
together may never have run, and a direct push to `main` never ran at all.

## Rules

- **A published version is never replaced.** Fix forward with a new patch version.
- **Retrying:** re-run the failed jobs, or run the workflow again. A failed run leaves at most a draft, which the next attempt replaces.
- **Every platform must build.** Only the Flatpak is optional.
- **Signing** is skipped without certificates, and fails the release if a certificate exists and signing breaks. See `docs/CODE-SIGNING.md`.
- **Who can release:** anyone with write access.

## Asset names

The self-updater picks one asset by name, `update.SelfUpdateAssetMatch`, and must
never match the wrong file: `macos-bundle-arm64` deliberately does not contain the
`macos-arm64` of the bare binary, and neither does the Homebrew tarball. A test in
`internal/update` checks the matcher against the manifest, so renaming an asset
means changing both.

## Requiring manual approval later

The publish job already runs in the `release` environment. A repo admin can add a
required reviewer there, and every release then waits for that approval before
publishing.
