// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeDiskBackend struct {
	disks          map[string]bool
	lastPassphrase []byte
	ensureErr      error
}

func newFakeDiskBackend() *fakeDiskBackend {
	return &fakeDiskBackend{disks: make(map[string]bool)}
}

func (f *fakeDiskBackend) EnsureDisk(_ context.Context, req manager.DiskRequest) (string, error) {
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	f.disks[req.UID] = true
	f.lastPassphrase = req.Passphrase
	return "/dev/fake/" + req.UID, nil
}

func (f *fakeDiskBackend) DeleteDisk(_ context.Context, uid string) error {
	delete(f.disks, uid)
	return nil
}

func newDiskRegistry(t *testing.T) *manager.DiskRegistry {
	t.Helper()
	return registry.New[resource.DiskSpec, resource.DiskStatus](state.NewFileStore(t.TempDir()), resource.KindDisk)
}

func seedDisk(t *testing.T, reg *manager.DiskRegistry, uid, kmsKeyID string) {
	t.Helper()
	if err := reg.Put(&resource.Disk{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.DiskSpec{SizeMB: 1024, KMSKeyID: kmsKeyID},
	}); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
}

func TestDiskReconcileUnencrypted(t *testing.T) {
	t.Parallel()

	reg := newDiskRegistry(t)
	be := newFakeDiskBackend()
	seedDisk(t, reg, "disk-1", "")
	rec := manager.NewDiskReconciler(reg, be, nil, "")

	if err := rec.Reconcile(context.Background(), "disk-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := reg.Get("disk-1")
	if !got.Status.IsReady() || got.Status.Encrypted {
		t.Fatalf("unexpected status: %+v", got.Status)
	}
	if got.Status.Path == "" || !got.Metadata.HasFinalizer(resource.DiskFinalizer) {
		t.Fatalf("missing path/finalizer: %+v", got)
	}
	if be.lastPassphrase != nil {
		t.Fatal("unencrypted disk should not pass a passphrase")
	}
}

func TestDiskReconcileEncrypted(t *testing.T) {
	t.Parallel()

	master, _ := crypto.GenerateKey()
	reg := newDiskRegistry(t)
	be := newFakeDiskBackend()
	seedDisk(t, reg, "disk-1", "key-1")
	rec := manager.NewDiskReconciler(reg, be, master, "")

	if err := rec.Reconcile(context.Background(), "disk-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := reg.Get("disk-1")
	if !got.Status.IsReady() || !got.Status.Encrypted {
		t.Fatalf("expected encrypted+ready, got %+v", got.Status)
	}
	if len(be.lastPassphrase) != 32 {
		t.Fatalf("expected a 32-byte derived passphrase, got %d", len(be.lastPassphrase))
	}
}

func TestDiskReconcileEncryptionWithoutMaster(t *testing.T) {
	t.Parallel()

	reg := newDiskRegistry(t)
	seedDisk(t, reg, "disk-1", "key-1")
	rec := manager.NewDiskReconciler(reg, newFakeDiskBackend(), nil, "")

	if err := rec.Reconcile(context.Background(), "disk-1"); err == nil {
		t.Fatal("expected error when encryption is requested without a master key")
	}
	got, _ := reg.Get("disk-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestDiskFinalize(t *testing.T) {
	t.Parallel()

	reg := newDiskRegistry(t)
	be := newFakeDiskBackend()
	seedDisk(t, reg, "disk-1", "")
	rec := manager.NewDiskReconciler(reg, be, nil, "")
	if err := rec.Reconcile(context.Background(), "disk-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := reg.Get("disk-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := reg.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "disk-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := reg.Get("disk-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected record removed, got %v", err)
	}
	if be.disks["disk-1"] {
		t.Fatal("disk should have been deleted from the backend")
	}
	if rec.Name() != resource.KindDisk {
		t.Fatalf("name = %q", rec.Name())
	}
}

func TestDiskNodeScoping(t *testing.T) {
	t.Parallel()

	reg := newDiskRegistry(t)
	mine := &resource.Disk{Metadata: resource.ObjectMeta{UID: "disk-mine", Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1024}}
	mine.Status.NodeName = "node-a"
	if err := reg.Put(mine); err != nil {
		t.Fatalf("seed mine: %v", err)
	}
	other := &resource.Disk{Metadata: resource.ObjectMeta{UID: "disk-other", Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1024}}
	other.Status.NodeName = "node-b"
	if err := reg.Put(other); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	unscheduled := &resource.Disk{Metadata: resource.ObjectMeta{UID: "disk-unscheduled", Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1024}}
	if err := reg.Put(unscheduled); err != nil {
		t.Fatalf("seed unscheduled: %v", err)
	}

	be := newFakeDiskBackend()
	rec := manager.NewDiskReconciler(reg, be, nil, "node-a")
	for _, uid := range []string{"disk-mine", "disk-other", "disk-unscheduled"} {
		if err := rec.Reconcile(context.Background(), uid); err != nil {
			t.Fatalf("reconcile %s: %v", uid, err)
		}
	}
	if !be.disks["disk-mine"] {
		t.Fatal("disk scheduled to node-a should be realized")
	}
	if be.disks["disk-other"] || be.disks["disk-unscheduled"] {
		t.Fatalf("only the disk scheduled to node-a should be realized: %+v", be.disks)
	}
	if got, _ := reg.Get("disk-other"); got.Status.IsReady() {
		t.Fatal("disk on another node should be left alone")
	}
	if got, _ := reg.Get("disk-unscheduled"); got.Status.IsReady() {
		t.Fatal("unscheduled disk should be left alone")
	}
}

func TestExecDiskBackendCommands(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	be := manager.NewExecDiskBackendWithRunner(t.TempDir(), rec.run)
	ctx := context.Background()

	// Unencrypted: a single mkfs on the image.
	if _, err := be.EnsureDisk(ctx, manager.DiskRequest{UID: "disk-plain", SizeMB: 16}); err != nil {
		t.Fatalf("ensure plain: %v", err)
	}
	if !anyCallHas(rec.calls, "mkfs.ext4") {
		t.Fatalf("expected mkfs, got %v", rec.calls)
	}

	// Encrypted: luksFormat, open and mkfs on the mapper.
	rec.calls = nil
	path, err := be.EnsureDisk(ctx, manager.DiskRequest{UID: "disk-enc", SizeMB: 16, Passphrase: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatalf("ensure encrypted: %v", err)
	}
	if path != "/dev/mapper/infra-disk-enc" {
		t.Fatalf("path = %q", path)
	}
	if !anyCallHas(rec.calls, "luksFormat") || !anyCallHas(rec.calls, "open") {
		t.Fatalf("expected cryptsetup calls, got %v", rec.calls)
	}
}

func anyCallHas(calls [][]string, token string) bool {
	for _, c := range calls {
		for _, a := range c {
			if a == token {
				return true
			}
		}
	}
	return false
}
