// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package replication

import (
	"io"
	"os"
)

// NewFixtureServer uses an offline file copy for deterministic transport fixtures.
func NewFixtureServer(dir string, key []byte, nodeID string) *Server {
	s := NewServer(dir, key, nodeID, nil)
	s.clone = func(dst, src *os.File) error {
		_, err := io.Copy(dst, src)
		_, seekErr := dst.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
		return seekErr
	}
	return s
}
