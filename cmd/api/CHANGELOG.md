# Changelog

All notable changes to this component are documented here.

## [0.1.1] - 2026-09-30

### Fixed

- **core**: bump grpc to v1.83.2 to clear govulncheck (`3ef3cf6`)

## [0.1.0] - 2026-07-30

### Added

- **api**: add generic resource HTTP handler (`9c445af`)
- **api**: serve the API and wire the VPC resource (`2cf7c32`)
- **api**: soft-delete resources that carry finalizers (`7a8e368`)
- **api**: serve encrypted secrets over HTTP (`da5b49f`)
- **api**: wire optional secret encryption into the API (`a5afed3`)
- **api**: require authentication when configured (`588871e`)
- **api**: serve SSL CAs and certificate issuance (`2f63b5b`)
- **api**: verify JWT bearer tokens (`0ed6a74`)
- **api**: serve ACL policies (`5aa8e33`)
- **repo**: select the state backend from configuration (`f83de00`)
- **api**: serve the network and compute resources (`fbe7500`)
- **api**: serve the DNS, peering, load-balancer, WAF and node resources (`a5e4a9e`)
- **api**: add versioned secrets (`9ac9baf`)
- **api**: add CA-signed certificates (`5b4cef6`)

### Fixed

- **api**: set security response headers on both HTTP servers (`47f45f9`)
