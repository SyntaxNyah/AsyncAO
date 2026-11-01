# CI and releases

## What runs when

| Event | Checks |
|---|---|
| Push to a branch | Compile on Windows, Linux and macOS. Run the local checks in `CLAUDE.md` yourself. |
| PR commit, manual run | gofmt, vet, staticcheck, golangci-lint, race tests on all three OSes, lean-build tests, benchmark alloc gates, packaging and a smoke test on all three OSes. |
| Push to `main` | The same checks, without packaging: the PR already packaged the code, and a release packages it again before publishing. |
| Only docs changed (`docs/`, root `*.md`, `LICENSE`) | Nothing. |

`ci-ok` summarises all of this. It is the status to require on `main`, and it
keeps working when the jobs behind it change. Requiring it takes repo admin
access.

## Layout

- `.github/workflows/ci.yml` chooses the checks.
- `.github/workflows/checks.yml` holds lint and tests, `build.yml` holds packaging. CI and releases share both.
- `.github/workflows/release.yml` publishes; `security.yml` runs `govulncheck` weekly, outside `ci-ok`.
- `.github/actions/setup-build` installs Go and the C libraries. It is the one place the Go and SDL versions are set for CI.
- `scripts/package.sh build|pack|smoke` is the one build path, and also runs locally.
- `packaging/release-assets.txt` lists every asset a release publishes.

## Cutting a release

Add a `## vX.Y.Z - date` section at the top of `internal/ui/assets/CHANGELOG.md`
in the PR and merge it. Once CI passes on `main`, `release.yml` builds and signs
every platform and variant, checks the assets against the manifest, uploads them
to a draft and publishes it. Publishing creates the tag.

A hyphenated tag (`v2.1.0-test.1`) pushed by hand publishes a prerelease from any
branch: stable clients never see it, the in-app experimental channel does. A
stable tag pushed by hand must be on `main`. Tag pushes run the checks themselves.

## Rules

- **A published version is never replaced.** Fix forward with a new patch version.
- **Retrying:** re-run the failed jobs of the Release run. A failed run leaves at most a draft, which the next attempt replaces.
- **Every platform must build.** Only the Flatpaks are optional.
- **Signing** is skipped without certificates, and fails the release if a certificate exists and signing breaks. See `docs/CODE-SIGNING.md`.
- `release.yml` triggered by CI runs the workflow file from `main`, so changes to that path only take effect after merging.

## Asset names

The self-updater picks one asset by name, `update.SelfUpdateAssetMatch`, and must
never match a variant: `macos-bundle-arm64` deliberately does not contain the
`macos-arm64` of the bare binary, and neither does the Homebrew tarball. A test in
`internal/update` checks the matcher against the manifest, so renaming an asset
means changing both.

## Requiring manual approval later

The publish job already runs in the `release` environment. A repo admin can add a
required reviewer there, and every release then waits for that approval before
publishing.
