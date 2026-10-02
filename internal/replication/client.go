// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication

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
)

// Client pulls disk backing files from, and pings, other nodes' replication
// servers. Requests are signed with key, which must match the target node's
// Server key.
//
// This talks plain HTTP, not HTTPS: no node-to-node TLS/certificate
// machinery exists anywhere in the codebase to build on (see the
// disk-replication plan's scope note), and the signature already stops an
// unkeyed sender from pulling or probing anything. Confidentiality and a
// stronger identity than "holds the shared key" are a documented later
// hardening, not required for this foundation.
type Client struct {
	key    []byte
	nodeID string
	http   *http.Client
}

// NewClient returns a Client identifying itself as nodeID and signing every
// request with key.
func NewClient(key []byte, nodeID string) *Client {
	return &Client{key: key, nodeID: nodeID, http: &http.Client{Timeout: 30 * time.Second}}
}

// Ping reports whether addr's replication server answers. A nil error means
// reachable; any error (including a non-200 response) means not.
func (c *Client) Ping(ctx context.Context, addr string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ping", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("replication: ping %s: status %d", addr, resp.StatusCode)
	}
	return nil
}

// PullDisk downloads uid's current backing file from addr into dest,
// resuming from a previous partial attempt when one is found at
// dest+".part". The transfer lands at dest only once it is complete and
// intact; a failure partway leaves the .part file in place for the next
// call to resume from.
func (c *Client) PullDisk(ctx context.Context, addr, uid, dest string) error {
	part := dest + ".part"
	have, err := partSize(part)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/disks/"+uid, nil)
	if err != nil {
		return err
	}
	version, _ := os.ReadFile(part + ".version") // #nosec G304 -- caller-owned transfer metadata
	digest := strings.TrimSpace(string(version))
	resuming := have > 0 && validDigest.MatchString(digest)
	if resuming {
		req.URL.RawQuery = "version=" + digest
	}
	if resuming {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	signRequest(req, c.key, c.nodeID)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		resuming = false // the server ignored our Range; start over
	case http.StatusPartialContent:
		if !resuming {
			return fmt.Errorf("replication: unexpected partial response")
		}
	case http.StatusNotFound, http.StatusRequestedRangeNotSatisfiable:
		if resuming {
			if err := os.Remove(part + ".version"); err != nil && !os.IsNotExist(err) {
				return err
			}
			_ = resp.Body.Close()
			return c.PullDisk(ctx, addr, uid, dest)
		}
		return fmt.Errorf("replication: disk not found")
	default:
		return fmt.Errorf("replication: pull %s/%s: status %d", addr, uid, resp.StatusCode)
	}

	expected := resp.Header.Get("X-Infra-SHA256")
	if !validDigest.MatchString(expected) || (resuming && expected != digest) {
		return fmt.Errorf("replication: invalid or changed snapshot digest")
	}
	if err := os.WriteFile(part+".version", []byte(expected), 0600); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if resuming {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o600) // #nosec G304 -- dest is caller-controlled, not request input
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return fmt.Errorf("replication: write %s: %w", part, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	completed, err := os.Open(part) // #nosec G304 -- caller-owned transfer path
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, completed)
	_ = completed.Close()
	if hashErr != nil {
		return hashErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		_ = os.Remove(part + ".version")
		return fmt.Errorf("replication: snapshot checksum mismatch")
	}
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	_ = os.Remove(part + ".version")
	parent, err := os.Open(filepath.Dir(dest)) // #nosec G304 -- caller-owned destination directory
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return parent.Sync()
}

// partSize returns the size of an in-progress transfer at path, or 0 if
// there is none yet.
func partSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
