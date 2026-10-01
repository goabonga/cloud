// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// SSLCARegistry is the typed store of certificate authorities.
type SSLCARegistry = registry.Registry[resource.SSLCASpec, resource.SSLCAStatus]

// TrustWriter installs CA certificates in an instance's root filesystem.
type TrustWriter interface {
	// Sync makes the trust bundles beneath root hold exactly pems as their
	// platform-managed CAs, and reports whether a bundle changed. Empty pems
	// removes them.
	Sync(root string, pems []byte) (bool, error)
}

// TrustReconciler makes the instances on this host trust the platform's CAs:
// the global ones, which every instance trusts, and those scoped to the
// instance's VPC. The CAs go in the system trust bundle of the instance's
// root filesystem, so TLS clients in the image verify certificates they sign
// with no configuration of their own. The bundle is rewritten every pass, so
// a CA added, scoped elsewhere or deleted reaches running instances on the
// next tick.
type TrustReconciler struct {
	cas      *SSLCARegistry
	computes *ComputeRegistry
	subnets  *SubnetRegistry
	w        TrustWriter
	nodeName string
}

// NewTrustReconciler returns a trust pass for the node named nodeName; an
// empty nodeName takes every instance as local, as on a single host.
func NewTrustReconciler(cas *SSLCARegistry, computes *ComputeRegistry, subnets *SubnetRegistry, w TrustWriter, nodeName string) *TrustReconciler {
	return &TrustReconciler{cas: cas, computes: computes, subnets: subnets, w: w, nodeName: nodeName}
}

// Name identifies the reconcile pass.
func (r *TrustReconciler) Name() string { return "trust" }

// ReconcileAll syncs every local instance's trust bundle.
func (r *TrustReconciler) ReconcileAll(_ context.Context) error {
	cas, err := r.cas.List()
	if err != nil {
		return fmt.Errorf("manager: list ssl cas: %w", err)
	}
	computes, err := r.computes.List()
	if err != nil {
		return fmt.Errorf("manager: list computes: %w", err)
	}
	sort.Slice(cas, func(i, j int) bool { return cas[i].Metadata.UID < cas[j].Metadata.UID })

	var errs []error
	for i := range computes {
		c := &computes[i]
		if c.Metadata.IsDeleting() || c.Status.Rootfs == "" {
			continue
		}
		if r.nodeName != "" && c.Status.NodeName != r.nodeName {
			continue
		}
		vpcID := ""
		if sn, err := r.subnets.Get(c.Spec.SubnetID); err == nil {
			vpcID = sn.Spec.VPCID
		}
		if _, err := r.w.Sync(c.Status.Rootfs, trustedPEMs(cas, vpcID)); err != nil {
			errs = append(errs, fmt.Errorf("compute %s trust: %w", c.Metadata.UID, err))
		}
	}
	return errors.Join(errs...)
}

// trustedPEMs concatenates the certificates of the CAs an instance of vpcID
// trusts: the global ones and those scoped to vpcID.
func trustedPEMs(cas []resource.SSLCA, vpcID string) []byte {
	var out []byte
	for i := range cas {
		ca := &cas[i]
		if ca.Metadata.IsDeleting() || len(ca.Status.CertPEM) == 0 {
			continue
		}
		if !ca.Spec.Global && (vpcID == "" || !slices.Contains(ca.Spec.VPCIDs, vpcID)) {
			continue
		}
		out = append(out, ca.Status.CertPEM...)
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
	}
	return out
}

// The platform's CAs sit in the bundle between these markers, so a pass
// replaces its own block and leaves the image's CAs alone.
const (
	trustBegin = "# BEGIN infra-agent trusted CAs\n"
	trustEnd   = "# END infra-agent trusted CAs\n"
)

// trustBundles are the system bundles of the common distributions, relative
// to the root filesystem: Debian, Ubuntu and Alpine; Fedora and RHEL; SUSE.
var trustBundles = []string{
	"etc/ssl/certs/ca-certificates.crt",
	"etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"etc/ssl/ca-bundle.pem",
}

// FSTrustWriter is the TrustWriter that edits the bundles in place. An
// instance controls its root filesystem, so a bundle is opened the way disk
// files are written: beneath the root, refusing a symlink on the way. A
// bundle that is a symlink - the distribution's own, e.g. /etc/ssl/cert.pem -
// is skipped, its target being one of the bundles edited. With no bundle in
// the image, the Debian one is created.
type FSTrustWriter struct{}

// Sync implements TrustWriter.
func (FSTrustWriter) Sync(root string, pems []byte) (bool, error) {
	changed, found := false, false
	for _, path := range trustBundles {
		c, err := syncBundle(root, path, pems, false)
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			continue
		}
		if err != nil {
			return changed, err
		}
		found = true
		changed = changed || c
	}
	if found || len(pems) == 0 {
		return changed, nil
	}
	return syncBundle(root, trustBundles[0], pems, true)
}

// syncBundle replaces the platform's block in root/path with pems.
func syncBundle(root, path string, pems []byte, create bool) (bool, error) {
	flags := unix.O_RDWR
	if create {
		flags |= unix.O_CREAT
	}
	file, err := openFileBeneath(root, path, flags, 0o644)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	current, err := readAll(file)
	if err != nil {
		return false, fmt.Errorf("manager: trust bundle %s: read: %w", path, err)
	}
	next := spliceTrust(current, pems)
	if bytes.Equal(current, next) {
		return false, nil
	}
	if create {
		// The umask narrows the mode given at creation.
		if err := file.Chmod(0o644); err != nil {
			return false, fmt.Errorf("manager: trust bundle %s: chmod: %w", path, err)
		}
	}
	if err := file.Truncate(0); err != nil {
		return false, fmt.Errorf("manager: trust bundle %s: truncate: %w", path, err)
	}
	if _, err := file.WriteAt(next, 0); err != nil {
		return false, fmt.Errorf("manager: trust bundle %s: write: %w", path, err)
	}
	return true, nil
}

// spliceTrust returns bundle with its platform block, if any, replaced by one
// holding pems, or removed when pems is empty.
func spliceTrust(bundle, pems []byte) []byte {
	rest := bundle
	if b := bytes.Index(bundle, []byte(trustBegin)); b >= 0 {
		if e := bytes.Index(bundle[b:], []byte(trustEnd)); e >= 0 {
			rest = append(slices.Clip(bundle[:b]), bundle[b+e+len(trustEnd):]...)
		}
	}
	if len(pems) == 0 {
		return rest
	}
	out := slices.Clone(rest)
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	out = append(out, trustBegin...)
	out = append(out, pems...)
	return append(out, trustEnd...)
}
