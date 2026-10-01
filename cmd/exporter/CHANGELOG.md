# Changelog

All notable changes to this component are documented here.

## [0.5.1] - 2026-10-01

### Fixed

- **exporter**: stop TestRunGracefulShutdown racing srv.Shutdown (`2582ee1`)
- **exporter**: satisfy errcheck and fix a skipped signal-stop deferral (`6fa4f35`)

## [0.1.1] - 2026-09-30

_No notable changes._

## [0.1.0] - 2026-07-30

### Added

- **exporter**: add the resource metrics collector (`85c18f2`)
- **exporter**: expose Prometheus metrics (`97ea0e9`)
- **repo**: select the state backend from configuration (`f83de00`)
