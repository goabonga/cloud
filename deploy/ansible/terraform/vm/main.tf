# Simple micro-VM demo: one VPC, one private subnet, a security group and a
# single infra_microvm with a generated SSH key - the minimal path to a real
# VM under infra-hypervisor (see ../../../../docs/architecture/realization.md
# #microvms), as opposed to the namespaced container ../compute runs. No
# user_data is given, so the agent writes a minimal cloud-config from
# hostname and ssh_authorized_key itself; see ../k8s for the "#!" script
# override instead, and for a multi-node cluster rather than a single VM.

resource "tls_private_key" "demo" {
  algorithm = "ED25519"
}

resource "infra_vpc" "demo" {
  cidr = "10.23.0.0/16"
}

resource "infra_subnet" "app" {
  vpc_id = infra_vpc.demo.id
  cidr   = "10.23.1.0/24"
  type   = "private"
}

resource "infra_security_group" "vm" {
  vpc_id = infra_vpc.demo.id
  name   = "vm"
}

resource "infra_security_group_rule" "ssh" {
  security_group_id = infra_security_group.vm.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 22
  cidr              = "0.0.0.0/0"
}

resource "infra_microvm" "demo" {
  name              = "demo"
  hostname          = "demo"
  subnet_id         = infra_subnet.app.id
  security_group_id = infra_security_group.vm.id
  # A plain base-image boot (no kubeadm) needs much less than ../k8s's
  # 2048MiB; 1024 is comfortable headroom for cloud-init on one vCPU.
  vcpus              = 1
  memory_mb          = 1024
  kernel_path        = "/var/lib/infra-microvm-images/vmlinuz"
  initrd_path        = "/var/lib/infra-microvm-images/initrd.img"
  cmd_line           = "console=ttyS0 root=/dev/vda1 rw"
  image              = "/var/lib/infra-microvm-images/noble-base.raw"
  ssh_authorized_key = trimspace(tls_private_key.demo.public_key_openssh)
}

output "vm_ip" {
  description = "Address the agent assigned to the VM."
  value       = infra_microvm.demo.ip
}

output "vm_phase" {
  description = "Lifecycle phase: Ready once infra-hypervisor has booted it."
  value       = infra_microvm.demo.phase
}

output "ssh_private_key" {
  description = "Private half of the generated key pair (terraform output -raw ssh_private_key, then ssh -i <(that) ubuntu@<vm_ip>)."
  value       = tls_private_key.demo.private_key_openssh
  sensitive   = true
}
