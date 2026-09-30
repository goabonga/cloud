# A demo topology provisioned against the deployed control plane:
#   vpc -> subnet -> internet gateway + route -> security group + rules
#   -> KMS-encrypted disk -> two compute instances, scheduled onto the agent
#   node pool -> a layer-4 load balancer in front of both.

# The agent hosts are registered as nodes by Ansible with the label role=agent;
# this pool selects them so the scheduler can place compute.
resource "infra_node_pool" "workers" {
  name = "workers"
  node_selector = {
    role = "agent"
  }
}

resource "infra_vpc" "demo" {
  cidr = "10.20.0.0/16"
}

resource "infra_subnet" "app" {
  vpc_id = infra_vpc.demo.id
  cidr   = "10.20.1.0/24"
  type   = "private"
}

resource "infra_igw" "demo" {
  vpc_id = infra_vpc.demo.id
}

resource "infra_route" "default" {
  vpc_id      = infra_vpc.demo.id
  destination = "0.0.0.0/0"
  gateway     = infra_igw.demo.id
}

resource "infra_security_group" "web" {
  vpc_id = infra_vpc.demo.id
  name   = "web"
}

resource "infra_security_group_rule" "ssh" {
  security_group_id = infra_security_group.web.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 22
  cidr              = "0.0.0.0/0"
}

resource "infra_security_group_rule" "http" {
  security_group_id = infra_security_group.web.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 80
  cidr              = "0.0.0.0/0"
}

resource "infra_kms_keyring" "demo" {
  name = "demo"
}

resource "infra_kms_key" "disks" {
  keyring_id = infra_kms_keyring.demo.id
  name       = "disks"
  algorithm  = "AES-256"
}

resource "infra_disk" "data" {
  name       = "data"
  size_mb    = 1024
  kms_key_id = infra_kms_key.disks.id
}

resource "infra_compute" "web" {
  name              = "web"
  subnet_id         = infra_subnet.app.id
  security_group_id = infra_security_group.web.id
  node_pool_id      = infra_node_pool.workers.id
  image             = "docker.io/library/nginx:latest"
  cpu               = 0.5
  memory_mb         = 256

  disks = [{
    disk_id    = infra_disk.data.id
    mount_path = "/data"
  }]
}

# A second instance behind the same load balancer. traefik/whoami answers with
# its hostname and addresses, so a request through the load balancer shows
# which backend served it: nginx's welcome page or a whoami report.
resource "infra_compute" "whoami" {
  name              = "whoami"
  hostname          = "whoami"
  subnet_id         = infra_subnet.app.id
  security_group_id = infra_security_group.web.id
  node_pool_id      = infra_node_pool.workers.id
  image             = "docker.io/traefik/whoami:latest"
  cpu               = 0.25
  memory_mb         = 64
}

resource "infra_load_balancer" "web" {
  name      = "web"
  vpc_id    = infra_vpc.demo.id
  port      = 80
  protocol  = "tcp"
  algorithm = "round_robin"
}

resource "infra_lb_backend" "web" {
  lb_id      = infra_load_balancer.web.id
  compute_id = infra_compute.web.id
  port       = 80
}

resource "infra_lb_backend" "whoami" {
  lb_id      = infra_load_balancer.web.id
  compute_id = infra_compute.whoami.id
  port       = 80
}

output "compute_ip" {
  description = "Address assigned to the demo compute instance."
  value       = infra_compute.web.ip
}

output "compute_phase" {
  description = "Lifecycle phase of the demo compute instance."
  value       = infra_compute.web.phase
}

output "whoami_ip" {
  description = "Address assigned to the whoami instance."
  value       = infra_compute.whoami.ip
}

output "whoami_phase" {
  description = "Lifecycle phase of the whoami instance."
  value       = infra_compute.whoami.phase
}

output "lb_address" {
  description = "Virtual address of the load balancer."
  value       = infra_load_balancer.web.address
}

output "lb_phase" {
  description = "Lifecycle phase of the load balancer."
  value       = infra_load_balancer.web.phase
}
