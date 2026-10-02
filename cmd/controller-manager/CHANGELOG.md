# Changelog

All notable changes to this component are documented here.

## [0.9.3] - 2026-10-02

### Fixed

- allocate function slots atomically and retire failed starts (`94ee638`)

## [0.9.0] - 2026-10-01

### Added

- **controller**: schedule async disk replicas onto nodes (`2524657`)

## [0.7.0] - 2026-10-01

### Added

- **controller**: schedule disks onto nodes (`4ae036d`)

## [0.1.1] - 2026-09-30

_No notable changes._

## [0.1.0] - 2026-07-30

### Added

- **controller**: add placement scheduler (`6fec871`)
- **controller**: add leader-election lease (`8cffbe6`)
- **controller**: add the controller manager framework (`7439315`)
- **controller**: add the VPC summary controller (`9bced4e`)
- **controller**: run controllers under leader election (`79983a7`)
- **repo**: select the state backend from configuration (`f83de00`)
- **controller**: schedule compute onto nodes (`d1a3802`)
- **controller**: run the scheduler in infra-controller-manager (`2e23ae8`)
- **controller**: place only onto recently-seen nodes (`5365afe`)
- **controller**: evict compute from stale or deleted nodes (`f13feaa`)
