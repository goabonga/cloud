# Versions

Each component below is released independently, versioned and tagged by
[multicz](https://github.com/goabonga/multicz) from
[Conventional Commits](https://www.conventionalcommits.org/) - see
[Stability and deprecation](stability.md) for how a commit type maps to a
bump. The numbers here are rewritten on every release; nothing on this page
is hand-maintained.

## Control plane

| Component | Version | Changelog |
| --- | --- | --- |
| `infra-api` | {{ config.extra.versions.infra_api }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/api/CHANGELOG.md) |
| `infra-controller-manager` | {{ config.extra.versions.infra_controller_manager }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/controller-manager/CHANGELOG.md) |

## Data plane

| Component | Version | Changelog |
| --- | --- | --- |
| `infra-agent` | {{ config.extra.versions.infra_agent }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/agent/CHANGELOG.md) |

## Identity

| Component | Version | Changelog |
| --- | --- | --- |
| `infra-idp` | {{ config.extra.versions.infra_idp }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/idp/CHANGELOG.md) |

## Observability

| Component | Version | Changelog |
| --- | --- | --- |
| `infra-exporter` | {{ config.extra.versions.infra_exporter }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/exporter/CHANGELOG.md) |

## Clients

| Component | Version | Changelog |
| --- | --- | --- |
| `infra` (CLI) | {{ config.extra.versions.infra }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/cli/CHANGELOG.md) |
| `terraform-provider-infra` | {{ config.extra.versions.terraform_provider_infra }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/provider/CHANGELOG.md) |

## Web

| Component | Version | Changelog |
| --- | --- | --- |
| `infra-www` | {{ config.extra.versions.infra_www }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/www/CHANGELOG.md) |
| `infra-spa` | {{ config.extra.versions.infra_spa }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/www/CHANGELOG.md) |

## Helpers

| Component | Version | Changelog |
| --- | --- | --- |
| `infra-container-init` | {{ config.extra.versions.infra_container_init }} | [CHANGELOG.md](https://github.com/goabonga/infrastructure/blob/main/cmd/container-init/CHANGELOG.md) |

## Downloads

Every component except `infra-spa` ships as a `.deb` and an Arch
`.pkg.tar.zst`, attached to its own
[GitHub Release](https://github.com/goabonga/infrastructure/releases) tagged
`<component>-v<version>`. Arch packages tracking a release automatically are
also pushed to the [AUR](https://aur.archlinux.org/) when configured.
