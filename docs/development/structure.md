# Project structure

The project provides five executable entry points and a browser application.
The API and identity platform expose process health.

| Entry point | Component | Current behavior |
| --- | --- | --- |
| `cmd/cli` | `cloud` | Command help and version |
| `cmd/api` | `cloud-api` | HTTP health endpoint on `127.0.0.1:8080` |
| `cmd/idp` | `cloud-identity-platform` | HTTP health endpoint on `127.0.0.1:8081` |
| `cmd/ssr` | `cloud-ssr` | Serves the built browser application on `127.0.0.1:8088` |
| `cmd/mgr` | `cloud-fleet` | Controller and agent modes with HTTP health endpoints |
| `www` | `cloud-www` | React/TypeScript application with Vite |
| `scripts` | `cloud-scripts` | Python project and GitHub automation |

## Run locally

Use Go with the toolchain declared in `go.mod`, Node.js 24.15 or later within the
supported ranges in `www/package.json`, uv and multicz with its Go dependency
plugin. The Go code currently uses only the standard library.

```console
uv tool install multicz --with multicz-go-deps-plugin
go run ./cmd/cli --help
go run ./cmd/cli --version
go run ./cmd/api --addr 127.0.0.1:8080
go run ./cmd/idp --addr 127.0.0.1:8081
go run ./cmd/mgr --version
go run ./cmd/mgr controller --addr 127.0.0.1:8090
go run ./cmd/mgr agent --addr 127.0.0.1:8091
```

Run the HTTP commands in separate terminals. `GET /healthz` returns the service,
version and process status. This endpoint reports liveness; it does not check
whether infrastructure resources are ready. The servers drain active requests
on SIGINT or SIGTERM and configure HTTP read, write and idle timeouts.

```console
npm --prefix www ci
npm --prefix www run dev
npm --prefix www run build
go run ./cmd/ssr --assets www/dist --addr 127.0.0.1:8088
```

The Vite development server serves the frontend during development. The Go web
server serves the production build and falls back to its entry point for browser
navigation. Missing assets, directory listings, hidden files and unimplemented
API or IDP paths return 404. It also exposes `GET /healthz` and supports
`--version` without requiring a frontend build.

## Fleet process base

`cmd/mgr` assembles `cloud-fleet`; `internal/fleet` selects its controller or agent
mode and uses the shared HTTP lifecycle. Both modes expose `GET /healthz`,
identifying the process as `cloud-fleet-controller` or `cloud-fleet-agent`.
Their default addresses are `127.0.0.1:8090` and `127.0.0.1:8091` respectively.
The root command and each mode support `--help` and `--version`; modes accept
`--addr`. Invalid modes, flags and unexpected arguments fail before startup.
This initial base provides process health and lifecycle. It does not schedule workloads or reconcile infrastructure resources.

## Code and checks

`cmd/` assembles each process. `internal/cli` owns the CLI flags,
`internal/transport` owns HTTP lifecycle and health, and `internal/ssr` owns serving
the JavaScript application. `internal/fleet` owns Fleet mode selection. Go import graphs determine which component checks
run after an internal package changes.

```console
make check
make go-check
make www-check
make scripts-check
python3 scripts/check_go.py cloud-api
```

Python tests use pytest functions and fixtures. Go tests cover HTTP routing,
startup validation and graceful shutdown; frontend tests render the implemented
application. The CI runs the applicable checks before any component release.
See [GitHub automation](github.md) for change detection, signing and releases.
