// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"io"
	"net/http"
	"testing"
)

func TestBuildCloudInitSeedMinimalCloudConfig(t *testing.T) {
	seed := buildCloudInitSeed(MicroVMRequest{UID: "vm-1", Hostname: "web-1", SSHAuthorizedKey: "ssh-ed25519 AAAA key"})

	wantMeta := "instance-id: vm-1\nlocal-hostname: web-1\n"
	if seed.MetaData != wantMeta {
		t.Fatalf("MetaData = %q, want %q", seed.MetaData, wantMeta)
	}
	wantUser := "#cloud-config\nhostname: web-1\nssh_authorized_keys:\n  - ssh-ed25519 AAAA key\n"
	if seed.UserData != wantUser {
		t.Fatalf("UserData = %q, want %q", seed.UserData, wantUser)
	}
}

func TestBuildCloudInitSeedFallsBackToUIDAsHostname(t *testing.T) {
	seed := buildCloudInitSeed(MicroVMRequest{UID: "vm-1"})
	wantMeta := "instance-id: vm-1\nlocal-hostname: vm-1\n"
	if seed.MetaData != wantMeta {
		t.Fatalf("MetaData = %q, want %q", seed.MetaData, wantMeta)
	}
	if seed.UserData != "#cloud-config\n" {
		t.Fatalf("UserData = %q, want a bare cloud-config header", seed.UserData)
	}
}

func TestBuildCloudInitSeedUserDataOverride(t *testing.T) {
	seed := buildCloudInitSeed(MicroVMRequest{UID: "vm-1", Hostname: "web-1", UserData: "#!/bin/sh\necho hi\n"})
	if seed.UserData != "#!/bin/sh\necho hi\n" {
		t.Fatalf("UserData = %q, want the override verbatim", seed.UserData)
	}
	// Even overridden, meta-data still identifies the instance.
	if seed.MetaData != "instance-id: vm-1\nlocal-hostname: web-1\n" {
		t.Fatalf("MetaData = %q", seed.MetaData)
	}
}

func TestCloudInitCmdline(t *testing.T) {
	got := cloudInitCmdline(MicroVMRequest{UID: "vm-1", Gateway: "10.0.1.1"})
	want := "ds=nocloud-net;s=http://10.0.1.1:8912/vm-1/"
	if got != want {
		t.Fatalf("cloudInitCmdline() = %q, want %q", got, want)
	}
}

// TestCloudInitSeedsServesAndForgets drives the real HTTP listener (on an
// OS-assigned port) end to end: a registered seed answers meta-data and
// user-data, an unknown instance 404s, and a removed seed 404s too.
func TestCloudInitSeedsServesAndForgets(t *testing.T) {
	c := newCloudInitSeeds(0)
	if err := c.ensureStarted(); err != nil {
		t.Fatalf("ensureStarted: %v", err)
	}
	addr := c.addr
	if err := c.ensureStarted(); err != nil || c.addr != addr {
		t.Fatalf("ensureStarted should be a no-op once started: err=%v addr=%q, want %q", err, c.addr, addr)
	}

	c.set("vm-1", cloudInitSeed{MetaData: "instance-id: vm-1\n", UserData: "#cloud-config\n"})

	meta := getBody(t, "http://"+c.addr+"/vm-1/meta-data")
	if meta != "instance-id: vm-1\n" {
		t.Fatalf("meta-data = %q", meta)
	}
	user := getBody(t, "http://"+c.addr+"/vm-1/user-data")
	if user != "#cloud-config\n" {
		t.Fatalf("user-data = %q", user)
	}
	vendor := getStatus(t, "http://"+c.addr+"/vm-1/vendor-data")
	if vendor != http.StatusOK {
		t.Fatalf("vendor-data status = %d, want 200", vendor)
	}

	if status := getStatus(t, "http://"+c.addr+"/vm-unknown/meta-data"); status != http.StatusNotFound {
		t.Fatalf("unknown instance status = %d, want 404", status)
	}

	c.remove("vm-1")
	if status := getStatus(t, "http://"+c.addr+"/vm-1/meta-data"); status != http.StatusNotFound {
		t.Fatalf("removed instance status = %d, want 404", status)
	}
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) // #nosec G107 -- test-owned loopback URL
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", url, err)
	}
	return string(data)
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url) // #nosec G107 -- test-owned loopback URL
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}
