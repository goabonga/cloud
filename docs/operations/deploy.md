# Deployment

This page covers running the components on real hosts: a single dev host, the
Debian packages, and a libvirt-based multi-host test cluster driven by Ansible.

## Single host

Run `infra-api` with a file state backend for development:

```bash
infra-api
```

## Debian packages

Each component ships as a `.deb`. Build them locally:

```bash
make deb VERSION=1.0.0           # all components, amd64
ARCH=arm64 ./packaging/build-debs.sh 1.0.0 infra-api   # one component, arm64
```

The CI release publishes raw binaries and checksums. Build the `.deb` locally when
a package deployment is required. Installing a service package
drops the binary in `/usr/local/bin` and a systemd unit under
`/lib/systemd/system`:

```bash
sudo dpkg -i infra-api_1.0.0_amd64.deb
sudo systemctl enable --now infra-api
```

## systemd

The long-running components ship systemd units under `deploy/systemd/`
(`infra-api`, `infra-controller-manager`, `infra-agent`, `infra-exporter`,
`infra-idp`, `infra-www`). State lives in `/var/lib/infra` (provisioned via
`StateDirectory`); `infra-agent` runs with `CAP_NET_ADMIN` to manage bridges
and iptables.

## Observability

`infra-exporter` exposes Prometheus metrics. `deploy/ansible/roles/monitoring`
installs a native Prometheus + Grafana stack (no Docker) alongside the
control plane - see the multi-host section below.

## Multi-host (libvirt + Ansible)

`deploy/ansible/` provisions a libvirt/KVM test cluster from an Ubuntu 24.04
cloud image and cloud-init, installs the `.deb` packages and starts the
services. A control host runs the API, the IdP, the dashboard, Prometheus,
Grafana and Terraform with the local provider; two agent hosts run the agent
and a leader-elected controller-manager; every host runs an etcd member.

```bash
make deb VERSION=0.1.0 && make build-provider
cd deploy/ansible
ansible-playbook -K create-vms.yml             # provision the VMs
ansible-playbook -K site.yml                   # install packages and start services
ansible-playbook verify.yml -e failover=true   # check the stack, drill a failover
ansible-playbook -K destroy-vms.yml
```

See `deploy/ansible/README.md` for prerequisites and what the failover drill
covers. The architecture [overview](../architecture/overview.md) describes the
state backends.


## Management transport TLS

The Ansible inventory provisions a dedicated management CA locally under
`{{ cred_dir }}/management-pki`. Its signing key remains on the deployment
controller. Each infrastructure host receives its own ECDSA certificate and key,
with server/client authentication usages and SANs for its inventory name and
`ansible_host`. All management nodes are trusted cluster participants.

etcd client and peer listeners use HTTPS with mandatory client certificates and
TLS 1.3. Applications use `GOA_MANAGEMENT_TLS_CA`, `GOA_MANAGEMENT_TLS_CERT` and
`GOA_MANAGEMENT_TLS_KEY`, pointing at `/etc/infra-mtls/{ca.crt,node.crt,node.key}`.
The application key is root-owned and readable by the `infra` group; etcd has a
separate protected copy. Remote etcd endpoints require complete credentials;
explicit `http://` endpoints are rejected when TLS is configured. Only loopback
etcd may run without TLS for local development. API/IdP HTTP ingress remains a
separate concern and must use the deployment's existing ingress TLS policy.

For a new installation, run the normal site playbook. An existing plaintext
etcd cluster needs a planned migration before applying these service templates:

1. Take and verify an etcd snapshot and schedule maintenance. Provision node
   certificates without restarting the cluster; verify SANs against advertised
   addresses and retain the existing membership/data directories.
2. Following etcd's TLS migration procedure, update each member's advertised
   peer URL to HTTPS with `etcdctl member update <member-id>
   --peer-urls=https://<address>:2380`. Coordinate listener changes while preserving
   quorum; do not bootstrap a new cluster over existing data. A staged dual
   listener transition needs operator-managed intermediate configurations.
3. Switch client URLs and all application credentials together. Verify etcd
   endpoint health using `--cacert`, `--cert` and `--key`, then restart agents and
   controllers. Disable disk replication explicitly during a staged application
   upgrade if certificates are not yet ready.

This playbook does not automatically migrate existing etcd membership, and it
has not been run against your machines as part of this code change.

Leaf certificates last 397 days; the management root lasts ten years. Monitor
expiry. To renew a leaf, remove its generated `.crt` and rerun credential
provisioning and deployment while preserving the CA. Environment templates carry
an identity fingerprint so a changed certificate triggers application restarts;
etcd certificate copies notify its restart handler. If advertised addresses
change, regenerate the corresponding CSR and certificate as well. Root rotation
requires a staged trust bundle containing both roots, reissuance of every node
identity, and removal of the old root only after all peers have migrated. No
automatic certificate renewal or root rotation is implemented.
