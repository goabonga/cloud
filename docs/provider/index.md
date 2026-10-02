# Terraform provider

`terraform-provider-infra` manages infrastructure resources through the
`infra-api` control plane, so the same resource model is available as code.

```hcl
terraform {
  required_providers {
    infra = {
      source = "goabonga/infra"
    }
  }
}

provider "infra" {
  endpoint = "http://[::1]:8080"
}

resource "infra_vpc" "prod" {
  name = "prod"
  cidr = "10.0.0.0/16"
}
```

## Project scope

Set `project_id` in the provider block, or set `GOA_PROJECT_ID`, to place
new resources in an existing project. The authenticated identity must have
write permission on that project. An explicit empty `project_id` overrides
the environment variable and creates resources without a project.

Updates preserve the resource's existing owner, project and version metadata.
Changing the provider's project does not move existing resources: an update
with a different configured project returns an error.

## Deletion

When the API accepts an asynchronous deletion, Terraform waits until the
resource returns HTTP 404. The wait is bounded by five minutes or the caller's
earlier deadline. A timeout, cancellation or polling error is reported as a
Terraform diagnostic so the resource remains in state for a later retry.
Immediate HTTP 204 and already absent resources complete without polling.

## Resources

The provider manages the full network and compute topology:

| Resource | Purpose |
| --- | --- |
| `infra_vpc` | Virtual private cloud (bridge fabric). |
| `infra_subnet` | Subnet within a VPC. |
| `infra_security_group` | Firewall group. |
| `infra_security_group_rule` | Ingress/egress rule. |
| `infra_ip_address` | Reserved IP address. |
| `infra_igw` | Internet gateway; `egress_proxy` filters the VPC's egress to `allowed_domains` and `allowed_addresses`. |
| `infra_route` | Static route. |
| `infra_kms_keyring` | KMS keyring. |
| `infra_kms_key` | KMS key (used to encrypt disks). |
| `infra_disk` | Persistent disk; `kms_key_id` encrypts it. |
| `infra_disk_file` | File injected into a disk: literal content, or a part of an `infra_ssl_cert`. |
| `infra_compute` | Compute instance with attached disks. |
| `infra_ssl_ca` | Certificate authority, trusted by the instances of its `vpc_ids`. |
| `infra_ssl_cert` | Certificate signed by a CA, `public-root` included. |
| `infra_dns_zone` | DNS zone. |
| `infra_dns_record` | DNS record within a zone. |
| `infra_peering` | Peering between two VPCs. |
| `infra_load_balancer` | Load balancer: a VIP and public address, with a layer-4 `port` or listeners. |
| `infra_lb_backend` | Compute backend attached to a load balancer. |
| `infra_lb_target_group` | Pool of targets with their protocol, health check and, with https, `backend_ca_id` and `server_name`. |
| `infra_lb_target` | Compute instance in a target group. |
| `infra_lb_listener` | http, https, tls or tcp port of a load balancer, routing to target groups. |
| `infra_waf_policy` | Web-application-firewall policy. |
| `infra_waf_rule` | Rule within a WAF policy. |
| `infra_node` | Host registered with the control plane. |
| `infra_node_pool` | Pool of nodes selected by label. |

## Example: a VPC with an encrypted-disk compute instance

```hcl
resource "infra_vpc" "prod" {
  cidr = "10.0.0.0/16"
}

resource "infra_subnet" "pub" {
  vpc_id = infra_vpc.prod.id
  cidr   = "10.0.1.0/24"
  type   = "public"
}

resource "infra_security_group" "web" {
  vpc_id = infra_vpc.prod.id
  name   = "web"
}

resource "infra_security_group_rule" "https" {
  security_group_id = infra_security_group.web.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 443
}

resource "infra_kms_keyring" "ring" {
  name = "prod"
}

resource "infra_kms_key" "disk" {
  keyring_id = infra_kms_keyring.ring.id
  name       = "disk-encryption"
}

resource "infra_disk" "data" {
  size_mb    = 1024
  kms_key_id = infra_kms_key.disk.id
}

resource "infra_compute" "web01" {
  subnet_id         = infra_subnet.pub.id
  security_group_id = infra_security_group.web.id
  image             = "nginx:latest"
  cpu               = 1
  memory_mb         = 512
  disks = [{
    disk_id    = infra_disk.data.id
    mount_path = "/var/data"
  }]
}
```

Per-attribute reference pages are generated from the schema by `make docs-gen`
(tfplugindocs).
