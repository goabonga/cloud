# Contributing to infrastructure

Thanks for taking the time to contribute. This document is the short version of
how to propose a change and what the project expects in return.

## Code of Conduct

Participation in this project is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md). By contributing you agree to abide by its
terms.

## Repository layout

This is a single Go module with one published binary per `cmd/<name>/`
directory. Each binary is an independently versioned
[multicz](https://github.com/goabonga/multicz) component; its version lives in
`cmd/<name>/version.go`. Shared code lives under `internal/`.

```
infrastructure/
├── cmd/                # one main package per published component
├── internal/           # shared, non-published packages
├── test/integration/   # privileged, build-tagged integration suite
├── docs/               # Zensical documentation site
├── multicz.toml        # per-component versioning and release config
└── zensical.toml       # documentation site config
```

## Development setup

```bash
git clone https://github.com/goabonga/infrastructure.git
cd infrastructure

# Build everything and run the unit tests.
make build
make test

# Install the pre-commit and commit-msg hooks (runs gofmt, golangci-lint,
# go vet, SPDX header checks and Conventional Commits validation).
pre-commit install
```

Requires Go 1.25+, `golangci-lint`, and `uv` (used to run `multicz` and
`zensical` without a global install).

## Running checks

```bash
make fmt        # gofmt
make vet        # go vet (incl. integration build tag)
make lint       # golangci-lint
make test       # go test -race with coverage
make license-check
make release-validate   # multicz validate --strict
```

Run them in that order before pushing (`make check` chains fmt, vet and test).
The integration suite needs a privileged Linux host:

```bash
make test-integration   # requires root + CAP_NET_ADMIN
```

## License headers

Every Go, shell, YAML and TOML file must start with the two-line SPDX header
(Go uses `//`, the rest use `#`):

```
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
```

```bash
make license        # add missing headers in place
make license-check  # fail if any are missing
```

## Branching and pull requests

1. Fork the repository and create a topic branch from `main`.
2. Keep commits small, focused and atomic - one logical concern per commit.
3. Open a pull request against `main`. CI runs lint, vet, build, test, the
   security scans and the integration suite.
4. Reviews target correctness, scope and adherence to the conventional-commits
   contract; please do not bundle unrelated changes.

## Commit messages

Commit messages MUST follow
[Conventional Commits](https://www.conventionalcommits.org/). They drive the
version bumps and changelogs computed by
[multicz](https://github.com/goabonga/multicz). Use the component scope when the
change targets a single component.

| Type | Effect on version | Use it for |
| --- | --- | --- |
| `feat` | minor | new user-facing capability |
| `fix` | patch | bug fix |
| `perf` | patch | performance improvement |
| `deprecate` | minor | announce an upcoming removal (raises a deprecation notice); routed to `### Deprecated` |
| `remove` | minor | perform the removal after the n+2 window; routed to `### Removed` |
| `refactor`, `docs`, `test`, `chore`, `ci`, `build`, `style` | none | maintenance, no release |
| `feat!` / `BREAKING CHANGE:` | major | incompatible change bypassing the deprecation cycle |

Scopes: `api`, `controller`, `agent`, `cli`, `provider`, `idp`, `exporter`,
`core` (shared `internal/`), or `ci` / `docs` / `repo` for repo-level work.

Examples:

```
feat(api): add VPC list endpoint
fix(agent): release the bridge lock on reconcile error
docs(provider): document the dns_zone resource
```

Do not append `Co-Authored-By` trailers; the release workflow expects a single
authored release commit per push.

## Releasing

Releases are fully automated. On every push to `main`, the workflow runs
`multicz bump --commit --tag --push` and then publishes each bumped component to
GitHub Releases. Maintainers do not bump versions, edit changelogs or create
tags by hand.

## Reporting bugs and asking for features

Please open a GitHub issue. For security-sensitive reports, follow
[SECURITY.md](SECURITY.md) instead of the public tracker.
