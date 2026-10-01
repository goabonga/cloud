// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// Disk resource kinds.
const (
	KindDisk     = "disk"
	KindDiskFile = "disk_file"
)

// DiskFinalizer is attached by the agent so the backing store (and any LUKS
// mapping) is torn down before the disk record is deleted.
const DiskFinalizer = "infra.io/disk"

// DiskSpec is the desired state of a persistent disk. Setting KMSKeyID encrypts
// it at rest via dm-crypt with that key.
type DiskSpec struct {
	Name     string `json:"name,omitempty"`
	SizeMB   int    `json:"sizeMb"`
	KMSKeyID string `json:"kmsKeyId,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s DiskSpec) Validate() error {
	if s.SizeMB <= 0 {
		return fmt.Errorf("disk: sizeMb must be positive")
	}
	return nil
}

// DiskStatus is the observed state of a disk.
type DiskStatus struct {
	StatusBase
	Encrypted bool   `json:"encrypted"`
	Path      string `json:"path,omitempty"`
}

// Disk is a persistent-disk resource.
type Disk = Resource[DiskSpec, DiskStatus]

// DiskFileSpec injects a file into a disk's filesystem.
type DiskFileSpec struct {
	DiskID  string `json:"diskId"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Mode    string `json:"mode,omitempty"`
	// SSLCertID takes the content from an ssl_cert instead, and SSLPart says
	// which part: "certificate", "chain" (the certificate then its CA's) or
	// "private_key".
	SSLCertID string `json:"sslCertId,omitempty"`
	SSLPart   string `json:"sslPart,omitempty"`
}

// The parts of a certificate a disk file can carry.
const (
	SSLPartCertificate = "certificate"
	SSLPartChain       = "chain"
	SSLPartPrivateKey  = "private_key"
)

// Validate reports whether the spec is well-formed.
func (s DiskFileSpec) Validate() error {
	if s.DiskID == "" {
		return fmt.Errorf("disk_file: diskId is required")
	}
	if s.Path == "" {
		return fmt.Errorf("disk_file: path is required")
	}
	if s.SSLCertID != "" {
		if s.Content != "" {
			return fmt.Errorf("disk_file: content and sslCertId are exclusive")
		}
		switch s.SSLPart {
		case SSLPartCertificate, SSLPartChain, SSLPartPrivateKey:
		default:
			return fmt.Errorf("disk_file: sslPart must be certificate, chain or private_key")
		}
	} else if s.SSLPart != "" {
		return fmt.Errorf("disk_file: sslPart needs sslCertId")
	}
	return nil
}

// DiskFileStatus is the observed state of an injected file.
type DiskFileStatus struct {
	StatusBase
}

// DiskFile is an injected-file resource.
type DiskFile = Resource[DiskFileSpec, DiskFileStatus]
