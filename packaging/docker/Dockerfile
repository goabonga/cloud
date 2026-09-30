# syntax=docker/dockerfile:1

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Container image for the network daemons that carry no host-kernel
# dependency: infra-api, infra-idp, infra-exporter and
# infra-controller-manager. One parameterised Dockerfile rather than four
# near-identical copies - every one of them is the same static Go binary on
# the same base, so a per-component file would only mean a base-image bump
# had to be applied four times and could be applied inconsistently.
#
# Deliberately NOT covered here: infra-agent and infra-container-init drive
# netns, nftables, cgroups v2 and dm-crypt on the host and are packaged as
# .deb + systemd units instead; infra (CLI) and terraform-provider-infra are
# binaries a user runs, not services.
#
# Build context is the repository root - this is a single Go module, so the
# build needs go.mod, cmd/ and internal/ together:
#
#   docker build -f packaging/docker/Dockerfile \
#     --build-arg CMD_DIR=api --build-arg COMPONENT=infra-api .

ARG GO_IMAGE=golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

FROM ${GO_IMAGE} AS build
WORKDIR /src
# Dependencies first, so a source-only change reuses the download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# TARGETOS / TARGETARCH are supplied by buildx per requested platform;
# defaulted so a plain `docker build` still works.
ARG CMD_DIR
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# CGO_ENABLED=0 is what makes the distroless static base viable: no libc to
# carry, nothing to resolve at runtime.
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -ldflags='-s -w' -o /out/app "./cmd/${CMD_DIR}/"

FROM ${RUNTIME_IMAGE}
ARG COMPONENT
ARG VERSION=0.0.0
LABEL org.opencontainers.image.title="${COMPONENT}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/goabonga/infrastructure" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Chris <goabonga@pm.me>"
COPY --from=build /out/app /usr/local/bin/app
# Numeric uid/gid of the distroless nonroot user, so runAsNonRoot can
# verify it without resolving a name. State is written under /var/lib
# when GOA_STATE_DIR points there; mount it as a volume with matching
# ownership, or point the daemon at Postgres via GOA_STATE_DSN.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/app"]
