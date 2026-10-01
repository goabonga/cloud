# Go hypervisor

`internal/hypervisor` and `cmd/hypervisor` are a hand-rolled, pure-Go VMM
driving `/dev/kvm` directly: no cgo, no external VMM binary. It is a
from-scratch replacement for the [cloud-hypervisor](realization.md#microvms)
backend `microvm` uses today, built both to remove that external
dependency and as a deliberate choice to own the hypervisor layer rather
than only integrate one.

It is **not yet wired into `microvm`** as a selectable backend —
`internal/manager/vmm.go` and `ExecMicroVMBackend` are untouched, and
`cloud-hypervisor` remains the only realization path in production. This
page describes what exists today and the milestones ahead of it being an
option there.

## Status

Milestone 1 (single vCPU, direct kernel boot, serial console) is done:
`cmd/hypervisor` can boot a real Linux kernel and initrd under KVM and
serve a working `ttyS0` console. Ahead: multi-vCPU (SMP), virtio-net,
virtio-blk, then a combined parity pass and hardening — see the plan this
series follows for the full milestone breakdown.

Not implemented yet, each deliberately scoped to a later milestone:

- More than one vCPU (`Config.VCPUs` other than `1` is rejected).
- Any virtio device — no network, no disk. A VM boots from its kernel and
  initrd only; there is no way to mount a root filesystem yet.
- Any MMIO device at all (the run loop's `KVM_EXIT_MMIO` dispatch exists
  but nothing registers a handler for it).
- `KVM_IRQFD`/`KVM_IOEVENTFD` (the UART's interrupt is a synchronous
  `KVM_IRQ_LINE` pulse per byte, correct but not the fastest path).

## Why a hand-rolled hypervisor, not cloud-hypervisor

cloud-hypervisor is a mature, security-reviewed VMM shared with Kata
Containers and used in production elsewhere — the original, and still
generally the *safer*, choice for `microvm` (see
[realization.md](realization.md#microvms) for that rationale, which still
applies to the shipped backend). This effort exists because the project
is also meant to be a vehicle for learning to build the tools it depends
on, not only integrate them — an explicit, accepted tradeoff: months of
systems-level work and the security exposure of a hand-rolled VMM, for
full control of the hypervisor layer and the learning that comes with
writing it.

## Process model

One `cmd/hypervisor` OS process per VM, spawned and controlled over a Unix
socket — the same isolation granularity `cloud-hypervisor` has today (each
`microvm` instance already gets its own process; see `vmm.go`'s
`startVMM`). A bug in this hypervisor's KVM-ioctl or virtio code crashing
its process cannot take `infra-agent` or another VM down with it, and a VM
survives an `infra-agent` restart — the same properties the current
backend has, not a regression traded for simplicity.

## Package layout

```
internal/hypervisor/            # orchestration: Machine, guest memory, the vcpu run loop
internal/hypervisor/kvm/        # raw /dev/kvm ioctl wrappers and uapi structs — no policy
internal/hypervisor/boot/       # x86-64 Linux boot protocol: bzImage parsing, zero page, E820, GDT, page tables
internal/hypervisor/uart/       # 16550-compatible serial console device
internal/hypervisor/protocol/   # the control-socket wire format (NDJSON)
cmd/hypervisor/                 # the binary: control-socket server wrapping internal/hypervisor
```

`internal/hypervisor/kvm` and `internal/hypervisor/boot` are pure: no
ioctls in `boot`, no boot-protocol policy in `kvm`. Every KVM ioctl request
number and uapi struct in `kvm` is hand-derived from the kernel's own
`linux/kvm.h`/`asm/kvm.h` headers and checked against them by
`kvm_internal_test.go` — `golang.org/x/sys/unix` has no built-in KVM
support to build on.

## Boot protocol

Kernels boot under the Linux x86-64 boot protocol's **64-bit entry path**:
the vCPU starts directly in long mode with paging already enabled (CR0,
CR3, CR4, EFER and segment registers set via `KVM_SET_SREGS`/`KVM_SET_REGS`
before the first `KVM_RUN`), entering at the loaded kernel's base address
plus `0x200` — `startup_64` in the kernel's decompression stub. There is no
real-mode or 32-bit protected-mode code executed at all, and so no BIOS or
UEFI firmware path exists or is planned; this matches what `microvm`
already requires of its `KernelPath`/`InitrdPath` today (see
`internal/domain/resource/microvm.go`).

Guest low memory holds (see `internal/hypervisor/boot/layout.go` for the
exact addresses): a flat 3-entry GDT, 3-level identity-mapped page tables
(2 MiB pages, one page-directory page per GiB of guest memory), the kernel
command line, and the "zero page" (`struct boot_params`) tying it together
with an E820 memory map. The kernel and an optional initrd are loaded at
their own fixed/high addresses.

## Control-socket protocol

`cmd/hypervisor` listens on a Unix domain socket (its path given via
`-control-socket`) and speaks newline-delimited JSON
(`internal/hypervisor/protocol`): one `json.Marshal`'d message per line, no
HTTP framing — a persistent 1:1 connection with no external client gets
nothing from request/response machinery that NDJSON doesn't already give
it, including room for the server to push an unsolicited event later
without changing the framing.

Three request types today:

- **`create`** — boots the VM (cloud-hypervisor's separate `vm.create`
  and `vm.boot` calls are deliberately combined into one, since this
  process only ever boots once). The payload already carries
  `disk_path`/`disk_readonly`/`tap_name`/`mac` fields for virtio-blk and
  virtio-net to use once those milestones land; a VM today only honors
  `vcpus`/`memory_mb`/`kernel_path`/`initrd_path`/`cmdline`.
- **`status`** — `{phase, pid, error}`. `pid` is the `cmd/hypervisor`
  process's own pid, serving the same role a cloud-hypervisor pidfile does
  for `infra-agent`'s liveness checks today. Works on a freshly connected
  client, including one reconnecting after its own restart — server state
  (the running `Machine`, its phase) lives for the process's lifetime, not
  per connection.
- **`shutdown`** — stops the vCPU loop, tears the VM down, and exits the
  process. `infra-agent`'s existing grace-period-then-`SIGKILL` pattern
  (`chvShutdownGrace`) needs no change to work against this process
  instead of a cloud-hypervisor one.

## Running it manually

```sh
sudo go run ./cmd/hypervisor -control-socket /tmp/hv.sock &
# from another shell, speaking the protocol directly:
echo '{"id":1,"type":"create","payload":{"vcpus":1,"memory_mb":256,"kernel_path":"/boot/vmlinuz-'"$(uname -r)"'","cmdline":"console=ttyS0 panic=-1"}}' \
  | socat - UNIX-CONNECT:/tmp/hv.sock
```

Needs root (or `kvm` group membership) for `/dev/kvm`, and a real kernel —
most distributions ship one usable directly at `/boot/vmlinuz-$(uname -r)`.
Serial output goes to `cmd/hypervisor`'s own stdout.

## Testing

Unit tests (`go test ./internal/hypervisor/...`) cover everything that
doesn't need real KVM access: ioctl request-number/struct-size derivation,
the boot-protocol byte builders, the UART register state machine, the
control-socket codec. Tests needing `/dev/kvm` live in
`test/integration/` (`-tags integration`), gated the same way
`TestExecMicroVMBackendBoot` is — root, and a kernel path given through an
environment variable:

```sh
sudo GOA_ITEST_HYPERVISOR_KERNEL=/boot/vmlinuz-$(uname -r) \
  go test -tags integration ./test/integration/ -run TestHypervisorBoot -v
```

GitHub-hosted CI runners do have `/dev/kvm` (nested virtualization on the
hosts behind them) once ci.yml's `integration` job opens it up with a udev
rule - `TestKVMOpen` and `TestKVMCreateVMAndVCPU` run for real there.
`TestHypervisorBoot` and `TestExecMicroVMBackendBoot` still self-skip in CI
regardless: they gate on `GOA_ITEST_HYPERVISOR_KERNEL`, and no kernel image
is provisioned there - the same limitation `microvm`'s own integration test
already has, not something this effort tries to solve.
