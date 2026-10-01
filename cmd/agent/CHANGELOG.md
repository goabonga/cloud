# Changelog

All notable changes to this component are documented here.

## [0.18.0] - 2026-10-01

### Added

- **core**: drive microvm's vm process via infra-hypervisor (`323d8ce`)

## [0.17.0] - 2026-10-01

### Added

- **agent**: preserve sparseness when cloning a microvm boot image (`e75ce33`)

### Fixed

- **agent**: keep the microvm guest's NIC named eth0 (`5c7ba76`)

## [0.16.0] - 2026-10-01

### Added

- **agent**: sync a disk's backing file onto its async replica (`6a94389`)
- **agent**: start the async disk replica reconciler in infra-agent (`01d0ce3`)

## [0.14.0] - 2026-10-01

### Added

- **agent**: start the replication transport in infra-agent (`e2cecbc`)

## [0.13.1] - 2026-10-01

### Fixed

- **agent**: skip disks not owned by the local node (`9c28d77`)

## [0.1.1] - 2026-09-30

_No notable changes._

## [0.1.0] - 2026-07-30

### Added

- **agent**: reconcile VPCs against Linux bridges (`71c3df5`)
- **agent**: run the reconcile loop from infra-agent (`4289adf`)
- **agent**: reconcile ACL policies into iptables (`67b9412`)
- **repo**: select the state backend from configuration (`f83de00`)
- **agent**: realize subnets and internet gateways on the host (`fa61187`)
- **agent**: reconcile subnets and gateways from infra-agent (`3955867`)
- **agent**: realize encrypted disks with dm-crypt (`e3c8ae2`)
- **agent**: reconcile disks from infra-agent (`7f94474`)
- **agent**: realize security groups as iptables chains (`12b9652`)
- **agent**: pull and extract OCI images for compute (`0c74e8f`)
- **agent**: realize compute instances as namespaced containers (`402959b`)
- **agent**: reconcile security groups and compute from infra-agent (`9738a73`)
- **agent**: realize VPC peering as a veth bridge link (`425f3ac`)
- **agent**: reconcile peerings from infra-agent (`4b1a5b8`)
- **agent**: realize WAF policies as iptables chains (`3817414`)
- **agent**: reconcile WAF policies from infra-agent (`3133ac9`)
- **agent**: realize DNS zones with a per-VPC dnsmasq (`a0388b2`)
- **agent**: reconcile DNS from infra-agent (`ea2fc08`)
- **agent**: realize load balancers with IPVS (`8e16af3`)
- **agent**: reconcile load balancers from infra-agent (`e304464`)
- **agent**: realize only compute scheduled to this node (`56c2c75`)
- **agent**: heartbeat the agent's node (`40f485c`)
