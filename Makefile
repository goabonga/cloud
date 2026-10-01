# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

SHELL := /bin/bash
BUILD_DIR := build

# component dir -> output binary name
CLI_BIN := $(BUILD_DIR)/infra
API_BIN := $(BUILD_DIR)/infra-api
AGENT_BIN := $(BUILD_DIR)/infra-agent
CTRLMGR_BIN := $(BUILD_DIR)/infra-controller-manager
PROVIDER_BIN := $(BUILD_DIR)/terraform-provider-infra
EXPORTER_BIN := $(BUILD_DIR)/infra-exporter
IDP_BIN := $(BUILD_DIR)/infra-idp
CONTAINER_INIT_BIN := $(BUILD_DIR)/infra-container-init
LB_BIN := $(BUILD_DIR)/infra-lb
WWW_BIN := $(BUILD_DIR)/infra-www

# Run multicz / zensical through uv without a global install.
MULTICZ := uv tool run multicz
ZENSICAL := uv tool run zensical

.PHONY: help build build-cli build-api build-agent build-controller-manager \
        build-provider build-exporter build-idp build-container-init build-lb \
        frontend build-www \
        fmt vet lint test test-integration tidy check \
        license license-check \
        docs docs-gen docs-serve \
        deb release-plan release-validate \
        clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}'

# ── Build ──────────────────────────────────────────────────────────────────

build: build-cli build-api build-agent build-controller-manager build-provider \
       build-exporter build-idp build-container-init build-lb ## Build every component

build-cli: ## Build the CLI (infra)
	go build -o $(CLI_BIN) ./cmd/cli/

build-api: ## Build the API server
	go build -o $(API_BIN) ./cmd/api/

build-agent: ## Build the per-host agent
	go build -o $(AGENT_BIN) ./cmd/agent/

build-controller-manager: ## Build the controller-manager
	go build -o $(CTRLMGR_BIN) ./cmd/controller-manager/

build-provider: ## Build the Terraform provider
	go build -o $(PROVIDER_BIN) ./cmd/provider/

build-exporter: ## Build the Prometheus exporter (static)
	CGO_ENABLED=0 go build -ldflags='-s -w' -o $(EXPORTER_BIN) ./cmd/exporter/

build-idp: ## Build the identity provider
	go build -o $(IDP_BIN) ./cmd/idp/

build-container-init: ## Build the container-init helper (static)
	CGO_ENABLED=0 go build -ldflags='-s -w' -o $(CONTAINER_INIT_BIN) ./cmd/container-init/

build-lb: ## Build the load balancers' data plane (static)
	CGO_ENABLED=0 go build -ldflags='-s -w' -o $(LB_BIN) ./cmd/lb/

frontend: ## Build the SPA and stage it into the infra-www embed dir
	cd www && npm ci && npm run build
	find cmd/www/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r www/dist/. cmd/www/dist/

build-www: frontend ## Build the web dashboard server with the SPA embedded
	go build -o $(WWW_BIN) ./cmd/www/

# ── Quality ────────────────────────────────────────────────────────────────

fmt: ## Format all Go sources
	gofmt -w .

vet: ## Run go vet (incl. integration build tag)
	go vet ./...
	go vet -tags=integration ./...

lint: ## Run golangci-lint
	golangci-lint run

test: ## Run unit tests with the race detector and coverage
	go test -race -coverprofile=coverage.out ./...

test-integration: ## Run the privileged integration suite (needs root + CAP_NET_ADMIN)
	go test -tags=integration ./test/integration/...

tidy: ## Tidy the module graph
	go mod tidy

check: fmt vet test ## Local pre-push gate: format, vet, test

# ── License headers ────────────────────────────────────────────────────────

license: ## Add missing SPDX headers in place
	python scripts/add_license_header.py --path cmd --types go
	python scripts/add_license_header.py --path internal --types go
	python scripts/add_license_header.py --path test --types go
	python scripts/add_license_header.py --path scripts --types py,sh
	python scripts/add_license_header.py --path .github --types yml,yaml

license-check: ## Verify SPDX headers are present
	python scripts/add_license_header.py --path cmd --types go --check
	python scripts/add_license_header.py --path internal --types go --check
	python scripts/add_license_header.py --path test --types go --check
	python scripts/add_license_header.py --path scripts --types py,sh --check
	python scripts/add_license_header.py --path .github --types yml,yaml --check

# ── Documentation (Zensical) ───────────────────────────────────────────────

docs: ## Build the documentation site to site/
	$(ZENSICAL) build --clean

docs-gen: build-provider ## Generate Terraform Registry docs (requires tfplugindocs)
	@if command -v tfplugindocs >/dev/null 2>&1; then \
		tfplugindocs generate --provider-name infra --rendered-website-dir dist/registry-docs; \
		echo "registry docs written to dist/registry-docs"; \
	else \
		echo "tfplugindocs not installed; the Zensical provider docs under docs/provider/ are maintained by hand."; \
		echo "install: go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest"; \
	fi

docs-serve: ## Serve the documentation site locally
	$(ZENSICAL) serve

# ── Packaging ──────────────────────────────────────────────────────────────

VERSION ?= 0.0.0

deb: ## Build Debian packages into dist/ (VERSION=x.y.z, ARCH=amd64)
	./packaging/build-debs.sh $(VERSION)

# ── Release (multicz) ──────────────────────────────────────────────────────

release-validate: ## Validate the multicz configuration
	$(MULTICZ) validate --strict

release-plan: ## Preview the release plan against origin/main
	$(MULTICZ) plan

# ── Clean ──────────────────────────────────────────────────────────────────

clean: ## Remove build and docs artifacts
	rm -rf $(BUILD_DIR) dist/ site/ coverage.out
