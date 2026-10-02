# Getting started

## Prerequisites

- Go 1.25 or newer
- A Linux host (the data plane uses kernel primitives)
- [uv](https://docs.astral.sh/uv/) to run `multicz` and `zensical`

## Build

```bash
git clone https://github.com/goabonga/infrastructure.git
cd infrastructure
make build
```

Binaries are written to `./build`:

```bash
./build/infra-api
./build/infra
```

## Test

```bash
make test               # unit tests, race detector, coverage
make test-integration   # privileged kernel-level suite (root + CAP_NET_ADMIN)
```

## Next steps

- [Install on a host](installation.md)
- [Understand the architecture](../architecture/overview.md)
