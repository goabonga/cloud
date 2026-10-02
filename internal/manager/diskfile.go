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
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

// DiskFileRegistry is the typed store the disk file reconciler reads and writes.
type DiskFileRegistry = registry.Registry[resource.DiskFileSpec, resource.DiskFileStatus]

// defaultDiskFileMode is the mode of a disk file whose spec names none, and
// defaultKeyFileMode that of a private key.
const (
	defaultDiskFileMode os.FileMode = 0o644
	defaultKeyFileMode  os.FileMode = 0o600
)

// ErrCertificatePending reports a certificate not issued yet.
var ErrCertificatePending = errors.New("manager: certificate not issued yet")

// CertificateSource renders one part of a certificate, as named by
// resource.SSLPart*: the certificate, the chain or the private key.
type CertificateSource interface {
	CertificatePart(certID, part string) ([]byte, error)
}

// SSLCertificates is the CertificateSource reading the platform's
// certificates, decrypting private keys with the KMS key.
type SSLCertificates struct{ svc *ssl.Service }

// NewSSLCertificates returns a source backed by svc.
func NewSSLCertificates(svc *ssl.Service) SSLCertificates { return SSLCertificates{svc: svc} }

// CertificatePart implements CertificateSource. The chain is the certificate
// followed by its CA's, as servers such as nginx expect.
func (s SSLCertificates) CertificatePart(certID, part string) ([]byte, error) {
	cert, err := s.svc.GetCert(certID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && len(cert.Status.CertPEM) == 0) {
		return nil, ErrCertificatePending
	}
	if err != nil {
		return nil, err
	}
	switch part {
	case resource.SSLPartCertificate:
		return cert.Status.CertPEM, nil
	case resource.SSLPartChain:
		ca, err := s.svc.Get(cert.Spec.CAID)
		if err != nil {
			return nil, fmt.Errorf("manager: ca %s of certificate %s: %w", cert.Spec.CAID, certID, err)
		}
		return append(append([]byte{}, cert.Status.CertPEM...), ca.Status.CertPEM...), nil
	case resource.SSLPartPrivateKey:
		_, key, err := s.svc.RevealCert(certID)
		return key, err
	}
	return nil, fmt.Errorf("manager: unknown certificate part %q", part)
}

// CACertificate implements TLSSource.
func (s SSLCertificates) CACertificate(caID string) ([]byte, error) {
	ca, err := s.svc.Get(caID)
	if err != nil {
		return nil, fmt.Errorf("manager: ca %s: %w", caID, err)
	}
	if len(ca.Status.CertPEM) == 0 {
		return nil, fmt.Errorf("manager: ca %s has no certificate yet", caID)
	}
	return ca.Status.CertPEM, nil
}

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
	certs    CertificateSource
}

// NewDiskFileReconciler returns a disk file pass for the node named nodeName;
// an empty nodeName takes every instance as local, as on a single host.
func NewDiskFileReconciler(reg *DiskFileRegistry, computes *ComputeRegistry, w DiskFileWriter, nodeName string) *DiskFileReconciler {
	return &DiskFileReconciler{reg: reg, computes: computes, w: w, nodeName: nodeName}
}

// WithCertificates lets disk files hold parts of the platform's
// certificates. Without it, such a file is in error.
func (r *DiskFileReconciler) WithCertificates(certs CertificateSource) *DiskFileReconciler {
	r.certs = certs
	return r
}

// Name identifies the reconcile pass.
func (r *DiskFileReconciler) Name() string { return resource.KindDiskFile }

// ReconcileAll writes every disk file into the local mounts of its disk.
func (r *DiskFileReconciler) ReconcileAll(ctx context.Context) error {
	files, err := r.reg.WithContext(ctx).List()
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
	mode, err := diskFileMode(f.Spec.Mode, f.Spec.SSLPart == resource.SSLPartPrivateKey)
	if err != nil {
		f.Status.SetPhase(resource.PhaseError, "BadMode", err.Error())
		_ = r.reg.Put(f)
		return err
	}
	content, err := r.content(f)
	if errors.Is(err, ErrCertificatePending) {
		if f.Status.Phase != resource.PhasePending {
			f.Status.SetPhase(resource.PhasePending, "WaitingForCertificate", err.Error())
			_ = r.reg.Put(f)
		}
		return nil
	}
	if err != nil {
		f.Status.SetPhase(resource.PhaseError, "CertificateError", err.Error())
		_ = r.reg.Put(f)
		return err
	}
	changed := false
	for _, root := range mounts {
		c, err := r.w.WriteFile(root, f.Spec.Path, content, mode)
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

// content is what f holds: its literal content, or a part of a certificate.
func (r *DiskFileReconciler) content(f *resource.DiskFile) ([]byte, error) {
	if f.Spec.SSLCertID == "" {
		return []byte(f.Spec.Content), nil
	}
	if r.certs == nil {
		return nil, errors.New("manager: no KMS key on this agent to render certificates")
	}
	return r.certs.CertificatePart(f.Spec.SSLCertID, f.Spec.SSLPart)
}

// diskFileMode parses an octal permission string such as "0644"; an empty
// one is the default mode, narrower for a private key.
func diskFileMode(s string, privateKey bool) (os.FileMode, error) {
	if s == "" && privateKey {
		return defaultKeyFileMode, nil
	}
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
