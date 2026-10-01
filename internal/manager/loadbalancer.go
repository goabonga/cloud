// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// LBRealServer is one backend behind a load balancer's virtual service.
type LBRealServer struct {
	IP     string
	Port   int
	Weight int
}

// LoadBalancerBackend abstracts the IPVS operations a load balancer needs.
type LoadBalancerBackend interface {
	// EnsureService binds the VIP to the bridge, ensures the IPVS virtual service
	// and syncs its real servers to the desired set. Traffic to the real servers
	// leaves through nodePort, masqueraded to this host's address there, so
	// every reply returns through this host whichever host the backend runs on
	// and whichever subnet the client sits in. Idempotent.
	EnsureService(ctx context.Context, vip string, port int, protocol, algorithm, bridge, nodePort string, servers []LBRealServer) error
	// DeleteService removes the virtual service and the VIP. Idempotent.
	DeleteService(ctx context.Context, vip string, port int, protocol, bridge string) error
}

// ipvsProtoFlag maps a protocol to its ipvsadm flag.
func ipvsProtoFlag(protocol string) string {
	if strings.EqualFold(protocol, "udp") {
		return "-u"
	}
	return "-t"
}

// ipvsScheduler maps an algorithm to its ipvsadm scheduler name.
func ipvsScheduler(algorithm string) string {
	switch algorithm {
	case "least_conn":
		return "lc"
	case "source":
		return "sh"
	default:
		return "rr"
	}
}

// lbWeight returns a usable weight, defaulting to 1.
func lbWeight(w int) int {
	if w <= 0 {
		return 1
	}
	return w
}

// ExecLB is a LoadBalancerBackend that shells out to iproute2 and ipvsadm. It
// requires root and ipvsadm at run time.
type ExecLB struct {
	run Runner
}

// NewExecLB returns an ExecLB using the real commands.
func NewExecLB() *ExecLB {
	return &ExecLB{run: defaultRun}
}

// NewExecLBWithRunner returns a backend driven by a custom runner, used in tests
// to assert the issued commands without touching the kernel.
func NewExecLBWithRunner(run Runner) *ExecLB {
	return &ExecLB{run: run}
}

// EnsureService binds the VIP, ensures the virtual service and reconciles its
// real servers to match the desired set.
func (b *ExecLB) EnsureService(ctx context.Context, vip string, port int, protocol, algorithm, bridge, nodePort string, servers []LBRealServer) error {
	// replace, not add: idempotent without matching iproute2's error text
	// (see ExecBackend.EnsureAddress).
	if out, err := b.run(ctx, "ip", "addr", "replace", vip+"/32", "dev", bridge); err != nil {
		return fmt.Errorf("manager: add vip %s on %s: %w: %s", vip, bridge, err, strings.TrimSpace(out))
	}
	proto := ipvsProtoFlag(protocol)
	sched := ipvsScheduler(algorithm)
	svc := fmt.Sprintf("%s:%d", vip, port)

	if _, err := b.run(ctx, "ipvsadm", "-A", proto, svc, "-s", sched); err != nil {
		if out, eErr := b.run(ctx, "ipvsadm", "-E", proto, svc, "-s", sched); eErr != nil {
			return fmt.Errorf("manager: ensure ipvs service %s: %w: %s", svc, eErr, strings.TrimSpace(out))
		}
	}
	if err := b.ensureFullNAT(ctx, nodePort); err != nil {
		return err
	}

	out, _ := b.run(ctx, "ipvsadm", "-Ln", proto, svc)
	current := parseRealServers(out)
	desired := make(map[string]LBRealServer, len(servers))
	for _, s := range servers {
		desired[fmt.Sprintf("%s:%d", s.IP, s.Port)] = s
	}
	for addr, s := range desired {
		flag := "-a"
		if current[addr] {
			flag = "-e"
		}
		if out, err := b.run(ctx, "ipvsadm", flag, proto, svc, "-r", addr, "-m", "-w", strconv.Itoa(lbWeight(s.Weight))); err != nil {
			return fmt.Errorf("manager: ensure real server %s on %s: %w: %s", addr, svc, err, strings.TrimSpace(out))
		}
	}
	for addr := range current {
		if _, ok := desired[addr]; !ok {
			_, _ = b.run(ctx, "ipvsadm", "-d", proto, svc, "-r", addr)
		}
	}
	return nil
}

// DeleteService removes the virtual service and the VIP (best effort).
func (b *ExecLB) DeleteService(ctx context.Context, vip string, port int, protocol, bridge string) error {
	proto := ipvsProtoFlag(protocol)
	_, _ = b.run(ctx, "ipvsadm", "-D", proto, fmt.Sprintf("%s:%d", vip, port))
	_, _ = b.run(ctx, "ip", "addr", "del", vip+"/32", "dev", bridge)
	return nil
}

// parseRealServers extracts the "ip:port" of each real server from the output of
// `ipvsadm -Ln <proto> <service>`.
func parseRealServers(out string) map[string]bool {
	servers := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "->" {
			servers[fields[1]] = true
		}
	}
	return servers
}

// LoadBalancerRegistry is the typed store of load balancers.
type LoadBalancerRegistry = registry.Registry[resource.LoadBalancerSpec, resource.LoadBalancerStatus]

// LBBackendRegistry is the typed store of load-balancer backends.
type LBBackendRegistry = registry.Registry[resource.LBBackendSpec, resource.LBBackendStatus]

// LoadBalancerReconciler realizes a load balancer as an IPVS virtual service on
// a VIP attached to the VPC bridge, with real servers drawn from its backends.
type LoadBalancerReconciler struct {
	reg      *LoadBalancerRegistry
	backends *LBBackendRegistry
	computes *ComputeRegistry
	vpcs     *VPCRegistry
	backend  LoadBalancerBackend
	// publicIPs is set on an edge: the host then also serves each load
	// balancer's public address. servedPublic is what this host serves, per
	// load balancer - the status is shared by every edge, so it cannot tell
	// one edge what it still has to remove.
	publicIPs    *IPAddressRegistry
	servedPublic map[string]string
}

// NewLoadBalancerReconciler returns a reconciler backed by the LB and backend
// stores, the compute and VPC stores and the IPVS backend.
func NewLoadBalancerReconciler(reg *LoadBalancerRegistry, backends *LBBackendRegistry, computes *ComputeRegistry, vpcs *VPCRegistry, backend LoadBalancerBackend) *LoadBalancerReconciler {
	return &LoadBalancerReconciler{reg: reg, backends: backends, computes: computes, vpcs: vpcs, backend: backend}
}

// AsEdge makes the reconciler serve each load balancer's public address too,
// as an edge facing the public block does: the address goes on the loopback
// and gets the same virtual service as the VIP, so traffic routed to the edge
// for it reaches the backends, full-NAT included.
func (r *LoadBalancerReconciler) AsEdge(publicIPs *IPAddressRegistry) *LoadBalancerReconciler {
	r.publicIPs, r.servedPublic = publicIPs, map[string]string{}
	return r
}

// Name identifies the reconcile pass.
func (r *LoadBalancerReconciler) Name() string { return resource.KindLoadBalancer }

// ReconcileAll reconciles every load balancer, collecting per-LB errors.
func (r *LoadBalancerReconciler) ReconcileAll(ctx context.Context) error {
	lbs, err := r.reg.List()
	if err != nil {
		return fmt.Errorf("manager: list load balancers: %w", err)
	}
	var errs []error
	for i := range lbs {
		uid := lbs[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("load balancer %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the load balancer identified by uid in line with its spec.
func (r *LoadBalancerReconciler) Reconcile(ctx context.Context, uid string) error {
	lb, err := r.reg.Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load lb %q: %w", uid, err)
	}
	if lb.Metadata.IsDeleting() {
		return r.finalize(ctx, lb)
	}
	return r.ensure(ctx, lb)
}

func (r *LoadBalancerReconciler) ensure(ctx context.Context, lb *resource.LoadBalancer) error {
	if !lb.Metadata.HasFinalizer(resource.LoadBalancerFinalizer) {
		lb.Metadata.AddFinalizer(resource.LoadBalancerFinalizer)
	}

	vpc, err := r.vpcs.Get(lb.Spec.VPCID)
	if errors.Is(err, state.ErrNotFound) {
		err = fmt.Errorf("vpc %q not found", lb.Spec.VPCID)
		lb.Status.SetPhase(resource.PhaseError, "VPCError", err.Error())
		_ = r.reg.Put(lb)
		return err
	}
	if err != nil {
		return fmt.Errorf("manager: load vpc %q: %w", lb.Spec.VPCID, err)
	}
	if vpc.Status.BridgeName == "" {
		lb.Status.SetPhase(resource.PhasePending, "WaitingForVPC", "vpc bridge not ready")
		return r.reg.Put(lb)
	}

	vip := lb.Status.Address
	if vip == "" {
		vip = lb.Spec.Address
	}
	if vip == "" {
		used, uErr := r.usedAddresses(lb.Metadata.UID)
		if uErr != nil {
			return uErr
		}
		vip, err = allocateIP(vpc.Spec.CIDR, used)
		if err != nil {
			lb.Status.SetPhase(resource.PhaseError, "AllocError", err.Error())
			_ = r.reg.Put(lb)
			return err
		}
	}

	servers, err := r.realServers(lb.Metadata.UID)
	if err != nil {
		lb.Status.SetPhase(resource.PhaseError, "BackendError", err.Error())
		_ = r.reg.Put(lb)
		return err
	}

	lb.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "configuring service")
	if err := r.backend.EnsureService(ctx, vip, lb.Spec.Port, lb.Spec.Protocol, lb.Spec.Algorithm, vpc.Status.BridgeName, nodePortName(lb.Spec.VPCID), servers); err != nil {
		lb.Status.SetPhase(resource.PhaseError, "IPVSError", err.Error())
		_ = r.reg.Put(lb)
		return err
	}

	if r.publicIPs != nil {
		if err := r.ensurePublic(ctx, lb, nodePortName(lb.Spec.VPCID), servers); err != nil {
			lb.Status.SetPhase(resource.PhaseError, "PublicAddressError", err.Error())
			_ = r.reg.Put(lb)
			return err
		}
	}

	lb.Status.Address = vip
	lb.Status.ServiceID = fmt.Sprintf("%s:%d", vip, lb.Spec.Port)
	lb.Status.MarkReconciled(lb.Metadata.Generation)
	lb.Status.SetPhase(resource.PhaseReady, "Serving", "virtual service ready")
	if err := r.reg.Put(lb); err != nil {
		return fmt.Errorf("manager: save lb %q: %w", lb.Metadata.UID, err)
	}
	r.markBackends(lb.Metadata.UID)
	return nil
}

func (r *LoadBalancerReconciler) finalize(ctx context.Context, lb *resource.LoadBalancer) error {
	if lb.Metadata.HasFinalizer(resource.LoadBalancerFinalizer) {
		vip := lb.Status.Address
		if vip == "" {
			vip = lb.Spec.Address
		}
		bridge := ""
		if vpc, err := r.vpcs.Get(lb.Spec.VPCID); err == nil {
			bridge = vpc.Status.BridgeName
		}
		if vip != "" {
			if err := r.backend.DeleteService(ctx, vip, lb.Spec.Port, lb.Spec.Protocol, bridge); err != nil {
				lb.Status.SetPhase(resource.PhaseError, "IPVSError", err.Error())
				_ = r.reg.Put(lb)
				return err
			}
		}
		if r.publicIPs != nil {
			for _, pub := range []string{r.servedPublic[lb.Metadata.UID], lb.Status.PublicAddress} {
				if pub != "" {
					if err := r.backend.DeleteService(ctx, pub, lb.Spec.Port, lb.Spec.Protocol, "lo"); err != nil {
						return err
					}
				}
			}
			delete(r.servedPublic, lb.Metadata.UID)
		}
		lb.Metadata.RemoveFinalizer(resource.LoadBalancerFinalizer)
		lb.Status.SetPhase(resource.PhaseDeleting, "Deleting", "service removed")
		if err := r.reg.Put(lb); err != nil {
			return fmt.Errorf("manager: save lb %q: %w", lb.Metadata.UID, err)
		}
	}
	if len(lb.Metadata.Finalizers) == 0 {
		if err := r.reg.Delete(lb.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete lb %q: %w", lb.Metadata.UID, err)
		}
	}
	return nil
}

// realServers resolves the live backends of an LB into real servers, skipping
// any whose compute is not yet running.
func (r *LoadBalancerReconciler) realServers(lbID string) ([]LBRealServer, error) {
	all, err := r.backends.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list lb backends: %w", err)
	}
	var servers []LBRealServer
	for i := range all {
		be := &all[i]
		if be.Metadata.IsDeleting() || be.Spec.LBID != lbID {
			continue
		}
		c, cErr := r.computes.Get(be.Spec.ComputeID)
		if cErr != nil || c.Status.IP == "" {
			continue
		}
		servers = append(servers, LBRealServer{IP: c.Status.IP, Port: be.Spec.Port, Weight: be.Spec.Weight})
	}
	return servers, nil
}

// markBackends records the resolved real-server address and phase on each live
// backend of the LB.
func (r *LoadBalancerReconciler) markBackends(lbID string) {
	all, err := r.backends.List()
	if err != nil {
		return
	}
	for i := range all {
		be := &all[i]
		if be.Metadata.IsDeleting() || be.Spec.LBID != lbID {
			continue
		}
		ip := ""
		if c, cErr := r.computes.Get(be.Spec.ComputeID); cErr == nil {
			ip = c.Status.IP
		}
		phase := resource.PhasePending
		reason, msg := "WaitingForCompute", "backend compute not ready"
		if ip != "" {
			phase, reason, msg = resource.PhaseReady, "Attached", "backend attached"
		}
		if be.Status.Phase == phase && be.Status.RealServerIP == ip {
			continue
		}
		be.Status.RealServerIP = ip
		be.Status.SetPhase(phase, reason, msg)
		if phase == resource.PhaseReady {
			be.Status.MarkReconciled(be.Metadata.Generation)
		}
		_ = r.backends.Put(be)
	}
}

// usedAddresses collects the addresses already taken by other LBs and computes,
// so an auto-assigned VIP does not collide.
func (r *LoadBalancerReconciler) usedAddresses(excludeLB string) (map[string]bool, error) {
	used := make(map[string]bool)
	lbs, err := r.reg.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list load balancers: %w", err)
	}
	for i := range lbs {
		if lbs[i].Metadata.UID == excludeLB {
			continue
		}
		if a := lbs[i].Status.Address; a != "" {
			used[a] = true
		}
	}
	computes, err := r.computes.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list computes: %w", err)
	}
	for i := range computes {
		if ip := computes[i].Status.IP; ip != "" {
			used[ip] = true
		}
	}
	return used, nil
}

// ensureFullNAT masquerades the connections IPVS forwards through nodePort to
// this host's address there. In plain IPVS-NAT a backend answers the client
// directly, which bypasses the director whenever the client shares the
// backend's subnet or the backend runs on another host. net.ipv4.vs.conntrack
// exposes IPVS connections to netfilter so the MASQUERADE applies; it only
// exists once ip_vs is loaded, hence after the virtual service is created.
func (b *ExecLB) ensureFullNAT(ctx context.Context, nodePort string) error {
	if out, err := b.run(ctx, "sysctl", "-w", "net.ipv4.vs.conntrack=1"); err != nil {
		return fmt.Errorf("manager: enable ipvs conntrack: %w: %s", err, strings.TrimSpace(out))
	}
	rule := []string{"POSTROUTING", "-o", nodePort, "-m", "ipvs", "--ipvs", "-j", "MASQUERADE"}
	if _, err := b.run(ctx, "iptables", append([]string{"-t", "nat", "-C"}, rule...)...); err == nil {
		return nil
	}
	if out, err := b.run(ctx, "iptables", append([]string{"-t", "nat", "-A"}, rule...)...); err != nil {
		return fmt.Errorf("manager: masquerade ipvs via %s: %w: %s", nodePort, err, strings.TrimSpace(out))
	}
	return nil
}

// ensurePublic serves the load balancer's public address on this edge, or
// stops serving the one it no longer names. A public ip_address that is not
// resolved yet is not an error: the VIP keeps serving and the next pass
// picks the address up.
func (r *LoadBalancerReconciler) ensurePublic(ctx context.Context, lb *resource.LoadBalancer, nodePort string, servers []LBRealServer) error {
	want := ""
	if id := lb.Spec.PublicIPID; id != "" {
		ip, err := r.publicIPs.Get(id)
		if errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("public ip address %q not found", id)
		}
		if err != nil {
			return fmt.Errorf("manager: load ip address %q: %w", id, err)
		}
		if ip.Spec.Type != "public" {
			return fmt.Errorf("ip address %q is not public", id)
		}
		want = ip.Status.Address
	}
	if old := r.servedPublic[lb.Metadata.UID]; old != "" && old != want {
		if err := r.backend.DeleteService(ctx, old, lb.Spec.Port, lb.Spec.Protocol, "lo"); err != nil {
			return err
		}
		delete(r.servedPublic, lb.Metadata.UID)
	}
	if want != "" {
		if err := r.backend.EnsureService(ctx, want, lb.Spec.Port, lb.Spec.Protocol, lb.Spec.Algorithm, "lo", nodePort, servers); err != nil {
			return err
		}
		r.servedPublic[lb.Metadata.UID] = want
	}
	lb.Status.PublicAddress = want
	return nil
}
