// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// imagePuller pulls OCI images from a registry and extracts them into a
// per-compute rootfs tree, caching pulled layers between calls.
type imagePuller struct {
	cacheDir  string // pulled image tarballs
	rootfsDir string // extracted rootfs, one subdirectory per compute
}

// newImagePuller stores its cache and rootfs trees under stateDir.
func newImagePuller(stateDir string) *imagePuller {
	return &imagePuller{
		cacheDir:  filepath.Join(stateDir, "images"),
		rootfsDir: filepath.Join(stateDir, "rootfs"),
	}
}

// rootfsPath returns the extracted rootfs path for a compute UID.
func (p *imagePuller) rootfsPath(uid string) string {
	return filepath.Join(p.rootfsDir, uid)
}

// mkdirTraversable creates dir with permissions a contained process needs to
// traverse it. These paths hold an OCI rootfs and the agent's image cache under
// the state dir; they are not host-sensitive.
func mkdirTraversable(dir string) error {
	return mkdirAll(dir, 0o755)
}

// The agent's unit runs it under UMask=0077, and the kernel applies the umask
// to the mode of every file and directory it creates: an image extracted
// without correcting for it comes out 0600/0700 and root-only, so a process
// that drops to another user (nginx's workers, for one) cannot read its own
// files. Modes are therefore set explicitly after creation, which the umask
// does not affect, and always through an open descriptor so a symlink planted
// in the rootfs is never followed.

// mkdirAll is os.MkdirAll that sets mode on every directory it creates, the
// missing parents included, instead of leaving them to the umask. Directories
// that already exist keep their mode.
func mkdirAll(dir string, mode os.FileMode) error {
	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("manager: %s exists and is not a directory", dir)
		}
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := mkdirAll(parent, 0o755|mode); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, mode); err != nil && !os.IsExist(err) { // #nosec G301 -- rootfs directories must be traversable by the contained process
		return err
	}
	return chmodDir(dir, mode)
}

// chmodDir sets dir's mode without following a symlink at its path.
func chmodDir(dir string, mode os.FileMode) error {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0) // #nosec G304 -- dir is confined under the rootfs by the caller
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Chmod(mode)
}

// imageConfig holds the runtime configuration extracted from an OCI image.
type imageConfig struct {
	Entrypoint []string
	Cmd        []string
	Env        []string
	WorkingDir string
}

// image returns the named image, loading it from the local cache when present
// and pulling (then caching) it from the registry otherwise.
func (p *imagePuller) image(ref name.Reference) (v1.Image, error) {
	if err := mkdirTraversable(p.cacheDir); err != nil {
		return nil, fmt.Errorf("manager: image cache dir: %w", err)
	}
	cachedTar := filepath.Join(p.cacheDir, sanitizeCacheKey(ref.String())+".tar.gz")
	if _, err := os.Stat(cachedTar); err == nil {
		if img, err := crane.Load(cachedTar); err == nil {
			return img, nil
		}
		_ = os.Remove(cachedTar)
	}
	desc, err := remote.Get(ref)
	if err != nil {
		return nil, fmt.Errorf("manager: pull %s: %w", ref, err)
	}
	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("manager: image %s: %w", ref, err)
	}
	if err := crane.Save(img, ref.String(), cachedTar); err != nil {
		_ = os.Remove(cachedTar)
	}
	return img, nil
}

// config returns the entrypoint, command and environment of an image.
func (p *imagePuller) config(imageRef string) (*imageConfig, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("manager: invalid image reference %q: %w", imageRef, err)
	}
	img, err := p.image(ref)
	if err != nil {
		return nil, err
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("manager: image config %q: %w", imageRef, err)
	}
	return &imageConfig{
		Entrypoint: cfg.Config.Entrypoint,
		Cmd:        cfg.Config.Cmd,
		Env:        cfg.Config.Env,
		WorkingDir: cfg.Config.WorkingDir,
	}, nil
}

// pullAndExtract pulls an OCI image and extracts a flattened rootfs for a
// compute UID, returning the rootfs path.
func (p *imagePuller) pullAndExtract(imageRef, uid string) (string, error) {
	if imageRef == "" {
		return "", fmt.Errorf("manager: image reference is required")
	}
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", fmt.Errorf("manager: invalid image reference %q: %w", imageRef, err)
	}
	img, err := p.image(ref)
	if err != nil {
		return "", err
	}

	rootfs := p.rootfsPath(uid)
	if err := mkdirTraversable(rootfs); err != nil {
		return "", fmt.Errorf("manager: rootfs dir: %w", err)
	}

	reader := mutate.Extract(img)
	defer func() { _ = reader.Close() }()
	if err := extractTar(reader, rootfs); err != nil {
		return "", fmt.Errorf("manager: extract %s: %w", imageRef, err)
	}
	setupRootfs(rootfs)
	return rootfs, nil
}

// cleanupRootfs removes the extracted rootfs for a compute UID.
func (p *imagePuller) cleanupRootfs(uid string) error {
	return os.RemoveAll(p.rootfsPath(uid))
}

// extractTar extracts a flattened image tar stream into destDir. Entries are
// buffered and applied in order (directories, symlinks, files, hardlinks) so
// that directory symlinks such as /bin -> usr/bin exist before files are written
// through them. Whiteouts delete the shadowed path.
func extractTar(reader io.Reader, destDir string) error {
	tr := tar.NewReader(reader)
	clean := filepath.Clean(destDir)

	type entry struct {
		hdr  *tar.Header
		data []byte
	}
	var dirs, symlinks, hardlinks, files []entry

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		target := filepath.Join(destDir, name)
		// Guard against path traversal outside destDir (CWE-22).
		if target != clean && !strings.HasPrefix(target, clean+string(os.PathSeparator)) {
			continue
		}
		base := filepath.Base(name)
		if strings.HasPrefix(base, ".wh.") {
			_ = os.RemoveAll(filepath.Join(filepath.Dir(target), strings.TrimPrefix(base, ".wh.")))
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			dirs = append(dirs, entry{hdr: hdr})
		case tar.TypeSymlink:
			symlinks = append(symlinks, entry{hdr: hdr})
		case tar.TypeLink:
			hardlinks = append(hardlinks, entry{hdr: hdr})
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("tar read file %s: %w", hdr.Name, err)
			}
			files = append(files, entry{hdr: hdr, data: data})
		}
	}

	symlinkTargets := make(map[string]bool, len(symlinks))
	for _, sl := range symlinks {
		symlinkTargets[filepath.Clean(sl.hdr.Name)] = true
	}
	for _, d := range dirs {
		if symlinkTargets[filepath.Clean(d.hdr.Name)] {
			continue
		}
		// #nosec G301 G115 -- container rootfs dirs must be traversable by the
		// contained process; the mode is a permission value from the tar header.
		mode := os.FileMode(d.hdr.Mode).Perm() | 0o755
		dir := filepath.Join(destDir, filepath.Clean(d.hdr.Name))
		if mkdirAll(dir, mode) == nil {
			// Also correct a directory an earlier entry created implicitly.
			_ = chmodDir(dir, mode)
		}
	}
	for _, sl := range symlinks {
		target := filepath.Join(destDir, filepath.Clean(sl.hdr.Name))
		if _, err := os.Stat(filepath.Dir(target)); os.IsNotExist(err) {
			_ = mkdirTraversable(filepath.Dir(target))
		}
		if info, err := os.Lstat(target); err == nil {
			if info.IsDir() {
				_ = os.RemoveAll(target)
			} else {
				_ = os.Remove(target)
			}
		}
		_ = os.Symlink(sl.hdr.Linkname, target)
	}
	for _, f := range files {
		target := filepath.Join(destDir, filepath.Clean(f.hdr.Name))
		if _, err := os.Stat(filepath.Dir(target)); os.IsNotExist(err) {
			_ = mkdirTraversable(filepath.Dir(target))
		}
		_ = os.Remove(target)
		mode := os.FileMode(f.hdr.Mode).Perm() // #nosec G115 -- a file-permission value from the tar header
		// #nosec G304 -- target is confined under destDir by the traversal guard above.
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
		if err != nil {
			continue
		}
		_, _ = out.Write(f.data)
		_ = out.Chmod(mode)
		_ = out.Close()
	}
	for _, hl := range hardlinks {
		target := filepath.Join(destDir, filepath.Clean(hl.hdr.Name))
		linkTarget := filepath.Join(destDir, filepath.Clean(hl.hdr.Linkname))
		if _, err := os.Stat(filepath.Dir(target)); os.IsNotExist(err) {
			_ = mkdirTraversable(filepath.Dir(target))
		}
		_ = os.Remove(target)
		_ = os.Link(linkTarget, target)
	}
	return nil
}

// setupRootfs ensures the extracted rootfs has the minimum structure a container
// process needs and repairs the usrmerge directory symlinks some images ship as
// real directories.
func setupRootfs(rootfs string) {
	for _, d := range []string{"proc", "sys", "dev", "dev/pts", "dev/shm", "tmp", "run", "var", "etc"} {
		_ = mkdirTraversable(filepath.Join(rootfs, d))
	}
	for link, target := range map[string]string{"bin": "usr/bin", "sbin": "usr/sbin", "lib": "usr/lib", "lib64": "usr/lib64"} {
		linkPath := filepath.Join(rootfs, link)
		if _, err := os.Stat(filepath.Join(rootfs, target)); os.IsNotExist(err) {
			continue
		}
		info, err := os.Lstat(linkPath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if entries, _ := os.ReadDir(linkPath); len(entries) == 0 {
				_ = os.Remove(linkPath)
				_ = os.Symlink(target, linkPath)
			}
		}
	}
	resolv := filepath.Join(rootfs, "etc", "resolv.conf")
	if _, err := os.Stat(resolv); os.IsNotExist(err) {
		_ = os.WriteFile(resolv, []byte("nameserver 8.8.8.8\n"), 0o644) // #nosec G306 -- resolv.conf must be readable by the contained resolver
	}
}

// sanitizeCacheKey converts an image reference into a safe cache filename.
func sanitizeCacheKey(ref string) string {
	return strings.NewReplacer("/", "_", ":", "__", "@", "___").Replace(ref)
}
