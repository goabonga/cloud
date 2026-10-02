// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Not parallel: the umask is process-wide, so the test sets the agent unit's
// UMask=0077 and restores it before any parallel test resumes.
func TestExtractTarKeepsModesUnderARestrictiveUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range []tar.Header{
		{Name: "usr/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "usr/share/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "usr/share/nginx/html/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "usr/share/nginx/html/index.html", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5},
		{Name: "usr/bin/tool", Typeflag: tar.TypeReg, Mode: 0o755, Size: 5},
	} {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("header %s: %v", h.Name, err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("hello"))
		}
	}
	_ = tw.Close()

	dest := t.TempDir()
	if err := extractTar(&buf, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for path, want := range map[string]os.FileMode{
		"usr":                             0o755,
		"usr/share/nginx":                 0o755, // created implicitly, no tar entry
		"usr/share/nginx/html":            0o755,
		"usr/share/nginx/html/index.html": 0o644,
		"usr/bin/tool":                    0o755,
	} {
		info, err := os.Stat(filepath.Join(dest, path))
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s: mode %o, want %o", path, got, want)
		}
	}
}
