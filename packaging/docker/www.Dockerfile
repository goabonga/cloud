# syntax=docker/dockerfile:1

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Container image for infra-www, the dashboard server.
#
# Separate from packaging/docker/Dockerfile because this is the one component
# with a two-toolchain build: cmd/www embeds the built SPA through
# `//go:embed all:dist`, so the Vite bundle has to exist before the Go
# compiler runs. The committed cmd/www/dist/.gitkeep only keeps the embed
# compiling on a clean checkout - it produces a server that serves nothing.
#
# Build context is the repository root:
#
#   docker build -f packaging/docker/www.Dockerfile .

ARG NODE_IMAGE=node:22-bookworm@sha256:5647be709086c696ff32edaaf1c70cd26d1da6ab2b39c32f3c7b4c4a31957e37
ARG GO_IMAGE=golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

FROM ${NODE_IMAGE} AS spa
WORKDIR /spa
# npm ci needs the lockfile and manifest only; copying them first keeps the
# install layer warm across source-only edits.
COPY www/package.json www/package-lock.json ./
RUN npm ci
COPY www/ ./
RUN npm run build

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# Replace the .gitkeep placeholder with the real bundle before compiling, so
# the embedded filesystem is the one the SPA build just produced.
COPY --from=spa /spa/dist/ ./cmd/www/dist/
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -ldflags='-s -w' -o /out/app ./cmd/www/

FROM ${RUNTIME_IMAGE}
ARG VERSION=0.0.0
LABEL org.opencontainers.image.title="infra-www" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/goabonga/infrastructure" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Chris <goabonga@pm.me>"
COPY --from=build /out/app /usr/local/bin/app
USER 65532:65532
EXPOSE 8088
ENTRYPOINT ["/usr/local/bin/app"]
