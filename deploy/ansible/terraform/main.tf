# A demo topology provisioned against the deployed control plane:
#   vpc -> subnet -> internet gateway + route -> security group + rules
#   -> two nginx instances, scheduled onto the agent node pool, each serving a
#   page from its own KMS-encrypted disk over HTTPS -> a load balancer in
#   front, terminating HTTPS and re-encrypting towards them -> a private DNS zone naming it inside the VPC, and a public zone
#   -> certificates for both: public names signed by the platform's public
#   root, internal ones by a CA only the VPC trusts.

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

# The instances' egress is filtered: their HTTP and HTTPS go through the
# gateway's egress proxy - redirected, or explicitly on its address, port
# 3128 - which lets out only the domains below, and anything else leaving
# through the gateway is refused unless its destination is allowed.
resource "infra_igw" "demo" {
  vpc_id = infra_vpc.demo.id
  egress_proxy = {
    enabled = true
    allowed_domains = [
      "example.com",
      # The demo's own public names, served by its load balancer.
      "demo.test",
      "*.demo.test",
    ]
  }
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

resource "infra_security_group_rule" "https" {
  security_group_id = infra_security_group.web.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 443
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

# TLS. The public names are signed by the platform's public root, which the
# API creates at its first start and every lab machine and instance trusts.
# The internal names are signed by a CA of the demo's own, trusted only by the
# instances of the VPC it names.
resource "infra_ssl_ca" "internal" {
  common_name  = "demo internal CA"
  organization = "demo"
  vpc_ids      = [infra_vpc.demo.id]
}

resource "infra_ssl_cert" "public" {
  ca_id       = "public-root"
  common_name = "demo.test"
  dns_names   = ["demo.test", "www.demo.test"]
}

resource "infra_ssl_cert" "internal" {
  ca_id       = infra_ssl_ca.internal.id
  common_name = "web.internal.demo"
  dns_names   = ["web.internal.demo", "www.internal.demo"]
}

# nginx's configuration lives on a disk of its own, mounted over conf.d: the
# server blocks, and each certificate's chain and private key, which the agent
# renders from the store so the keys never pass through Terraform.
resource "infra_disk" "conf" {
  for_each   = local.web
  name       = "${each.key}-conf"
  size_mb    = 64
  kms_key_id = infra_kms_key.disks.id
}

resource "infra_disk_file" "nginx_conf" {
  for_each = local.web
  disk_id  = infra_disk.conf[each.key].id
  path     = "default.conf"
  mode     = "0644"
  content  = <<-NGINX
    server {
      listen 80 default_server;
      listen 443 ssl default_server;
      server_name demo.test www.demo.test;
      ssl_certificate     /etc/nginx/conf.d/tls/public.crt;
      ssl_certificate_key /etc/nginx/conf.d/tls/public.key;
      root /usr/share/nginx/html;
    }

    server {
      listen 443 ssl;
      server_name web.internal.demo www.internal.demo;
      ssl_certificate     /etc/nginx/conf.d/tls/internal.crt;
      ssl_certificate_key /etc/nginx/conf.d/tls/internal.key;
      root /usr/share/nginx/html;
    }
  NGINX
}

locals {
  # Every (instance, file) pair: each instance gets both certificates.
  tls_files = merge([
    for w in local.web : {
      for f in [
        { name = "public.crt", cert = "public", part = "chain" },
        { name = "public.key", cert = "public", part = "private_key" },
        { name = "internal.crt", cert = "internal", part = "chain" },
        { name = "internal.key", cert = "internal", part = "private_key" },
      ] : "${w}/${f.name}" => merge(f, { web = w })
    }
  ]...)
  ssl_certs = {
    public   = infra_ssl_cert.public.id
    internal = infra_ssl_cert.internal.id
  }
}

resource "infra_disk_file" "tls" {
  for_each    = local.tls_files
  disk_id     = infra_disk.conf[each.value.web].id
  path        = "tls/${each.value.name}"
  ssl_cert_id = local.ssl_certs[each.value.cert]
  ssl_part    = each.value.part
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
  # The agent writes the disk files once the disks are mounted, after the
  # instance starts: nginx waits for its keys rather than failing on them.
  command = "until [ -s /etc/nginx/conf.d/tls/public.key ] && [ -s /etc/nginx/conf.d/tls/internal.key ]; do sleep 1; done; exec nginx -g 'daemon off;'"

  disks = [
    {
      disk_id    = infra_disk.site[each.key].id
      mount_path = "/usr/share/nginx/html"
    },
    {
      disk_id    = infra_disk.conf[each.key].id
      mount_path = "/etc/nginx/conf.d"
    },
  ]
}

# A public address for the load balancer, from the block routed to the edges:
# they serve the load balancer on it, so it is reachable from outside the VPC.
# Pinned, like the VIP, so the public DNS record can name it.
resource "infra_ip_address" "web_public" {
  type    = "public"
  address = "203.0.113.10"
}

resource "infra_load_balancer" "web" {
  name   = "web"
  vpc_id = infra_vpc.demo.id
  # Pinned rather than left to the agent, so the DNS record below can name it:
  # an address the agent assigns is only known after the apply.
  address      = "10.20.0.10"
  public_ip_id = infra_ip_address.web_public.id
  # No layer-4 port: the listeners below serve its addresses, with infra-lb
  # running in the VPC's load-balancer namespace on every agent.
}

# HTTPS is terminated on the load balancer with the certificate matching the
# requested name (SNI) - the public one for demo.test, the internal one for
# web.internal.demo - and re-encrypted towards nginx, which presents its
# internal certificate: the target group asks for web.internal.demo and
# verifies it against the internal CA.
resource "infra_lb_target_group" "web_https" {
  vpc_id        = infra_vpc.demo.id
  protocol      = "https"
  port          = 443
  backend_ca_id = infra_ssl_ca.internal.id
  server_name   = "web.internal.demo"
  health_check = {
    path = "/"
  }
}

resource "infra_lb_target_group" "web_http" {
  vpc_id   = infra_vpc.demo.id
  protocol = "http"
  port     = 80
}

resource "infra_lb_target" "web_https" {
  for_each        = local.web
  target_group_id = infra_lb_target_group.web_https.id
  compute_id      = infra_compute.web[each.key].id
}

resource "infra_lb_target" "web_http" {
  for_each        = local.web
  target_group_id = infra_lb_target_group.web_http.id
  compute_id      = infra_compute.web[each.key].id
}

resource "infra_lb_listener" "https" {
  load_balancer_id        = infra_load_balancer.web.id
  port                    = 443
  protocol                = "https"
  tls_mode                = "reencrypt"
  certificate_ids         = [infra_ssl_cert.public.id, infra_ssl_cert.internal.id]
  default_target_group_id = infra_lb_target_group.web_https.id
}

resource "infra_lb_listener" "http" {
  load_balancer_id        = infra_load_balancer.web.id
  port                    = 80
  protocol                = "http"
  default_target_group_id = infra_lb_target_group.web_http.id
}

# DNS, served by the agents themselves. A private zone attached to the VPC,
# answered by the resolver every instance is handed (the VPC's first address,
# 10.20.0.1), and a public zone, also answered on the edges' public DNS address
# (203.0.113.53), authoritatively and to anyone.
resource "infra_dns_zone" "internal" {
  name       = "internal"
  domain     = "internal.demo"
  visibility = "private"
  vpc_ids    = [infra_vpc.demo.id]
}

resource "infra_dns_record" "web" {
  zone_id = infra_dns_zone.internal.id
  name    = "web"
  type    = "A"
  records = [infra_load_balancer.web.address]
}

resource "infra_dns_record" "www_internal" {
  zone_id = infra_dns_zone.internal.id
  name    = "www"
  type    = "CNAME"
  records = ["web.internal.demo."]
}

resource "infra_dns_zone" "public" {
  name       = "public"
  domain     = "demo.test"
  visibility = "public"
}

# The public DNS itself, reachable from the simulated Internet.
resource "infra_dns_record" "ns" {
  zone_id = infra_dns_zone.public.id
  name    = "ns"
  type    = "A"
  records = ["203.0.113.53"]
}

# The zone apex is the load balancer's public address, served by the edges,
# and www an alias of it.
resource "infra_dns_record" "apex" {
  zone_id = infra_dns_zone.public.id
  name    = "@"
  type    = "A"
  records = [infra_ip_address.web_public.address]
}

resource "infra_dns_record" "www_public" {
  zone_id = infra_dns_zone.public.id
  name    = "www"
  type    = "CNAME"
  records = ["demo.test."]
}

resource "infra_dns_record" "txt" {
  zone_id = infra_dns_zone.public.id
  name    = "@"
  type    = "TXT"
  records = ["\"served by the infra agents\""]
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

output "dns_names" {
  description = "Names the demo publishes, private and public."
  value = {
    private = ["web.internal.demo", "www.internal.demo"]
    public  = ["demo.test", "www.demo.test", "ns.demo.test"]
  }
}

output "lb_public_address" {
  description = "Public address the edges serve the load balancer on."
  value       = infra_ip_address.web_public.address
}

output "internal_ca_pem" {
  description = "Certificate of the demo's internal CA, trusted by the VPC's instances only."
  value       = infra_ssl_ca.internal.cert_pem
}

output "listener_phases" {
  description = "Lifecycle phase of each listener: Ready once infra-lb holds its port."
  value = {
    https = infra_lb_listener.https.phase
    http  = infra_lb_listener.http.phase
  }
}

output "egress_proxy_address" {
  description = "Where the instances reach the egress proxy explicitly, on port 3128."
  value       = infra_igw.demo.egress_proxy_address
}

# --- Micro-VM demo: a 3-node kubeadm cluster -------------------------------
# A real VM per node under infra-hypervisor (infra_microvm), not a namespaced
# container like infra_compute above: one control plane and two workers,
# seeded entirely by cloud-init - no image baked for either role. Stock
# kubeadm, not a lighter distribution: each micro-VM gets enough CPU/RAM to
# clear kubeadm's own preflight checks (2 vCPUs, 2048MiB - the minimum is
# 1700MiB).
#
# Bootstrap order matters once, at creation: the workers' user-data embeds
# the control plane's address, which the agent only assigns once it
# reconciles the resource - asynchronously, not within this apply. Create the
# control plane on its own first:
#   terraform apply -target=infra_microvm.k8s_control_plane
# wait for it to reach Ready and for kubeadm to come up (dashboard, or poll
# `terraform show`), then apply the rest:
#   terraform apply
# A single `terraform apply` from scratch also works for everything *except*
# the workers, whose join command would otherwise embed an empty address.

# A key pair for this demo alone, generated by Terraform rather than supplied
# from the operator's own ~/.ssh - the private half only ever surfaces
# through `terraform output`, never written to disk by this config.
resource "tls_private_key" "k8s" {
  algorithm = "ED25519"
}

locals {
  # Not a secret: the k8s subnet is private to this VPC, reachable only from
  # inside it or through infra's own firewalling. Fixed rather than
  # kubeadm-generated so the control plane and a worker agree on it without a
  # second apply; must match kubeadm's token format (6 lowercase
  # alphanumerics, a dot, 16 more).
  k8s_join_token = "lab0k8.demojointoken001"
  k8s_vmlinuz    = "/var/lib/infra-microvm-images/vmlinuz"
  k8s_initrd     = "/var/lib/infra-microvm-images/initrd.img"
  k8s_image      = "/var/lib/infra-microvm-images/noble-base.raw"
  # No bootloader under direct kernel boot, so the root partition has to be
  # named explicitly; vda1 matches the base image's layout (see
  # ansible/roles/microvm).
  k8s_cmdline = "console=ttyS0 root=/dev/vda1 rw"

  # Shared by both roles: the SSH key, swap off, the kernel modules and
  # sysctls kubeadm's preflight checks require, and containerd plus the
  # kubeadm/kubelet/kubectl packages from the upstream apt repository. A "#!"
  # user-data script, not "#cloud-config" - see domain/resource/microvm.go -
  # so there is nowhere a YAML block's indentation could get in the way.
  k8s_common = join("\n", [
    "#!/bin/bash",
    "set -euo pipefail",
    "",
    "mkdir -p /home/ubuntu/.ssh",
    "echo '${trimspace(tls_private_key.k8s.public_key_openssh)}' >> /home/ubuntu/.ssh/authorized_keys",
    "chown -R ubuntu:ubuntu /home/ubuntu/.ssh",
    "chmod 700 /home/ubuntu/.ssh",
    "chmod 600 /home/ubuntu/.ssh/authorized_keys",
    "",
    "swapoff -a",
    "",
    "cat >/etc/modules-load.d/k8s.conf <<'EOF'",
    "overlay",
    "br_netfilter",
    "EOF",
    "modprobe overlay",
    "modprobe br_netfilter",
    "",
    "cat >/etc/sysctl.d/k8s.conf <<'EOF'",
    "net.bridge.bridge-nf-call-iptables  = 1",
    "net.bridge.bridge-nf-call-ip6tables = 1",
    "net.ipv4.ip_forward                 = 1",
    "EOF",
    "sysctl --system",
    "",
    "export DEBIAN_FRONTEND=noninteractive",
    "apt-get update",
    "apt-get install -y apt-transport-https ca-certificates curl gnupg containerd",
    "",
    "mkdir -p /etc/containerd",
    "containerd config default | sed 's/SystemdCgroup = false/SystemdCgroup = true/' >/etc/containerd/config.toml",
    "systemctl restart containerd",
    "",
    "mkdir -p /etc/apt/keyrings",
    "curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg",
    "echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.31/deb/ /' >/etc/apt/sources.list.d/kubernetes.list",
    "apt-get update",
    "apt-get install -y kubelet kubeadm kubectl",
    "apt-mark hold kubelet kubeadm kubectl",
  ])
}

resource "infra_subnet" "k8s" {
  vpc_id = infra_vpc.demo.id
  cidr   = "10.20.2.0/24"
  type   = "private"
}

resource "infra_security_group" "k8s" {
  vpc_id = infra_vpc.demo.id
  name   = "k8s"
}

# Lab simplification: one rule opens the whole k8s subnet to itself rather
# than enumerating kubeadm's exact ports (6443, 10250, etcd's 2379/2380,
# flannel's VXLAN 8472/udp, ...), plus SSH for debugging.
resource "infra_security_group_rule" "k8s_internal" {
  security_group_id = infra_security_group.k8s.id
  direction         = "ingress"
  protocol          = "all"
  cidr              = infra_subnet.k8s.cidr
}

resource "infra_security_group_rule" "k8s_ssh" {
  security_group_id = infra_security_group.k8s.id
  direction         = "ingress"
  protocol          = "tcp"
  port              = 22
  cidr              = "0.0.0.0/0"
}

resource "infra_microvm" "k8s_control_plane" {
  name              = "k8s-cp"
  hostname          = "k8s-cp"
  subnet_id         = infra_subnet.k8s.id
  security_group_id = infra_security_group.k8s.id
  vcpus             = 2
  memory_mb         = 2048
  kernel_path       = local.k8s_vmlinuz
  initrd_path       = local.k8s_initrd
  cmd_line          = local.k8s_cmdline
  image             = local.k8s_image

  user_data = join("\n", [
    local.k8s_common,
    "kubeadm init --pod-network-cidr=10.244.0.0/16 --token=${local.k8s_join_token} --token-ttl=0",
    "mkdir -p /home/ubuntu/.kube",
    "cp /etc/kubernetes/admin.conf /home/ubuntu/.kube/config",
    "chown -R ubuntu:ubuntu /home/ubuntu/.kube",
    "KUBECONFIG=/etc/kubernetes/admin.conf kubectl apply -f https://raw.githubusercontent.com/flannel-io/flannel/master/Documentation/kube-flannel.yml",
  ])
}

resource "infra_microvm" "k8s_worker" {
  for_each = toset(["node-1", "node-2"])

  name              = "k8s-${each.key}"
  hostname          = "k8s-${each.key}"
  subnet_id         = infra_subnet.k8s.id
  security_group_id = infra_security_group.k8s.id
  vcpus             = 2
  memory_mb         = 2048
  kernel_path       = local.k8s_vmlinuz
  initrd_path       = local.k8s_initrd
  cmd_line          = local.k8s_cmdline
  image             = local.k8s_image

  # Ordering only: the control plane must be created (and reconciled by the
  # agent, which assigns its address) before a worker's user-data can embed
  # the join address. See the bootstrap note above - this does not by itself
  # make a single `terraform apply` safe for the workers.
  depends_on = [infra_microvm.k8s_control_plane]

  user_data = join("\n", [
    local.k8s_common,
    # The join token is fixed (see k8s_join_token) rather than the one
    # `kubeadm init` would otherwise print, and discovery skips verifying the
    # control plane's CA hash - neither is known before the control plane has
    # actually booted and run it, which a single apply cannot wait for.
    # Acceptable inside this lab's own private subnet; not a pattern for a
    # real cluster.
    "kubeadm join ${infra_microvm.k8s_control_plane.ip}:6443 --token=${local.k8s_join_token} --discovery-token-unsafe-skip-ca-verification",
  ])
}

output "k8s_control_plane_ip" {
  description = "Address of the k8s control-plane micro-VM."
  value       = infra_microvm.k8s_control_plane.ip
}

output "k8s_worker_ips" {
  description = "Addresses of the k8s worker micro-VMs."
  value       = { for k, v in infra_microvm.k8s_worker : k => v.ip }
}

output "k8s_phases" {
  description = "Lifecycle phase of every k8s micro-VM: Ready once infra-hypervisor has booted it."
  value = merge(
    { "k8s-cp" = infra_microvm.k8s_control_plane.phase },
    { for k, v in infra_microvm.k8s_worker : "k8s-${k}" => v.phase }
  )
}

output "k8s_ssh_private_key" {
  description = "Private half of the key pair generated for this demo (terraform output -raw k8s_ssh_private_key, then ssh -i <(that) ubuntu@<ip>)."
  value       = tls_private_key.k8s.private_key_openssh
  sensitive   = true
}
