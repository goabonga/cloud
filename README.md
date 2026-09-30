# infrastructure

[![CI](https://github.com/goabonga/infrastructure/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/goabonga/infrastructure/actions/workflows/ci.yml)
[![Codecov](https://img.shields.io/codecov/c/github/goabonga/infrastructure?logo=codecov)](https://codecov.io/gh/goabonga/infrastructure)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/goabonga/infrastructure/blob/main/LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg?logo=go)](https://go.dev/)

A declarative, Linux-native cloud control plane. It provisions VPCs, compute,
networking, DNS, KMS, secrets, SSL, WAF, ACL and load balancers using only
kernel primitives (network namespaces, bridges/VXLAN, iptables, cgroups v2,
dm-crypt, OCI images) - no Docker daemon, libvirt or OVS; DNS is served by the
agent itself.

Resources follow a Kubernetes-style `metadata` / `spec` / `status` model: a
control plane (API + controller-manager) records desired state and a per-host
agent reconciles it against the kernel. Clients are a CLI and a Terraform
provider.

## Documentation

The project site is published from `main` to GitHub Pages:
<https://goabonga.github.io/infrastructure/>.

## Components

The repository is a single Go module with one published binary per component,
each independently versioned and released by
[multicz](https://github.com/goabonga/multicz).

| Component | Role |
| --- | --- |
| `infra-api` | REST control plane: stores resource spec/status. |
| `infra-controller-manager` | Cluster scheduler and reconcilers. |
| `infra-agent` | Per-host reconciler: drives kernel primitives. |
| `infra` | Command-line client. |
| `terraform-provider-infra` | Terraform provider for the API. |
| `infra-idp` | Identity provider (OIDC / sessions). |
| `infra-exporter` | Prometheus exporter. |
| `infra-container-init` | Minimal container init helper. |

## Requirements

- Go 1.25+
- `golangci-lint` for linting
- [uv](https://docs.astral.sh/uv/) to run `multicz` and `zensical`

## Getting started

```bash
# Build every component into ./build.
make build

# Run the unit tests with the race detector and coverage.
make test

# Run a single component.
./build/infra-api
```

## Versioning and release

Each component owns its version, changelog and git tag
(`{component}-v{version}`). Versions are bumped from
[Conventional Commits](https://www.conventionalcommits.org/) by multicz, which
only touches components whose `paths` changed.

```bash
# Preview what would be released.
make release-plan

# Validate the release configuration.
make release-validate
```

## Contributing

See [CONTRIBUTING.md](https://github.com/goabonga/infrastructure/blob/main/CONTRIBUTING.md)
for the workflow, the commit-message convention, and the test/lint
expectations. By participating you agree to the
[Code of Conduct](https://github.com/goabonga/infrastructure/blob/main/CODE_OF_CONDUCT.md).

Security issues: please follow the disclosure process in
[SECURITY.md](https://github.com/goabonga/infrastructure/blob/main/SECURITY.md).

## License

Distributed under the
[MIT License](https://github.com/goabonga/infrastructure/blob/main/LICENSE).
