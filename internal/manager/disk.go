// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// DiskRequest describes a disk to provision. A non-empty Passphrase encrypts it
// with dm-crypt (LUKS).
type DiskRequest struct {
	UID        string
	SizeMB     int
	Passphrase []byte
}

// DiskBackend abstracts the host disk operations the agent needs.
type DiskBackend interface {
	// EnsureDisk provisions the disk and returns its device/file path. Idempotent.
	EnsureDisk(ctx context.Context, req DiskRequest) (string, error)
	// DeleteDisk tears the disk down. Removing an absent disk is not an error.
	DeleteDisk(ctx context.Context, uid string) error
}

// ExecDiskBackend provisions disks as backing files, optionally LUKS-encrypted.
// It requires root and cryptsetup at run time.
type ExecDiskBackend struct {
	dir string
	run Runner
}

// NewExecDiskBackend stores backing files under dir.
func NewExecDiskBackend(dir string) *ExecDiskBackend {
	return &ExecDiskBackend{dir: dir, run: defaultRun}
}

// NewExecDiskBackendWithRunner is the test constructor.
func NewExecDiskBackendWithRunner(dir string, run Runner) *ExecDiskBackend {
	return &ExecDiskBackend{dir: dir, run: run}
}

func mapperName(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	name := "infra-" + b.String()
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

// EnsureDisk creates the backing file and filesystem, encrypting it when a
// passphrase is given. Once the backing file exists the call is a no-op.
func (b *ExecDiskBackend) EnsureDisk(ctx context.Context, req DiskRequest) (string, error) {
	img := filepath.Join(b.dir, req.UID+".img")
	mapper := mapperName(req.UID)
	mapperPath := "/dev/mapper/" + mapper

	if _, err := os.Stat(img); err == nil {
		if len(req.Passphrase) > 0 {
			return mapperPath, nil
		}
		return img, nil
	}

	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return "", fmt.Errorf("manager: disk dir: %w", err)
	}
	if err := truncate(img, int64(req.SizeMB)*1024*1024); err != nil {
		return "", err
	}

	if len(req.Passphrase) == 0 {
		if out, err := b.run(ctx, "mkfs.ext4", "-q", "-F", img); err != nil {
			_ = os.Remove(img)
			return "", fmt.Errorf("manager: mkfs %q: %w: %s", req.UID, err, strings.TrimSpace(out))
		}
		return img, nil
	}

	keyFile := img + ".key"
	if err := os.WriteFile(keyFile, req.Passphrase, 0o600); err != nil {
		return "", fmt.Errorf("manager: disk keyfile: %w", err)
	}
	defer func() { _ = os.Remove(keyFile) }()

	if out, err := b.run(ctx, "cryptsetup", "luksFormat", "--batch-mode", "--key-file", keyFile, img); err != nil {
		_ = os.Remove(img)
		return "", fmt.Errorf("manager: luksFormat %q: %w: %s", req.UID, err, strings.TrimSpace(out))
	}
	if out, err := b.run(ctx, "cryptsetup", "open", "--type", "luks", "--key-file", keyFile, img, mapper); err != nil {
		return "", fmt.Errorf("manager: luks open %q: %w: %s", req.UID, err, strings.TrimSpace(out))
	}
	if out, err := b.run(ctx, "mkfs.ext4", "-q", mapperPath); err != nil {
		return "", fmt.Errorf("manager: mkfs %q: %w: %s", req.UID, err, strings.TrimSpace(out))
	}
	return mapperPath, nil
}

// DeleteDisk closes the LUKS mapping (if any) and removes the backing file.
func (b *ExecDiskBackend) DeleteDisk(ctx context.Context, uid string) error {
	_, _ = b.run(ctx, "cryptsetup", "close", mapperName(uid))
	img := filepath.Join(b.dir, uid+".img")
	if err := os.Remove(img); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("manager: remove disk %q: %w", uid, err)
	}
	_ = os.Remove(img + ".key")
	return nil
}

func truncate(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path built from a controlled UID under the agent's disk dir
	if err != nil {
		return fmt.Errorf("manager: create disk image: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("manager: size disk image: %w", err)
	}
	return nil
}

// DiskRegistry is the typed store the disk reconciler reads and writes.
type DiskRegistry = registry.Registry[resource.DiskSpec, resource.DiskStatus]

// DiskReconciler provisions disks on the host, deriving a per-disk dm-crypt
// passphrase from the master KMS key when the disk references a KMS key.
type DiskReconciler struct {
	reg     *DiskRegistry
	backend DiskBackend
	master  []byte
	// nodeName scopes realization to disks scheduled to this node. When empty
	// the reconciler realizes every disk (single-host mode).
	nodeName string
}

// NewDiskReconciler returns a reconciler. master is the raw KMS master key used
// to derive per-disk encryption keys; a nil master disables disk encryption.
// nodeName scopes realization to disks scheduled to this node; an empty
// nodeName realizes every disk.
func NewDiskReconciler(reg *DiskRegistry, backend DiskBackend, master []byte, nodeName string) *DiskReconciler {
	return &DiskReconciler{reg: reg, backend: backend, master: master, nodeName: nodeName}
}

// Name identifies the reconcile pass.
func (r *DiskReconciler) Name() string { return resource.KindDisk }

// ReconcileAll reconciles every disk, collecting per-disk errors.
func (r *DiskReconciler) ReconcileAll(ctx context.Context) error {
	disks, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list disks: %w", err)
	}
	var errs []error
	for i := range disks {
		uid := disks[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("disk %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile provisions the disk identified by uid.
func (r *DiskReconciler) Reconcile(ctx context.Context, uid string) error {
	disk, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load disk %q: %w", uid, err)
	}
	if r.nodeName != "" && disk.Status.NodeName != r.nodeName {
		// Scheduled to another node (or not yet scheduled); leave it alone.
		return nil
	}
	if disk.Metadata.IsDeleting() {
		return r.finalize(ctx, disk)
	}
	return r.ensure(ctx, disk)
}

func (r *DiskReconciler) ensure(ctx context.Context, disk *resource.Disk) error {
	if disk.Spec.SizeMB > resource.MaxDiskSizeMB {
		return fmt.Errorf("disk: size exceeds admission limit")
	}
	if !disk.Metadata.HasFinalizer(resource.DiskFinalizer) {
		disk.Metadata.AddFinalizer(resource.DiskFinalizer)
	}

	var passphrase []byte
	if disk.Spec.KMSKeyID != "" {
		if len(r.master) == 0 {
			err := fmt.Errorf("disk encryption requested but no KMS master key configured")
			disk.Status.SetPhase(resource.PhaseError, "NoKMSKey", err.Error())
			_ = r.reg.WithContext(ctx).Put(disk)
			return err
		}
		key, err := crypto.DeriveKey(r.master, "disk:"+disk.Spec.KMSKeyID+":"+disk.Metadata.UID, 32)
		if err != nil {
			disk.Status.SetPhase(resource.PhaseError, "KeyError", err.Error())
			_ = r.reg.WithContext(ctx).Put(disk)
			return err
		}
		passphrase = key
	}

	disk.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "provisioning disk")
	path, err := r.backend.EnsureDisk(ctx, DiskRequest{UID: disk.Metadata.UID, SizeMB: disk.Spec.SizeMB, Passphrase: passphrase})
	if err != nil {
		disk.Status.SetPhase(resource.PhaseError, "DiskError", err.Error())
		_ = r.reg.WithContext(ctx).Put(disk)
		return err
	}

	disk.Status.Encrypted = len(passphrase) > 0
	disk.Status.Path = path
	disk.Status.MarkReconciled(disk.Metadata.Generation)
	disk.Status.SetPhase(resource.PhaseReady, "Provisioned", "disk ready")
	if err := r.reg.WithContext(ctx).Put(disk); err != nil {
		return fmt.Errorf("manager: save disk %q: %w", disk.Metadata.UID, err)
	}
	return nil
}

func (r *DiskReconciler) finalize(ctx context.Context, disk *resource.Disk) error {
	if disk.Metadata.HasFinalizer(resource.DiskFinalizer) {
		if err := r.backend.DeleteDisk(ctx, disk.Metadata.UID); err != nil {
			disk.Status.SetPhase(resource.PhaseError, "DiskError", err.Error())
			_ = r.reg.WithContext(ctx).Put(disk)
			return err
		}
		disk.Metadata.RemoveFinalizer(resource.DiskFinalizer)
		disk.Status.SetPhase(resource.PhaseDeleting, "Deleting", "disk removed")
		if err := r.reg.WithContext(ctx).Put(disk); err != nil {
			return fmt.Errorf("manager: save disk %q: %w", disk.Metadata.UID, err)
		}
	}
	if len(disk.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(disk.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete disk %q: %w", disk.Metadata.UID, err)
		}
	}
	return nil
}
