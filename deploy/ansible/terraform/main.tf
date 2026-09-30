# A demo topology provisioned against the deployed control plane:
#   vpc -> subnet -> internet gateway + route -> security group + rules
#   -> two nginx instances, scheduled onto the agent node pool, each serving a
#   page from its own KMS-encrypted disk -> a layer-4 load balancer in front.

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

# Two nginx instances behind one load balancer. Each serves its own page from
# a KMS-encrypted disk mounted over nginx's document root; a disk file puts an
# index.html naming the instance on it, so a request through the load balancer
# shows which backend answered.
locals {
  web = toset(["web-1", "web-2"])
}

resource "infra_disk" "site" {
  for_each   = local.web
  name       = "${each.key}-site"
  size_mb    = 64
  kms_key_id = infra_kms_key.disks.id
}

resource "infra_disk_file" "index" {
  for_each = local.web
  disk_id  = infra_disk.site[each.key].id
  path     = "index.html"
  mode     = "0644"
  content  = <<-HTML
    <!doctype html>
    <title>${each.key}</title>
    <h1>Served by ${each.key}</h1>
  HTML
}

resource "infra_compute" "web" {
  for_each          = local.web
  name              = each.key
  hostname          = each.key
  subnet_id         = infra_subnet.app.id
  security_group_id = infra_security_group.web.id
  node_pool_id      = infra_node_pool.workers.id
  image             = "docker.io/library/nginx:latest"
  cpu               = 0.5
  memory_mb         = 128

  disks = [{
    disk_id    = infra_disk.site[each.key].id
    mount_path = "/usr/share/nginx/html"
  }]
}

resource "infra_load_balancer" "web" {
  name      = "web"
  vpc_id    = infra_vpc.demo.id
  port      = 80
  protocol  = "tcp"
  algorithm = "round_robin"
}

resource "infra_lb_backend" "web" {
  for_each   = local.web
  lb_id      = infra_load_balancer.web.id
  compute_id = infra_compute.web[each.key].id
  port       = 80
}

output "web_ips" {
  description = "Address assigned to each nginx instance."
  value       = { for k, c in infra_compute.web : k => c.ip }
}

output "web_phases" {
  description = "Lifecycle phase of each nginx instance."
  value       = { for k, c in infra_compute.web : k => c.phase }
}

output "lb_address" {
  description = "Virtual address of the load balancer."
  value       = infra_load_balancer.web.address
}

output "lb_phase" {
  description = "Lifecycle phase of the load balancer."
  value       = infra_load_balancer.web.phase
}
