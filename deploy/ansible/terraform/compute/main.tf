# Simple compute demo: one VPC, one private subnet, a security group and a
# single infra_compute instance running nginx - the minimal path from zero to
# a scheduled, reachable workload. No load balancer, DNS, TLS or KMS-encrypted
# disk; see ../vm for a real VM-backed workload instead of this namespaced
# container, and ../k8s for a full cluster.

resource "infra_vpc" "demo" {
  cidr = "10.21.0.0/16"
}

resource "infra_subnet" "app" {
  vpc_id = infra_vpc.demo.id
  cidr   = "10.21.1.0/24"
  type   = "private"
}

resource "infra_security_group" "web" {
  vpc_id = infra_vpc.demo.id
  name   = "web"
}

resource "infra_security_group_rule" "http" {
  security_group_id = infra_security_group.web.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 80
  cidr              = "0.0.0.0/0"
}

resource "infra_compute" "web" {
  name              = "web"
  hostname          = "web"
  subnet_id         = infra_subnet.app.id
  security_group_id = infra_security_group.web.id
  image             = "docker.io/library/nginx:latest"
  cpu               = 0.5
  memory_mb         = 128
}

output "web_ip" {
  description = "Address the agent assigned to the instance."
  value       = infra_compute.web.ip
}

output "web_phase" {
  description = "Lifecycle phase: Ready once nginx is scheduled and healthy."
  value       = infra_compute.web.phase
}
