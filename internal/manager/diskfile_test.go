// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestFSDiskFileWriterWritesWithTheModeAndIsIdempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	w := manager.FSDiskFileWriter{}
	changed, err := w.WriteFile(root, "site/index.html", []byte("web-1\n"), 0o644)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	for path, want := range map[string]os.FileMode{"site": 0o755, "site/index.html": 0o644} {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: %v %v, want %o", path, info.Mode().Perm(), err, want)
		}
	}
	if changed, err := w.WriteFile(root, "site/index.html", []byte("web-1\n"), 0o644); err != nil || changed {
		t.Fatalf("same content: changed=%v err=%v", changed, err)
	}
	if changed, err := w.WriteFile(root, "site/index.html", []byte("web-2\n"), 0o644); err != nil || !changed {
		t.Fatalf("new content: changed=%v err=%v", changed, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "site/index.html")); string(got) != "web-2\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestFSDiskFileWriterStaysBeneathTheMount(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("host file"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Symlinks an instance could plant on its own disk.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	w := manager.FSDiskFileWriter{}
	for _, path := range []string{"escape/victim", "index.html"} {
		if _, err := w.WriteFile(root, path, []byte("pwned"), 0o644); err == nil {
			t.Fatalf("%s: writing through a symlink must fail", path)
		}
	}
	// ".." is cleaned against the mount root, so it cannot climb out either.
	if _, err := w.WriteFile(root, "../../victim", []byte("pwned"), 0o644); err != nil {
		t.Fatalf("dot-dot path: %v", err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "host file" {
		t.Fatalf("host file overwritten: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "victim")); string(got) != "pwned" {
		t.Fatalf("dot-dot path should land at the mount root: %q", got)
	}
}

// recordingWriter records the mounts it is asked to write into.
type recordingWriter struct{ roots []string }

func (w *recordingWriter) WriteFile(root, _ string, _ []byte, _ os.FileMode) (bool, error) {
	w.roots = append(w.roots, root)
	return true, nil
}

func TestDiskFileReconcilerWritesThroughLocalReadWriteMounts(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	files := registry.New[resource.DiskFileSpec, resource.DiskFileStatus](store, resource.KindDiskFile)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	put := func(uid, node, rootfs string, disks ...resource.ComputeDiskRef) {
		c := &resource.Compute{
			Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
			Spec:     resource.ComputeSpec{SubnetID: "sn-1", Image: "nginx", Disks: disks},
		}
		c.Status.NodeName, c.Status.Rootfs = node, rootfs
		if err := computes.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	put("web-1", "node-a", "/rootfs/web-1", resource.ComputeDiskRef{DiskID: "disk-1", MountPath: "/usr/share/nginx/html"})
	put("reader", "node-a", "/rootfs/reader", resource.ComputeDiskRef{DiskID: "disk-1", MountPath: "/data", ReadOnly: true})
	put("remote", "node-b", "/rootfs/remote", resource.ComputeDiskRef{DiskID: "disk-1", MountPath: "/data"})
	put("pending", "node-a", "", resource.ComputeDiskRef{DiskID: "disk-1", MountPath: "/data"})
	if err := files.Put(&resource.DiskFile{
		Metadata: resource.ObjectMeta{UID: "index", Generation: 1},
		Spec:     resource.DiskFileSpec{DiskID: "disk-1", Path: "index.html", Content: "web-1"},
	}); err != nil {
		t.Fatal(err)
	}

	w := &recordingWriter{}
	if err := manager.NewDiskFileReconciler(files, computes, w, "node-a").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !slices.Equal(w.roots, []string{"/rootfs/web-1/usr/share/nginx/html"}) {
		t.Fatalf("wrote into %v, want only web-1's read-write mount on node-a", w.roots)
	}
	got, _ := files.Get("index")
	if !got.Status.IsConverged(1) {
		t.Fatalf("status %+v, want Ready at generation 1", got.Status)
	}
}

func TestDiskFileReconcilerRejectsABadMode(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	files := registry.New[resource.DiskFileSpec, resource.DiskFileStatus](store, resource.KindDiskFile)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: "web-1", Generation: 1}, Spec: resource.ComputeSpec{
		SubnetID: "sn-1", Image: "nginx", Disks: []resource.ComputeDiskRef{{DiskID: "disk-1", MountPath: "/data"}},
	}}
	c.Status.Rootfs = "/rootfs/web-1"
	_ = computes.Put(c)
	_ = files.Put(&resource.DiskFile{
		Metadata: resource.ObjectMeta{UID: "f", Generation: 1},
		Spec:     resource.DiskFileSpec{DiskID: "disk-1", Path: "x", Content: "x", Mode: "0999"},
	})
	if err := manager.NewDiskFileReconciler(files, computes, &recordingWriter{}, "").ReconcileAll(context.Background()); err == nil {
		t.Fatal("a non-octal mode must fail")
	}
	got, _ := files.Get("f")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase %q, want Error", got.Status.Phase)
	}
}

// contentWriter records what each path is written with.
type contentWriter map[string]struct {
	content string
	mode    os.FileMode
}

func (w contentWriter) WriteFile(_, path string, content []byte, mode os.FileMode) (bool, error) {
	w[path] = struct {
		content string
		mode    os.FileMode
	}{string(content), mode}
	return true, nil
}

func TestDiskFileReconcilerRendersCertificateParts(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	files := registry.New[resource.DiskFileSpec, resource.DiskFileStatus](store, resource.KindDiskFile)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	cas := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatal(err)
	}
	svc := ssl.NewService(cas, certs, kek)
	ca, err := svc.CreateCA("ca", "ca", resource.SSLCASpec{CommonName: "test ca"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateCert("web", "web", resource.SSLCertSpec{CAID: "ca", CommonName: "web.internal.demo"}); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := svc.RevealCert("web")
	if err != nil {
		t.Fatal(err)
	}

	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: "web-1", Generation: 1}, Spec: resource.ComputeSpec{
		SubnetID: "sn-1", Image: "nginx", Disks: []resource.ComputeDiskRef{{DiskID: "disk-1", MountPath: "/etc/nginx/tls"}},
	}}
	c.Status.Rootfs = "/rootfs/web-1"
	if err := computes.Put(c); err != nil {
		t.Fatal(err)
	}
	for path, part := range map[string]string{
		"cert.pem":  resource.SSLPartCertificate,
		"chain.pem": resource.SSLPartChain,
		"key.pem":   resource.SSLPartPrivateKey,
	} {
		if err := files.Put(&resource.DiskFile{
			Metadata: resource.ObjectMeta{UID: path, Generation: 1},
			Spec:     resource.DiskFileSpec{DiskID: "disk-1", Path: path, SSLCertID: "web", SSLPart: part},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := files.Put(&resource.DiskFile{
		Metadata: resource.ObjectMeta{UID: "pending", Generation: 1},
		Spec:     resource.DiskFileSpec{DiskID: "disk-1", Path: "pending.pem", SSLCertID: "not-yet", SSLPart: resource.SSLPartCertificate},
	}); err != nil {
		t.Fatal(err)
	}

	w := contentWriter{}
	r := manager.NewDiskFileReconciler(files, computes, w, "").WithCertificates(manager.NewSSLCertificates(svc))
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for path, want := range map[string]struct {
		content string
		mode    os.FileMode
	}{
		"cert.pem":  {string(certPEM), 0o644},
		"chain.pem": {string(certPEM) + string(ca.Status.CertPEM), 0o644},
		"key.pem":   {string(keyPEM), 0o600},
	} {
		if got := w[path]; got != want {
			t.Fatalf("%s written %q mode %o, want %q mode %o", path, got.content, got.mode, want.content, want.mode)
		}
	}
	if _, ok := w["pending.pem"]; ok {
		t.Fatal("a file of a certificate not issued yet must not be written")
	}
	if got, _ := files.Get("pending"); got.Status.Phase != resource.PhasePending {
		t.Fatalf("pending file phase %q, want Pending", got.Status.Phase)
	}
}

func TestDiskFileReconcilerNeedsTheKMSKeyForCertificates(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	files := registry.New[resource.DiskFileSpec, resource.DiskFileStatus](store, resource.KindDiskFile)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: "web-1", Generation: 1}, Spec: resource.ComputeSpec{
		SubnetID: "sn-1", Image: "nginx", Disks: []resource.ComputeDiskRef{{DiskID: "disk-1", MountPath: "/tls"}},
	}}
	c.Status.Rootfs = "/rootfs/web-1"
	_ = computes.Put(c)
	_ = files.Put(&resource.DiskFile{
		Metadata: resource.ObjectMeta{UID: "key", Generation: 1},
		Spec:     resource.DiskFileSpec{DiskID: "disk-1", Path: "key.pem", SSLCertID: "web", SSLPart: resource.SSLPartPrivateKey},
	})
	if err := manager.NewDiskFileReconciler(files, computes, contentWriter{}, "").ReconcileAll(context.Background()); err == nil {
		t.Fatal("a certificate file on an agent without KMS key must fail")
	}
	if got, _ := files.Get("key"); got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase %q, want Error", got.Status.Phase)
	}
}
