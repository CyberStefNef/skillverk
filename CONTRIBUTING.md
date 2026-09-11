# Contributing

Use Git and the Go toolchain declared in [go.mod](go.mod).

```sh
make build     # bin/skillverk
make check     # tests and go vet
go test -race ./...
```

CI tests and builds on Linux, macOS, and Windows, runs the race detector on
Linux, and checks installer syntax. Format Go changes with `gofmt`.

## Code layout

- `main.go`, `commands.go`, `setup.go`: CLI commands, flags, and output.
- `internal/library/`: imports, storage, native links, ownership, and setup plans.
- `internal/tui/`: the picker and setup review, built with Bubble Tea.
- `internal/onboarding/`: agent review sessions and the embedded companion skill.

Tests use temporary libraries and Git repositories. Never run tests against a
real skill installation or execute imported workflows.

Ownership and recovery checks matter here. Before replacing or deleting a path,
verify that Skillverk owns it. Preserve active content when an update fails.
Report which harness operations failed and keep enough state to retry them.
Windows links must work without administrator rights or Developer Mode.

Keep changes focused and explain their effect on users. Update the README when
commands or behavior change.

## Optional checks

Tests verify links on disk. To check discovery through installed agents:

```sh
python3 scripts/native-discovery.py --skillverk bin/skillverk
```

The probe initializes discovery without sending a user turn or running a skill.
On Windows, it requires an ordinary user without Developer Mode.

To check OpenCode separately:

```sh
SKILLVERK_TEST_OPENCODE="$(command -v opencode)" \
  go test ./internal/library -run TestOpenCodeNativeDiscovery -count=1
```

`make preview` regenerates the README image and requires Node.js.

## Pull requests

Use a conventional commit title, such as `feat: import skill archives` or
`fix: preserve links after a failed update`. A scope is optional, for example
`fix(import): preserve executable permissions`. The PR-title check enforces
`type(optional scope): description`, allowing `!` before the colon for a
breaking change.

Squash merge pull requests. The PR title becomes the commit title on `main`,
and Release Please uses it to prepare releases. Individual branch commits do
not need to follow the convention. Explain breaking changes and migration steps
in the PR body.

Use `feat` for features, `fix` for bug fixes, and `perf` for performance changes.
Use `refactor`, `docs`, `build`, `ci`, `test`, `chore`, or `revert` for other work.

## Release

[Release Please](https://github.com/googleapis/release-please-action) collects
conventional commits on `main` into a release PR with a proposed version and
changelog. Review that PR and squash merge it when ready to release.

The release workflow builds the resulting tag for Linux, macOS, and Windows on
amd64 and arm64, then uploads the archives and `checksums.txt`. It calls the
build workflow directly because tags created with the built-in GitHub token
[do not trigger another workflow](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).

If building or uploading fails, run the Release workflow manually with the
existing tag. The installers need all archives and checksums to be present.
Do not move published tags.

Release builds report their tag through `skillverk --version`. Source builds
report the Go module version or Git commit. Build requirements live in `go.mod`.

Configure GitHub once before releasing:

- Enable squash merging and disable merge commits and rebase merging. Use the
  PR title as the default squash commit title and the PR body as its message.
- Under Actions permissions, allow GitHub Actions to create pull requests.
- Require the PR-title and code checks before merging. Approve workflow runs
  on Release Please PRs when GitHub requests it.
