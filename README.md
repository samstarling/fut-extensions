# fut-extensions

[fut](https://fut.sh) extensions that show each workspace branch's GitHub pull
request and Linear issue. Each directory is a standalone extension package.

- `github` publishes `pr`, `state` and `checks`. Needs an authenticated `gh`.
- `linear` publishes `issue`, `status` and `status_icon`. Reads a personal API
  key from the login keychain:
  `security add-generic-password -s fut-linear-api-key -a "$USER" -w`

Clicking the PR or issue token opens it in the browser (or the Linear app), and
each extension's `refresh` command publishes immediately. Requires an Apple
silicon Mac and fut 0.34 or later.

## Install

Install a release into fut's managed store. Each release's commit, which
contains the built binaries, is on the `release` branch and tagged
`vX.Y.Z-build`; the release workflow's summary lists the exact commands.

```sh
fut extension install-git https://github.com/samstarling/fut-extensions --rev <commit> --path github
fut extension enable github
fut extension reload
```

Or build a checkout with `mise run build` (see Development) and load it in
place from `~/.config/fut/config.toml`:

```toml
extensions = [
  "/path/to/fut-extensions/github",
  "/path/to/fut-extensions/linear",
]
```

Then show the tokens somewhere in your UI, for example in a sidebar row:

```toml
right = [
  { token = "workspace.extension.linear.status_icon", suffix = " " },
  { token = "workspace.extension.linear.issue", suffix = " " },
]
detail = [
  { token = "workspace.extension.github.pr", suffix = " " },
  { token = "workspace.extension.github.state", suffix = " " },
  { token = "workspace.extension.github.checks" },
]
```

## Keeping statuses fresh

The extensions publish as soon as a workspace is created, but fut has no timer
or branch-change hook. `launchd/install github linear` runs
each `bin/publish` every 60 seconds (it does nothing while fut isn't running);
`launchd/uninstall` removes the jobs. Logs go to `~/Library/Logs/fut-extensions/`.

The jobs use your current `PATH` to find `fut` and `gh`. If that PATH contains
versioned directories, set `FUT_EXTENSIONS_PATH` to something stable instead,
such as `"$HOME/.local/share/mise/shims:/opt/homebrew/bin:/usr/bin:/bin"`.
`FUT_EXTENSIONS_LABEL_PREFIX` changes the launchd label prefix (default
`local.fut-extensions`).

## Configuration

Both extensions read optional settings from fut's `config.toml`, or a
workspace's `.fut/config.toml` for the `refresh` command:

```toml
[extension.github]
default_branches = ["main", "master"]   # branches that never show a PR

[extension.linear]
default_branches = ["main", "master"]
keychain_service = "fut-linear-api-key"
```

## What they run and store

- `github` runs `gh pr list` in each workspace's worktree; `linear` calls
  Linear's GraphQL API with the key from `security find-generic-password`.
- Both call `fut list` and `fut token publish`, and `bin/open` uses `open`.
- Each workspace's PR or issue URL is cached in
  `~/Library/Caches/fut-extensions/<id>/` for the `open` command.

## Development

The extensions are written in Go. Each extension directory holds its
`main.go` and tests next to its manifest, and `internal/fut` holds the fut API
code they share, compiled into each binary so every package still stands alone.
`bin/publish` is a build output, so it's ignored on `main`.

With [mise](https://mise.jdx.dev), `mise install` fetches the pinned Go,
golangci-lint and shellcheck, then:

- `mise run build` builds each `bin/publish` for Apple silicon
- `mise run test` runs the Go tests
- `mise run lint` checks Go with golangci-lint and shell scripts with shellcheck
- `mise run format` formats Go

Check a built package with `fut extension validate github`.

## Releasing

Push a tag such as `v0.3.0`. The release workflow lints, tests and builds that
commit, commits the binaries on top, tags the result `v0.3.0-build` and moves
the `release` branch to it. Bump `version` in both manifests first.
