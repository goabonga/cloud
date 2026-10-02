// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySyncCannotEscapeStoreRoot(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(base)
	for _, path := range []string{outside, filepath.Join(base, "escape")} {
		if err := store.syncDirectory(path); err == nil {
			t.Fatalf("accepted sync outside root: %s", path)
		}
	}
	if err := store.syncDirectory(base); err != nil {
		t.Fatal(err)
	}
}
