// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// vmImageCache fetches micro-VM boot images (raw disk files, referenced by
// URL or local path) into a shared cache, then hands each instance its own
// copy-on-write clone so one VM's writes never touch another's or the cache,
// the same role imagePuller plays for compute's OCI rootfs.
type vmImageCache struct {
	cacheDir    string // one fetched/cached image per source, keyed by its hash
	instanceDir string // one disk per micro-VM instance
	httpClient  *http.Client
}

// newVMImageCache stores the shared cache and per-instance disks under
// stateDir.
func newVMImageCache(stateDir string) *vmImageCache {
	return &vmImageCache{
		cacheDir:    filepath.Join(stateDir, "microvm-images"),
		instanceDir: filepath.Join(stateDir, "microvm"),
		httpClient:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// instancePath returns the per-instance disk path for uid.
func (c *vmImageCache) instancePath(uid string) string {
	return filepath.Join(c.instanceDir, uid, "disk.raw")
}

// resolve returns a disk image ready for uid's exclusive use: the cached copy
// of source (fetched or copied in once, shared read-only by every instance
// booting it) is cloned into uid's own file - copy-on-write where the
// filesystem supports it - so writes never reach the cache or another
// instance. Calling it again for the same uid is a no-op.
func (c *vmImageCache) resolve(ctx context.Context, uid, source string) (string, error) {
	dst := c.instancePath(uid)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}

	cached, err := c.ensureCached(ctx, source)
	if err != nil {
		return "", err
	}
	if err := mkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", fmt.Errorf("manager: microvm disk dir for %q: %w", uid, err)
	}
	if err := cloneFile(dst, cached); err != nil {
		return "", fmt.Errorf("manager: clone boot image for %q: %w", uid, err)
	}
	return dst, nil
}

// ensureCached fetches or copies source into the shared cache if not already
// there, keyed by its own hash, and returns the cached path.
func (c *vmImageCache) ensureCached(ctx context.Context, source string) (string, error) {
	key := sha256.Sum256([]byte(source))
	cached := filepath.Join(c.cacheDir, hex.EncodeToString(key[:])+".raw")
	if _, err := os.Stat(cached); err == nil {
		return cached, nil
	}
	if err := mkdirAll(c.cacheDir, 0o750); err != nil {
		return "", fmt.Errorf("manager: image cache dir: %w", err)
	}
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		// A local path: clone it into the cache so a later change to the
		// original is never seen by VMs already cloned from it.
		if err := cloneFile(cached, source); err != nil {
			return "", fmt.Errorf("manager: cache local image %q: %w", source, err)
		}
		return cached, nil
	}
	if err := c.download(ctx, cached, source); err != nil {
		return "", err
	}
	return cached, nil
}

// download fetches url into a temporary file in the cache directory, then
// renames it into place atomically so a concurrent fetch of the same image by
// another reconcile never observes a partial file.
func (c *vmImageCache) download(ctx context.Context, dst, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("manager: fetch %q: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("manager: fetch %q: status %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".download-*")
	if err != nil {
		return fmt.Errorf("manager: create temp file for %q: %w", url, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed into place

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("manager: download %q: %w", url, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("manager: close download of %q: %w", url, err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("manager: chmod download of %q: %w", url, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("manager: finalize download of %q: %w", url, err)
	}
	return nil
}

// cloneFile makes dst a copy-on-write clone of src where the filesystem
// supports it (FICLONE: btrfs, xfs with reflink, overlayfs on one of those),
// falling back to a byte-for-byte copy otherwise.
func cloneFile(dst, src string) error {
	srcF, err := os.Open(src) // #nosec G304 -- agent-managed image cache/source path
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer func() { _ = srcF.Close() }()

	dstF, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- agent-managed path
	if err != nil {
		return fmt.Errorf("create %q: %w", dst, err)
	}
	defer func() { _ = dstF.Close() }()

	if err := unix.IoctlFileClone(int(dstF.Fd()), int(srcF.Fd())); err == nil {
		return nil
	}

	if _, err := srcF.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek %q: %w", src, err)
	}
	if _, err := io.Copy(dstF, srcF); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("copy %q to %q: %w", src, dst, err)
	}
	return nil
}
