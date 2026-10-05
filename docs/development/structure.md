# Project structure

cloud is a Go command-line client and daemon, with Python tooling for CI,
releases and documentation. This page describes where code lives, which
component owns it, and how the pieces depend on each other.

## Repository layout

| Path | Contents |
| --- | --- |
| `cmd/cli/` | Entry point of the `cloud` command. Only wires flags, signals and `internal/cli`. |
| `cmd/svc/` | Entry point of the `cloud-svc` daemon. Registers its handler with `internal/transport`. |
| `internal/cli/` | Command tree built with [cobra](https://github.com/spf13/cobra): `--version`, help, the `status` subcommand and `completion` scripts for bash, zsh, fish and PowerShell. |
| `internal/transport/` | Unix socket listener and client, HTTP serving with graceful shutdown, `/healthz`. |
| `scripts/` | Python project (`cloud-scripts`): CI detection, release, Dependabot rewrite, signing, licence headers. Has its own uv lockfile and pytest suite. |
| `docs/` | Source of the documentation site, built by Zensical. `development/` holds contributor pages. |
| `assets/cloud.svg` | Canonical logo. `make icons` derives `docs/cloud.svg` and `docs/favicon.ico` from it. |
| `.github/` | CI workflow, Dependabot configuration, issue and pull request templates. |
| `multicz.toml` | Release components: their paths, version files and changelogs. |
| `zensical.toml` | Site configuration, navigation and the version table read by the docs. |
| `Makefile` | Entry points for every local check and build. |

Go dependencies are pinned by `go.mod` and `go.sum`. The CLI uses `spf13/cobra`;
the daemon uses only the standard library.

## Components

Each component is versioned independently by multicz from Conventional Commits.
A change under a component's paths bumps that component.

| Component | Path | Produces | Version file |
| --- | --- | --- | --- |
| `cloud` | `cmd/cli`, `internal/cli` | `cloud` binary | `cmd/cli/version.go` |
| `cloud-svc` | `cmd/svc`, `internal/transport` | `cloud-svc` daemon | `cmd/svc/version.go` |
| `cloud-scripts` | `scripts/` | Python automation, not shipped | `scripts/pyproject.toml` |
| `cloud-docs` | `docs/`, `zensical.toml` | documentation site | `zensical.toml` |

`cloud-docs` depends on the other components, so a release also refreshes the
version table.

## Dependencies between packages

- `cmd/*` only assemble a process. Logic stays in `internal/`.
- `internal/cli` imports `internal/transport` to reach the daemon socket. A change
  in `internal/transport` therefore affects both `cloud` and `cloud-svc`.
- CI derives affected Go components from the Go import graph, so an internal
  change runs the checks of every binary that imports it.

## Runtime layout

The daemon and the CLI talk over a Unix socket, `$XDG_RUNTIME_DIR/cloud/svc.sock`,
or a per-user path under the temporary directory when `XDG_RUNTIME_DIR` is unset.
The socket directory is `0700` and the socket is `0600`, so only the owning user can
connect. Pass `--socket` to either command to use another path.

## Local development

```console
uv tool install multicz --with multicz-go-deps-plugin
go run ./cmd/svc &
go run ./cmd/cli --version
go run ./cmd/cli status
go run ./cmd/cli completion bash > /tmp/cloud.bash && source /tmp/cloud.bash
```

`cloud status` prints the daemon's `/healthz` response. It reports liveness only,
not whether dependencies are ready. The daemon removes a stale socket left by a
crash, refuses to start if another daemon is listening, and removes its socket on
SIGINT or SIGTERM after draining requests.

## Checks

| Command | Runs |
| --- | --- |
| `make build` | Builds the `cloud` and `cloud-svc` binaries into `bin/` |
| `make check` | Every check below, except `build`, `docs` and `icons` |
| `make go-test` | Unit tests of `cmd/` and `internal/` with `go test -race` |
| `make go-check` | `make go-test`, then `go vet`, build and gosec on `cmd/` and `internal/` |
| `make scripts-check` | Byte-compilation and pytest for `scripts/` |
| `make scripts-security` | bandit on `scripts/` and osv-scanner on `scripts/uv.lock` |
| `make go-security` | `go mod verify`, govulncheck, golangci-lint and osv-scanner on `go.mod` |
| `make audit` | zizmor on `.github/workflows` (offline without `GH_TOKEN`) and gitleaks on the history |
| `make license-check` | SPDX headers on Go, Python, TOML and YAML files |
| `make release-validate` | `multicz validate --strict` |
| `make docs` | Documentation site build |
| `make icons` | Regenerates the documentation logo and favicon |

Python tests use pytest functions and fixtures. Go tests cover HTTP routing, the
socket lifecycle, `cloud status` and graceful shutdown. See
[GitHub automation](github.md) for change detection, signing and releases.
