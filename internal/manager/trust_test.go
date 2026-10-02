// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

const debianBundle = "etc/ssl/certs/ca-certificates.crt"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFSTrustWriterKeepsTheImageCAsAndReplacesItsOwnBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundle := filepath.Join(root, debianBundle)
	writeFile(t, bundle, "IMAGE CA\n")
	w := manager.FSTrustWriter{}

	if changed, err := w.Sync(root, []byte("CA ONE\n")); err != nil || !changed {
		t.Fatalf("first sync: changed=%v err=%v", changed, err)
	}
	if changed, err := w.Sync(root, []byte("CA ONE\n")); err != nil || changed {
		t.Fatalf("same CAs: changed=%v err=%v", changed, err)
	}
	if _, err := w.Sync(root, []byte("CA TWO\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(bundle)
	want := "IMAGE CA\n# BEGIN infra-agent trusted CAs\nCA TWO\n# END infra-agent trusted CAs\n"
	if string(got) != want {
		t.Fatalf("bundle = %q, want %q", got, want)
	}
	if _, err := w.Sync(root, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(bundle); string(got) != "IMAGE CA\n" {
		t.Fatalf("no CAs left: bundle = %q, want the image's alone", got)
	}
}

func TestFSTrustWriterCreatesTheBundleOnlyWhenTheImageHasNone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	w := manager.FSTrustWriter{}
	if changed, err := w.Sync(root, nil); err != nil || changed {
		t.Fatalf("nothing to trust: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc")); !os.IsNotExist(err) {
		t.Fatalf("nothing to trust must create nothing: %v", err)
	}
	if _, err := w.Sync(root, []byte("CA ONE\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, debianBundle))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("created bundle: %v %v", info, err)
	}
	// The Fedora bundle's directories are not created in a Debian image.
	if _, err := os.Stat(filepath.Join(root, "etc/pki")); !os.IsNotExist(err) {
		t.Fatalf("etc/pki: %v, want absent", err)
	}
}

func TestFSTrustWriterDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	writeFile(t, victim, "host file")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/ssl/certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, debianBundle)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "etc/pki")); err != nil {
		t.Fatal(err)
	}
	if _, err := (manager.FSTrustWriter{}).Sync(root, []byte("CA ONE\n")); err == nil {
		t.Fatal("a symlinked Debian bundle to create must fail the sync")
	}
	if got, _ := os.ReadFile(victim); string(got) != "host file" {
		t.Fatalf("host file overwritten: %q", got)
	}
}

// recordingTrust records the CAs each root is asked to trust.
type recordingTrust map[string]string

func (r recordingTrust) Sync(root string, pems []byte) (bool, error) {
	r[root] = string(pems)
	return true, nil
}

func TestTrustReconcilerTrustsTheGlobalCAsAndThoseOfTheVPC(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	cas := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	subnets := registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet)
	for uid, vpc := range map[string]string{"sn-a": "vpc-a", "sn-b": "vpc-b"} {
		if err := subnets.Put(&resource.Subnet{
			Metadata: resource.ObjectMeta{UID: uid},
			Spec:     resource.SubnetSpec{VPCID: vpc, CIDR: "10.0.1.0/24"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	putCA := func(uid, pem string, spec resource.SSLCASpec) {
		ca := &resource.SSLCA{Metadata: resource.ObjectMeta{UID: uid}, Spec: spec}
		ca.Status.CertPEM = []byte(pem)
		if err := cas.Put(ca); err != nil {
			t.Fatal(err)
		}
	}
	putCA("public-root", "ROOT\n", resource.SSLCASpec{CommonName: "root", Global: true})
	putCA("internal", "INTERNAL\n", resource.SSLCASpec{CommonName: "internal", VPCIDs: []string{"vpc-a"}})
	putCA("issuing", "", resource.SSLCASpec{CommonName: "not issued yet", Global: true})
	putCompute := func(uid, subnet, node, rootfs string) {
		c := &resource.Compute{
			Metadata: resource.ObjectMeta{UID: uid},
			Spec:     resource.ComputeSpec{SubnetID: subnet, Image: "nginx"},
		}
		c.Status.NodeName, c.Status.Rootfs = node, rootfs
		if err := computes.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	putCompute("in-a", "sn-a", "node-a", "/rootfs/in-a")
	putCompute("in-b", "sn-b", "node-a", "/rootfs/in-b")
	putCompute("remote", "sn-a", "node-b", "/rootfs/remote")

	w := recordingTrust{}
	if err := manager.NewTrustReconciler(cas, computes, subnets, w, "node-a").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	want := recordingTrust{"/rootfs/in-a": "INTERNAL\nROOT\n", "/rootfs/in-b": "ROOT\n"}
	if len(w) != len(want) || w["/rootfs/in-a"] != want["/rootfs/in-a"] || w["/rootfs/in-b"] != want["/rootfs/in-b"] {
		t.Fatalf("trusted %q, want %q", w, want)
	}
}
