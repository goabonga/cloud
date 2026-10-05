# Contributing to cloud

Thanks for taking the time to contribute. This document is the short version of
how to propose a change and what the project expects in return.

## Code of Conduct

Participation in this project is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md). By contributing you agree to abide by its
terms.

## Repository layout

cloud is a single Go module, a command-line client and a daemon, with Python
tooling for CI, releases and documentation. Each part is an independently
versioned [multicz](https://github.com/goabonga/multicz) component:

| Component | Path | Produces | Version file |
| --- | --- | --- | --- |
| `cloud` | `cmd/cli`, `internal/cli` | `cloud` binary | `cmd/cli/version.go` |
| `cloud-svc` | `cmd/svc`, `internal/transport` | `cloud-svc` daemon | `cmd/svc/version.go` |
| `cloud-scripts` | `scripts/` | CI and release automation, not shipped | `scripts/pyproject.toml` |
| `cloud-docs` | `docs/`, `zensical.toml` | documentation site | `zensical.toml` |

```
cloud/
├── cmd/            # one main package per binary: cli, svc
├── internal/       # shared packages: cli, transport
├── scripts/        # Python project (cloud-scripts) with its own tests
├── docs/           # Zensical documentation site
├── assets/         # canonical logo
├── multicz.toml    # per-component versioning and release config
└── zensical.toml   # documentation site config
```

[docs/development/structure.md](docs/development/structure.md) describes how the
packages depend on each other.

## Development setup

```bash
git clone https://github.com/goabonga/cloud.git
cd cloud
uv tool install 'multicz>=1.8' --with 'multicz-go-deps-plugin>=0.4'

make build   # the cloud and cloud-svc binaries, into bin/
make check   # every check CI runs
```

Requires Go 1.26 (the toolchain is pinned in `go.mod`), [uv](https://docs.astral.sh/uv/)
and git configured to sign commits. The linters and scanners are pinned in the
`Makefile` and run through `uv tool run` or `go run`, without a global install.

## Running checks

| Command | Runs |
| --- | --- |
| `make check` | every check below |
| `make go-check` | `go test -race`, `go vet`, build and gosec on `cmd/` and `internal/` |
| `make go-security` | `go mod verify`, govulncheck, golangci-lint and osv-scanner on the module |
| `make scripts-check` | ruff formatting, byte-compilation and pytest for `scripts/` |
| `make scripts-security` | bandit and osv-scanner on `scripts/` |
| `make audit` | zizmor on the workflows and gitleaks on the commits |
| `make license-check` | SPDX headers |
| `make release-validate` | `multicz validate --strict` |
| `make docs` | builds the documentation site |

Run `make check` and `make docs` before pushing; the pull request template asks
for both.

## License headers

Every Go file under `cmd/` and `internal/`, Python and TOML file under `scripts/`,
and YAML and TOML file under `.github/` starts with the two-line SPDX header (Go
uses `//`, the others `#`):

```
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
```

`make license-check` fails when one is missing;
`python3 scripts/add_license_header.py --path <dir> --types <extensions>` adds them.

## Branching and pull requests

1. Create a topic branch from `develop`, where work lands; a fix that must ship
   before the next release may target `main`.
2. Keep commits small, focused and atomic: one logical change per commit.
3. Open a pull request with the template. Tick the local checks under
   `Validation` and the `Checklist`; CI ticks the `Pipeline` boxes itself.
4. Every commit must be signed and verified by GitHub, or `check commit
   signatures` rejects the pull request. GitHub's "Update branch" leaves unsigned
   commits; on branches of this repository, the `resign` workflow re-signs them.
5. Security scanners report their findings as review threads. A finding that is
   accepted rather than fixed gets a dated exception: a maintainer replies
   `/exception <days>d <reason>` to its thread (at most 90 days).
6. A maintainer merges by adding the `auto-merge` label: the signed merge queue
   rebases, re-signs and fast-forwards, so history stays linear. Nothing is merged
   by hand. Pull requests from forks get CI, but only branches of this repository go
   through the queue; a maintainer takes a fork contribution over on a branch here.

Reviews target correctness, scope and adherence to the Conventional Commits
contract; please do not bundle unrelated changes.
[docs/development/github.md](docs/development/github.md) describes every workflow.

## Commit messages

Commit messages MUST follow
[Conventional Commits](https://www.conventionalcommits.org/). They drive the
version bumps and changelogs computed by multicz. Use the scope of the part the
commit touches: `cli`, `svc`, `transport`, `scripts`, `docs` or `deps`; `release`
is reserved for release commits.

| Type | Effect on version |
| --- | --- |
| `feat` | minor |
| `fix`, `perf` | patch |
| `feat!` / `fix!` with a `BREAKING CHANGE:` footer | major |
| `ci`, `refactor`, `test`, `build`, `revert` | patch of the component whose paths they touch |
| `docs`, `style` | patch of `cloud-docs` and `cloud-scripts` only |
| `chore` | none |

Examples:

```
feat(cli): add the status subcommand
fix(transport): remove a stale socket before listening
docs(docs): document the socket permissions
```

Do not append `Co-Authored-By` trailers.

## Releasing

Releases are automated. When `develop` is ready, a maintainer opens a
**Release promotion** issue: the `promotion` workflow adds the release plan and
opens the pull request from `develop` into `main`. Once it is merged through the
queue, CI on `main` runs `multicz bump`, pushes the signed release commit and
tags, publishes each bumped component to GitHub Releases with the documentation
site, then brings `develop` up to `main`. Maintainers do not bump versions, edit
changelogs or create tags by hand.

## Reporting bugs and asking for features

Open a GitHub issue with the **Bug report** or **Feature request** form. For
security-sensitive reports, follow [SECURITY.md](SECURITY.md) instead of the
public tracker.
