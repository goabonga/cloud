# Simple FaaS demo: one VPC, one private subnet, a security group and a
# single infra_function with a small warm pool. infra_function is realized
# as ordinary compute instances a controller creates and retires to the
# warm-pool policy (see internal/domain/resource/function.go), invoked
# synchronously over HTTP through infra-api, not through Terraform or the
# agent directly - see README.md for the invoke call.

resource "infra_vpc" "demo" {
  cidr = "10.22.0.0/16"
}

resource "infra_subnet" "app" {
  vpc_id = infra_vpc.demo.id
  cidr   = "10.22.1.0/24"
  type   = "private"
}

resource "infra_security_group" "fn" {
  vpc_id = infra_vpc.demo.id
  name   = "fn"
}

resource "infra_security_group_rule" "http" {
  security_group_id = infra_security_group.fn.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 80
  cidr              = "0.0.0.0/0"
}

resource "infra_function" "hello" {
  name              = "hello"
  subnet_id         = infra_subnet.app.id
  security_group_id = infra_security_group.fn.id
  image             = "docker.io/library/nginx:latest"
  port              = 80
  cpu               = 0.5
  memory_mb         = 128
  warm_pool = {
    min_warm         = 1
    allow_cold_start = true
  }
}

output "function_id" {
  description = "Id to invoke: POST <endpoint>/api/v1/function/<id>/invoke."
  value       = infra_function.hello.id
}

output "function_phase" {
  description = "Lifecycle phase: Ready once the warm pool has an instance up."
  value       = infra_function.hello.phase
}

output "function_warm_count" {
  description = "Instances currently warm and backed by a ready compute."
  value       = infra_function.hello.warm_count
}
