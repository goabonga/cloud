# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

SHELL := /bin/bash
MULTICZ := uv tool run --with multicz-go-deps-plugin multicz
# Build and run Go tools with the toolchain go.mod pins, as CI does, so a tool
# that needs a newer Go fails here too instead of only in CI.
export GOTOOLCHAIN := $(shell sed -n 's/^toolchain //p' go.mod)
# Security tools, pinned; Dependabot does not track them, bump them by hand.
ZIZMOR := uv tool run zizmor==1.30.1
GITLEAKS := go run github.com/zricethezav/gitleaks/v8@v8.30.1
BANDIT := uv tool run bandit==1.9.4
OSV_SCANNER := go run github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.5.0
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.8.0
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

.PHONY: check build scripts-check scripts-security bandit osv-scripts docs icons license-check release-plan \
	release-validate go-test go-check go-security go-mod-verify govulncheck golangci-lint osv-go audit zizmor gitleaks

check: license-check release-validate audit scripts-check scripts-security go-check go-security
	python3 -c 'import tomllib; tomllib.load(open("zensical.toml", "rb"))'

build:
	go build -trimpath -o bin/cloud ./cmd/cli
	go build -trimpath -o bin/cloud-svc ./cmd/svc

scripts-check:
	python3 -m compileall -q scripts -x '/\.venv/'
	uv run --project scripts --locked pytest

scripts-security: bandit osv-scripts

bandit:
	$(BANDIT) -c scripts/pyproject.toml -r scripts -x scripts/tests,scripts/.venv

osv-scripts:
	$(OSV_SCANNER) scan source --lockfile scripts/uv.lock

audit: zizmor gitleaks

# Online audits (known-vulnerable or impostor actions) need GH_TOKEN; without
# it zizmor runs its offline audits only.
zizmor:
	$(ZIZMOR) $(if $(GH_TOKEN),,--offline) .github/workflows

# Scans the commits in GITLEAKS_RANGE (CI: those the pull request or push
# brings), or the current branch's history; never other branches.
gitleaks:
	$(GITLEAKS) git --redact --no-banner --log-opts="$(or $(GITLEAKS_RANGE),HEAD)" .

docs:
	uv tool run zensical==0.0.67 build --clean

icons:
	python3 scripts/regen_icons.py

release-plan:
	$(MULTICZ) plan

release-validate:
	$(MULTICZ) validate --strict

license-check:
	python3 scripts/add_license_header.py --path cmd --types go --check
	python3 scripts/add_license_header.py --path internal --types go --check
	python3 scripts/add_license_header.py --path scripts --types py,toml --check
	python3 scripts/add_license_header.py --path .github --types yml,yaml,toml --check

go-test:
	go test -race -count=1 ./cmd/... ./internal/...

go-check: go-test
	go vet ./cmd/... ./internal/...
	go build ./cmd/...
	go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 ./cmd/... ./internal/...

go-security: go-mod-verify govulncheck golangci-lint osv-go

go-mod-verify:
	go mod verify

govulncheck:
	$(GOVULNCHECK) ./...

golangci-lint:
	$(GOLANGCI_LINT) run ./...

osv-go:
	$(OSV_SCANNER) scan source --lockfile go.mod
