// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/manager"
)

// TestNativeDNSResolver serves a VPC's resolver from the agent on a real
// bridge address and queries it. It needs root and skips otherwise.
func TestNativeDNSResolver(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	ctx := context.Background()
	backend := manager.NewExecBackend()
	const bridge = "br-itest-dns"
	const vpc, addr = "vpc-itest", "10.251.0.1"
	if err := backend.EnsureBridge(ctx, manager.Bridge{Name: bridge}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteBridge(ctx, bridge) })
	if err := backend.EnsureVRF(ctx, vpc, bridge); err != nil {
		t.Fatalf("ensure VRF: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteVRF(ctx, vpc, bridge) })

	be := manager.NewNativeDNS()
	rr, err := manager.ParseRecord("web", "itest.internal", "A", 0, "10.251.1.10")
	if err != nil {
		t.Fatal(err)
	}
	view := &manager.DNSView{Forward: true, Zones: []manager.DNSZone{{Domain: "itest.internal", Records: []dns.RR{rr}}}}
	if err := be.ServeVPC(ctx, vpc, bridge, addr, view); err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = be.StopVPC(ctx, vpc) })

	q := new(dns.Msg)
	q.SetQuestion("web.itest.internal.", dns.TypeA)
	client := &dns.Client{Timeout: time.Second, Dialer: &net.Dialer{
		Control: func(_, _ string, conn syscall.RawConn) error {
			var socketErr error
			controlErr := conn.Control(func(fd uintptr) {
				socketErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, "vrf-"+vpc) // #nosec G115 -- kernel socket descriptors fit int
			})
			if controlErr != nil {
				return controlErr
			}
			return socketErr
		},
	}}
	var resp *dns.Msg
	for try := 0; try < 20; try++ {
		if resp, _, err = client.Exchange(q, addr+":53"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || len(resp.Answer) != 1 || !resp.Authoritative {
		t.Fatalf("query: %v %v", err, resp)
	}
}
