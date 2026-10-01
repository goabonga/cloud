// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// blocksOf returns the number of 512-byte blocks path actually occupies on
// disk, as opposed to its logical size.
func blocksOf(t *testing.T, path string) int64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return st.Blocks
}

// TestSparseCopyPreservesHoles clones a large, mostly-empty file and checks
// the destination stays sparse (far fewer blocks than its logical size),
// rather than materializing every hole as a literal run of zero bytes.
func TestSparseCopyPreservesHoles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.raw")
	dst := filepath.Join(dir, "dst.raw")

	const size = 64 << 20 // 64MiB, almost entirely a hole
	const chunk = "some boot-sector-looking bytes"

	srcF, err := os.Create(src) // #nosec G304 -- test-owned path
	if err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := srcF.WriteAt([]byte(chunk), 0); err != nil {
		t.Fatalf("write src head: %v", err)
	}
	if _, err := srcF.WriteAt([]byte(chunk), size-int64(len(chunk))); err != nil {
		t.Fatalf("write src tail: %v", err)
	}
	if err := srcF.Close(); err != nil {
		t.Fatalf("close src: %v", err)
	}

	srcBlocks := blocksOf(t, src)
	if srcBlocks*512 >= size/2 {
		t.Skipf("filesystem does not appear to support sparse files (src uses %d blocks for a %d-byte file)", srcBlocks, size)
	}

	srcF, err = os.Open(src) // #nosec G304 -- test-owned path
	if err != nil {
		t.Fatalf("reopen src: %v", err)
	}
	defer func() { _ = srcF.Close() }()
	dstF, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- test-owned path
	if err != nil {
		t.Fatalf("create dst: %v", err)
	}
	defer func() { _ = dstF.Close() }()

	if err := sparseCopy(dstF, srcF); err != nil {
		t.Fatalf("sparseCopy: %v", err)
	}
	if err := dstF.Sync(); err != nil {
		t.Fatalf("sync dst: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Size() != size {
		t.Fatalf("dst size = %d, want %d", info.Size(), size)
	}
	dstBlocks := blocksOf(t, dst)
	if dstBlocks*512 >= size/2 {
		t.Fatalf("dst does not look sparse: %d blocks for a %d-byte file", dstBlocks, size)
	}

	got, err := os.ReadFile(dst) // #nosec G304 -- test-owned path
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.HasPrefix(got, []byte(chunk)) {
		t.Fatalf("dst head = %q, want prefix %q", got[:len(chunk)], chunk)
	}
	if !bytes.Equal(got[size-int64(len(chunk)):], []byte(chunk)) {
		t.Fatalf("dst tail does not match the source's")
	}
}
