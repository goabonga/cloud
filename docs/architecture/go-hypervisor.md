# Go hypervisor

`internal/hypervisor` and `cmd/hypervisor` are a hand-rolled, pure-Go VMM
driving `/dev/kvm` directly: no cgo, no external VMM binary. It is
`microvm`'s realization backend — `internal/manager/vmm.go` drives the
compiled `cmd/hypervisor` binary (packaged as `infra-hypervisor`, shipped
inside the `infra-agent` `.deb`) exactly the way it used to drive
cloud-hypervisor; see [realization.md](realization.md#microvms) for that
integration. This page covers the hypervisor itself: its design and why
it exists at all, not the `microvm` wiring around it.

## Status

All five milestones this effort planned are done: single-vCPU direct
kernel boot, serial console, multi-vCPU/SMP, virtio-net, virtio-blk, and a
combined parity pass — and it is now `microvm`'s only backend.
`cmd/hypervisor` boots a real Linux kernel under KVM with any number of
vCPUs, a working `ttyS0` console, a network interface backed by a host TAP
device and a disk backed by a raw file — all at once in a single VM
(`TestHypervisorBootFull`). Shutdown (the `shutdown` request and
`SIGTERM`/`SIGINT` alike) cleanly closes every KVM/tap/disk fd and removes
the control socket file; `create` requests are validated (vcpus/memory/
kernel path) before this process ever opens `/dev/kvm`.

Deliberately not implemented, a real-world performance refinement rather
than a correctness requirement, and not attempted here:

- `KVM_IOEVENTFD` for `QueueNotify` and `KVM_IRQFD` for interrupt
  injection — every device's interrupt, UART included, is a synchronous
  `KVM_IRQ_LINE` pulse, correct but not the fastest path.

Also not done, genuinely out of scope for this effort rather than a
milestone away: multiqueue virtio, more than 8 MMIO devices (the default
IOAPIC's GSI 16-23 range), and anything needing virtio-pci.

## Why a hand-rolled hypervisor, not cloud-hypervisor

cloud-hypervisor — what `microvm` used before this — is a mature,
security-reviewed VMM shared with Kata Containers and used in production
elsewhere: by most measures, still the *safer* choice, and this effort
accepted that tradeoff knowingly rather than disputing it. The project is
also meant to be a vehicle for learning to build the tools it depends on,
not only integrate them — months of systems-level work and the security
exposure of a hand-rolled VMM, for full control of the hypervisor layer
and the learning that comes with writing it.

## Process model

One `cmd/hypervisor` OS process per VM, spawned and controlled over a Unix
socket — the same isolation granularity cloud-hypervisor had: each
`microvm` instance gets its own process (see `vmm.go`'s `startVMM`). A bug
in this hypervisor's KVM-ioctl or virtio code crashing its process cannot
take `infra-agent` or another VM down with it, and a VM survives an
`infra-agent` restart — the same properties the previous backend had, not
a regression traded for simplicity.

## Package layout

```
internal/hypervisor/            # orchestration: Machine, guest memory, the vcpu run loop
internal/hypervisor/kvm/        # raw /dev/kvm ioctl wrappers and uapi structs — no policy
internal/hypervisor/boot/       # x86-64 Linux boot protocol: bzImage parsing, zero page, E820, GDT, page tables
internal/hypervisor/uart/       # 16550-compatible serial console device
internal/hypervisor/virtio/     # virtio-mmio transport, split virtqueue, virtio-net, a tap-device helper
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
command line, an Intel MP Specification table (not ACPI — see "Multi-vCPU"
below), and the "zero page" (`struct boot_params`) tying it together with
an E820 memory map. The kernel and an optional initrd are loaded at their
own fixed/high addresses.

### Multi-vCPU

Every vCPU — including vCPU 0 — gets the identical boot-protocol register
setup, matching how Firecracker's own x86_64 loader configures every vCPU
uniformly. This is safe for vCPUs other than 0 specifically *because* this
package creates the in-kernel irqchip (`KVM_CREATE_IRQCHIP`): KVM then
holds every non-boot vCPU in `KVM_MP_STATE_UNINITIALIZED` and does not
execute guest code on them until the booted kernel's own SMP bring-up
code sends a real INIT-SIPI-SIPI sequence over the in-kernel LAPIC, which
resets the target vCPU's state (including `%rip`/`%rsp`) to whatever the
SIPI vector specifies — discarding whatever was configured at creation.
Without an in-kernel irqchip, per the KVM API documentation, "the
multiprocessing state must be maintained by userspace"; this package never
does, by design.

CPU topology (so the kernel knows more than one CPU exists at all) comes
from a legacy Intel MP Specification table, not ACPI/MADT — the same
choice Firecracker makes for x86_64, confirmed against its source before
implementing this. It needs no RSDP/XSDT/FADT scaffolding, just the one
self-contained, 16-byte-aligned floating-pointer structure
(`internal/hypervisor/boot/mptable.go`) placed at the fixed low-memory
address (`MPTableAddr`, `0x9fc00`) the kernel's `mpparse.c` falls back to
scanning when no EBDA is signaled — always the case here, since this
package never writes a BIOS Data Area at all. Every mainstream distro and
cloud kernel still carries `CONFIG_X86_MPPARSE`.

## virtio devices

Devices are virtio-mmio, not virtio-pci: no PCI bus, config space or BAR
emulation at all, the same minimal-VMM choice Firecracker made — each
device is just a flat, 512-byte MMIO register window
(`internal/hypervisor/virtio.Transport` owns the whole register state
machine: identity, status/feature negotiation, per-queue address/size/
ready state, interrupt status) that `mmio.go` maps onto a `KVM_EXIT_MMIO`
address range starting at `boot.MMIOBaseAddr`. The guest finds each device
through a `virtio_mmio.device=<size>@<base>:<irq>` kernel command-line
parameter `internal/hypervisor` appends automatically when a device is
configured — no ACPI or device-tree description is needed for devices,
only for CPU topology (see "Multi-vCPU" above).

No `KVM_SET_GSI_ROUTING` call is needed for a device's interrupt: the
default IOAPIC routing `KVM_CREATE_IRQCHIP` already sets up identity-maps
every GSI 0-23 to the same-numbered pin, the same default that already
makes COM1's GSI 4 work. Devices are assigned GSI 16 upward, capping at 8
devices (GSI 16-23, the rest of that 24-pin IOAPIC) — a known scaling
boundary; more devices would need MSI/virtio-pci, out of scope here.

**virtio-net** (`internal/hypervisor/virtio/net.go`) is backed by an
already-persistent TAP device — created and attached to its bridge out of
band, the same way `ExecMicroVMBackend.EnsureMicroVM` already does for
cloud-hypervisor — re-opened via `virtio.OpenTap`'s `TUNSETIFF` call
(a deliberate divergence from cloud-hypervisor's by-name-only TAP
attachment: this package needs the fd itself for frame I/O, cloud-hypervisor
never touches it). No `VIRTIO_NET_F_*` offload feature (checksum, TSO) is
offered, so the 10-byte `virtio_net_hdr` every frame is prefixed/stripped
with is always all-zero. RX is driven by `Net.ReadLoop` reading the tap
independently of any guest notification — delivery, not polling, is what
moves a frame — and drops it if the driver has no RX buffer available yet,
the same tradeoff a real NIC under memory pressure makes; TX is driven by
`Net.HandleNotify`, called from `mmio.go`'s dispatcher on a `QueueNotify`
write.

**virtio-blk** (`internal/hypervisor/virtio/blk.go`) is backed by a single
raw disk file — the same convention `internal/manager`'s
`vmImageCache`/`cloneFile` already produce today, unchanged from when they
fed cloud-hypervisor's boot images — via a small `BlockBackend` interface
(`io.ReaderAt` + `io.WriterAt` + `Sync`) `*os.File` satisfies directly. A
request is three or more descriptors — a read-only header (request type,
sector), zero or more data segments, a writable 1-byte status — handled
entirely from `Blk.HandleNotify`; `VIRTIO_BLK_T_IN`/`OUT`/`FLUSH` are
answered, anything else (discard, write-zeroes, get-id) gets
`VIRTIO_BLK_S_UNSUPP` — genuine parity with what cloud-hypervisor's own
`chDiskConfig{Path, Readonly}` used to expose, not a reduced target. No
multi-queue is offered either, for the same reason.

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
  process only ever boots once). Every field is honored today —
  `vcpus`/`memory_mb`/`kernel_path`/`initrd_path`/`cmdline`/`tap_name`/
  `mac`/`disk_path`/`disk_readonly`.
- **`status`** — `{phase, pid, error}`. `pid` is the `cmd/hypervisor`
  process's own pid, the same role a cloud-hypervisor pidfile used to play
  for `infra-agent`'s liveness checks. Works on a freshly connected
  client, including one reconnecting after its own restart — server state
  (the running `Machine`, its phase) lives for the process's lifetime, not
  per connection.
- **`shutdown`** — stops the vCPU loop, tears the VM down, and exits the
  process. `infra-agent`'s grace-period-then-`SIGKILL` pattern
  (`vmmShutdownGrace` in `internal/manager/microvm.go`) needed no change
  to move from cloud-hypervisor to this process.

## Running it manually

```sh
sudo go run ./cmd/hypervisor -control-socket /tmp/hv.sock &
# from another shell, speaking the protocol directly:
echo '{"id":1,"type":"create","payload":{"vcpus":1,"memory_mb":256,"kernel_path":"/boot/vmlinuz-'"$(uname -r)"'","cmdline":"console=ttyS0 panic=-1"}}' \
  | socat - UNIX-CONNECT:/tmp/hv.sock
# ... later, from the same or another shell:
echo '{"id":2,"type":"shutdown"}' | socat - UNIX-CONNECT:/tmp/hv.sock
```

Needs root (or `kvm` group membership) for `/dev/kvm`, and a real kernel —
most distributions ship one usable directly at `/boot/vmlinuz-$(uname -r)`.
Serial output goes to `cmd/hypervisor`'s own stdout. `shutdown`, and a
plain `Ctrl-C`/`kill` of the process alike, cleanly close every fd and
remove `/tmp/hv.sock`; a `create` payload is rejected immediately, before
`/dev/kvm` is even opened, if `vcpus`/`memory_mb`/`kernel_path` don't make
sense (see `cmd/hypervisor/validate.go`).

Add `"tap_name":"<an existing bridge-attached tap>"` and/or
`"disk_path":"<a raw disk file>"` to the `create` payload to attach a
network device or a disk — both need to already exist (this process never
creates a tap or fetches/clones a disk image itself; `internal/manager`'s
`ExecMicroVMBackend` does that, the same division of labor it had with
cloud-hypervisor before).

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
`TestHypervisorBoot`, `TestHypervisorBootNetworking`,
`TestHypervisorBootDisk`, `TestHypervisorBootFull` and
`TestExecMicroVMBackendBoot` still self-skip in CI regardless: they gate
on `GOA_ITEST_HYPERVISOR_KERNEL`, and no kernel image is provisioned there
- the same limitation `microvm`'s own integration test already has, not
something this effort tries to solve.

`TestHypervisorBootFull` is the milestone 5 parity proof: 2 vcpus, a
tap-backed net device and a disk-backed blk device, all in the same VM at
once — every earlier milestone's test already proves its own piece in
isolation, this one proves they don't interfere with each other running
together, which isn't ruled out by construction (every vcpu's goroutine,
`Net.ReadLoop`, and whichever vcpu services a blk notify are all touching
shared `Machine` state concurrently for the first time together here). It
also checks the goroutine count settles back to its pre-boot baseline
after `Close` — no `goleak` dependency in this repo, so this is
`runtime.NumGoroutine()` deltas.

`TestHypervisorBootNetworking` is the fullest proof so far: it creates a
real bridge and TAP device, boots a VM with `ip=`-based static networking
(the same early, rootfs-independent configuration `internal/manager`'s
`guestCmdline` uses regardless of which backend is on the other end), and
pings the guest from the host — a successful reply is a full round trip
through both Net.ReadLoop (host → guest) and Net.HandleNotify (guest →
host), not just "an interface showed up".

`TestHypervisorBootDisk` attaches a plain (not a real filesystem) raw
file and asks the kernel to mount it as root — there's no init to boot
into, so the mount necessarily fails, but reaching "VFS: Unable to mount
root fs" at all means the kernel's virtio_blk driver really probed the
device and performed real reads against it through `Blk.HandleNotify`.
Verifying an exact byte round-trip at the guest-userspace level needs a
prepared bootable disk image (with a real init) this automated test
doesn't attempt to build; that's a manual verification step, the same way
running `cmd/hypervisor` by hand already is.
