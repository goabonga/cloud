// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// DiskFileRegistry is the typed store the disk file reconciler reads and writes.
type DiskFileRegistry = registry.Registry[resource.DiskFileSpec, resource.DiskFileStatus]

// defaultDiskFileMode is the mode of a disk file whose spec names none.
const defaultDiskFileMode os.FileMode = 0o644

// DiskFileWriter writes content at path beneath root.
type DiskFileWriter interface {
	// WriteFile creates or updates root/path with content and mode, creating
	// missing directories, and reports whether the content changed. It never
	// resolves the path outside root.
	WriteFile(root, path string, content []byte, mode os.FileMode) (bool, error)
}

// DiskFileReconciler writes each disk file into its disk through the mounts of
// the instances on this host that attach the disk read-write: an agent only
// reaches a disk where it has mounted it, and an instance sees the file at
// <mount path>/<path>. Hosts with no such instance leave the file alone.
// Deleting a disk file leaves its content on the disk, as with any file
// written there.
type DiskFileReconciler struct {
	reg      *DiskFileRegistry
	computes *ComputeRegistry
	w        DiskFileWriter
	nodeName string
}

// NewDiskFileReconciler returns a disk file pass for the node named nodeName;
// an empty nodeName takes every instance as local, as on a single host.
func NewDiskFileReconciler(reg *DiskFileRegistry, computes *ComputeRegistry, w DiskFileWriter, nodeName string) *DiskFileReconciler {
	return &DiskFileReconciler{reg: reg, computes: computes, w: w, nodeName: nodeName}
}

// Name identifies the reconcile pass.
func (r *DiskFileReconciler) Name() string { return resource.KindDiskFile }

// ReconcileAll writes every disk file into the local mounts of its disk.
func (r *DiskFileReconciler) ReconcileAll(_ context.Context) error {
	files, err := r.reg.List()
	if err != nil {
		return fmt.Errorf("manager: list disk files: %w", err)
	}
	computes, err := r.computes.List()
	if err != nil {
		return fmt.Errorf("manager: list computes: %w", err)
	}
	var errs []error
	for i := range files {
		f := &files[i]
		if f.Metadata.IsDeleting() {
			continue
		}
		mounts := r.mountsOf(computes, f.Spec.DiskID)
		if len(mounts) == 0 {
			continue
		}
		if err := r.write(f, mounts); err != nil {
			errs = append(errs, fmt.Errorf("disk file %s: %w", f.Metadata.UID, err))
		}
	}
	return errors.Join(errs...)
}

// mountsOf returns where the local, running instances mount diskID read-write.
func (r *DiskFileReconciler) mountsOf(computes []resource.Compute, diskID string) []string {
	var mounts []string
	for i := range computes {
		c := &computes[i]
		if c.Metadata.IsDeleting() || c.Status.Rootfs == "" {
			continue
		}
		if r.nodeName != "" && c.Status.NodeName != r.nodeName {
			continue
		}
		for _, d := range c.Spec.Disks {
			if d.DiskID == diskID && !d.ReadOnly {
				mounts = append(mounts, filepath.Join(c.Status.Rootfs, filepath.Clean("/"+d.MountPath)))
			}
		}
	}
	return mounts
}

func (r *DiskFileReconciler) write(f *resource.DiskFile, mounts []string) error {
	mode, err := diskFileMode(f.Spec.Mode)
	if err != nil {
		f.Status.SetPhase(resource.PhaseError, "BadMode", err.Error())
		_ = r.reg.Put(f)
		return err
	}
	changed := false
	for _, root := range mounts {
		c, err := r.w.WriteFile(root, f.Spec.Path, []byte(f.Spec.Content), mode)
		if err != nil {
			f.Status.SetPhase(resource.PhaseError, "WriteError", err.Error())
			_ = r.reg.Put(f)
			return err
		}
		changed = changed || c
	}
	// Only touch the store when something moved: the pass runs every tick.
	if !changed && f.Status.IsConverged(f.Metadata.Generation) {
		return nil
	}
	f.Status.MarkReconciled(f.Metadata.Generation)
	f.Status.SetPhase(resource.PhaseReady, "Written", fmt.Sprintf("written to %d mount(s)", len(mounts)))
	if err := r.reg.Put(f); err != nil {
		return fmt.Errorf("manager: save disk file %q: %w", f.Metadata.UID, err)
	}
	return nil
}

// diskFileMode parses an octal permission string such as "0644".
func diskFileMode(s string) (os.FileMode, error) {
	if s == "" {
		return defaultDiskFileMode, nil
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil || v > 0o777 {
		return 0, fmt.Errorf("manager: disk file mode %q is not an octal permission such as 0644", s)
	}
	return os.FileMode(v), nil
}

// FSDiskFileWriter writes disk files beneath a mount. An instance controls
// the contents of its disks, so any component of the path may be a symlink it
// planted. The path is cleaned against the mount root, so it holds no "..",
// and walked one component at a time with openat(2) and O_NOFOLLOW, so a
// symlink anywhere on it fails the write instead of steering the agent into a
// host path.
//
// openat2(2) with RESOLVE_BENEATH would express this in one call, but the
// agent's unit sets RestrictSUIDSGID=, and systemd answers openat2 with ENOSYS
// under it (its mode sits in a struct the seccomp filter cannot inspect).
type FSDiskFileWriter struct{}

// WriteFile implements DiskFileWriter.
func (FSDiskFileWriter) WriteFile(root, path string, content []byte, mode os.FileMode) (bool, error) {
	file, err := openFileBeneath(root, path, unix.O_RDWR|unix.O_CREAT, mode)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()

	// The umask narrows the mode given at creation; set it explicitly.
	if err := file.Chmod(mode.Perm()); err != nil {
		return false, fmt.Errorf("manager: disk file %s: chmod: %w", path, err)
	}
	current, err := readAll(file)
	if err != nil {
		return false, fmt.Errorf("manager: disk file %s: read: %w", path, err)
	}
	if string(current) == string(content) {
		return false, nil
	}
	if err := file.Truncate(0); err != nil {
		return false, fmt.Errorf("manager: disk file %s: truncate: %w", path, err)
	}
	if _, err := file.WriteAt(content, 0); err != nil {
		return false, fmt.Errorf("manager: disk file %s: write: %w", path, err)
	}
	return true, nil
}

// openFileBeneath opens root/path with flags, creating missing directories
// along it when flags hold O_CREAT, and refuses a symlink on any component of
// the path.
func openFileBeneath(root, path string, flags int, mode os.FileMode) (*os.File, error) {
	rel := strings.TrimPrefix(filepath.Clean("/"+path), "/")
	if rel == "" {
		return nil, fmt.Errorf("manager: disk file path %q names the disk's root", path)
	}
	dirfd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("manager: open mount %s: %w", root, err)
	}
	defer func() { _ = unix.Close(dirfd) }()

	parts := strings.Split(rel, "/")
	for _, dir := range parts[:len(parts)-1] {
		next, err := openDirBeneath(dirfd, dir, flags&unix.O_CREAT != 0)
		if err != nil {
			return nil, fmt.Errorf("manager: disk file %s: %w", path, err)
		}
		_ = unix.Close(dirfd)
		dirfd = next
	}

	fd, err := unix.Openat(dirfd, parts[len(parts)-1], flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
	if err != nil {
		return nil, fmt.Errorf("manager: disk file %s: %w", path, err)
	}
	return os.NewFile(uintptr(fd), filepath.Join(root, rel)), nil
}

// openDirBeneath opens (creating it 0755 when create is set) the directory
// name directly beneath dirfd, without following a symlink.
func openDirBeneath(dirfd int, name string, create bool) (int, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(dirfd, name, flags, 0)
	if create && errors.Is(err, unix.ENOENT) {
		if err := unix.Mkdirat(dirfd, name, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, err
		}
		if fd, err = unix.Openat(dirfd, name, flags, 0); err != nil {
			return -1, err
		}
		// A new directory: correct the umask the way extraction does.
		if err := unix.Fchmod(fd, 0o755); err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
	}
	return fd, err
}

// readAll reads f from its start.
func readAll(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, info.Size())
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf, nil
}
