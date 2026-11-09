# CI and releases

## What runs when

Contributor CI and releases are separate pipelines that share only `lint.yml`, `checks.yml` and `build.yml`.

| Event | What runs |
|---|---|
| Push to a branch with no open PR | Lint, then, only when it passes, package on Windows, Linux and macOS, with no smoke test. Test builds appear under Actions, the run, Artifacts, for 14 days, one bundle file per platform with no extra zip around it, and report version `dev`, so they never self-update. Run the local checks in `CLAUDE.md` yourself. |
| Draft PR commit | The same lint and packaging. |
| Commit on a PR that is ready for review, manual CI run | gofmt, vet, staticcheck and golangci-lint first; only when they pass, race tests on all three OSes, benchmark alloc gates, packaging and a smoke test on all three OSes. |
| Merge or push to `main` | Nothing, unless it releases (below). |
| Only docs changed (`docs/`, root `*.md`, `LICENSE`) | Nothing. |
| A release starts (below) | All the checks again, then packaging, then publishing. |

`ci-ok` summarises PR CI. It is the status to require on PRs, and it keeps working
when the jobs behind it change. Requiring it takes repo admin access. Branch pushes
never report it, so a commit has one result for it. Opening a PR on a commit that was
already pushed builds that commit a second time; later pushes build once.

## Layout

- `.github/workflows/ci.yml` chooses the checks for PRs; `branch-build.yml` packages branches with no open PR.
- `.github/workflows/lint.yml` holds lint, `checks.yml` runs it and then the tests, `build.yml` holds packaging. Every
  build waits for lint: PR CI and branch builds run it on their own first, so a lint failure starts no tests and no
  builds, and releases run `checks.yml` before building.
- `.github/workflows/release.yml` publishes; `security.yml` runs `govulncheck` weekly, outside `ci-ok`.
- `.github/actions/setup-build` installs Go and the C libraries. It is the one place the Go and SDL versions are set for CI.
- `scripts/package.sh build|pack|smoke` is the one build path, and also runs locally.
- `packaging/release-assets.txt` lists every asset a release publishes.

## Caches

Each kind of job has its own Go cache, named from `go.sum`, the C compiler version and a
file for that job, so changing any of them starts a fresh cache. After a successful first
attempt on a branch or PR, `clean-caches` deletes that ref's Go, MSYS2 and SDL caches the
run didn't use. A deleted branch or closed PR loses them all. GitHub removes anything
unused for 7 days. A new branch's first build is cold, because `main` runs no CI to fill a
shared cache.

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

The self-updater picks one asset by name, `update.SelfUpdateAssetMatch`: the
Windows bundle zip, the Linux AppImage or the macOS bundle tarball. Clients up to
v1.95.1 match `windows-x86_64.exe` and `macos-arm64` instead and copy that bare
binary over themselves without its libraries, so no published name may contain
either. Finding nothing, they open the release page. Tests in `internal/update`
check both rules against the manifest, so renaming an asset means changing both.

## Requiring manual approval later

The publish job already runs in the `release` environment. A repo admin can add a
required reviewer there, and every release then waits for that approval before
publishing.
