# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

# Shared component metadata for every packaging format (.deb, Arch). Sourced,
# not executed - declares associative arrays only, so a format-specific build
# script can lay them out however that format needs without the two formats'
# descriptions drifting apart.

# One-line summaries and extended descriptions, keyed by package. Debian's
# lintian treats an empty extended description as an error, and a package
# with no prose is unreviewable anyway - this is where an operator finds out
# what a daemon on their host actually does, whichever packaging format they
# installed it from.
declare -A SUMMARY=(
  [infra]="command-line client for the infra control plane"
  [infra-api]="infra control-plane API server"
  [infra-agent]="infra per-host agent for kernel reconciliation"
  [infra-controller-manager]="infra cluster reconcilers and scheduler"
  [terraform-provider-infra]="Terraform provider for infra resources"
  [infra-exporter]="Prometheus exporter for infra cluster state"
  [infra-idp]="infra identity provider and JWT issuer"
  [infra-container-init]="PID 1 for infra-managed OCI containers"
  [infra-www]="infra web dashboard (embedded SPA + API reverse proxy)"
)
declare -A DESCRIPTION=(
  [infra]="Declarative CLI for applying and inspecting infra resources: VPCs,
ACL policies, secrets and SSL certificate authorities."
  [infra-api]="Serves the declarative resource API backed either by a local
state directory or, for high availability, by PostgreSQL."
  [infra-agent]="Reconciles the desired cluster state onto Linux primitives on
each host: network namespaces, nftables rules, cgroups v2, dm-crypt volumes
and OCI containers. Requires root and CAP_NET_ADMIN."
  [infra-controller-manager]="Runs the resource reconcilers and the binpack
and spread schedulers under leader election."
  [terraform-provider-infra]="Manages infra resources from Terraform. Install
under the Terraform plugin directory rather than invoking it directly."
  [infra-exporter]="Exposes cluster resource counts and reconciliation state
as Prometheus metrics on /metrics."
  [infra-idp]="Issues short-lived ES256 JWTs and publishes the matching JWKS
for the control-plane API to verify."
  [infra-container-init]="Minimal init process for containers the agent
starts: reaps zombies and forwards signals to the workload."
  [infra-www]="Serves the Vite dashboard embedded in the binary and proxies
its API calls to infra-api."
)

# Further units a package ships next to its service, from deploy/systemd.
# infra-agent starts infra-netns@ instances itself, one per namespace it holds,
# an infra-lb@ instance in each that has listeners, and an infra-egress@
# instance in each whose internet gateway filters egress.
declare -A EXTRA_UNITS=(
  [infra-agent]="infra-netns@.service infra-lb@.service infra-egress@.service"
)

# Further binaries a package ships, as "cmd-dir:binary". infra-agent runs
# infra-lb (the load balancers' data plane) and infra-hypervisor (one
# process per micro-VM instance, spawned directly rather than through a
# systemd unit — see EXTRA_UNITS above, which infra-hypervisor has no
# entry in for that reason).
declare -A EXTRA_BINARIES=(
  [infra-agent]="lb:infra-lb hypervisor:infra-hypervisor"
)

# component -> "cmd-dir:binary:service-unit" (empty service = not a daemon)
declare -A COMPONENTS=(
  [infra]="cli:infra:"
  [infra-api]="api:infra-api:infra-api"
  [infra-agent]="agent:infra-agent:infra-agent"
  [infra-controller-manager]="controller-manager:infra-controller-manager:infra-controller-manager"
  [terraform-provider-infra]="provider:terraform-provider-infra:"
  [infra-exporter]="exporter:infra-exporter:infra-exporter"
  [infra-idp]="idp:infra-idp:infra-idp"
  [infra-container-init]="container-init:infra-container-init:"
  [infra-www]="www:infra-www:infra-www"
)
