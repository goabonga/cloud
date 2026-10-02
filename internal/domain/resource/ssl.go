// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindSSLCA is the resource kind for SSL certificate authorities.
const KindSSLCA = "ssl_ca"

// SSLCASpec is the desired state of a certificate authority.
type SSLCASpec struct {
	CommonName   string `json:"commonName"`
	Organization string `json:"organization,omitempty"`
	// ValidDays is the CA lifetime; zero means the server default.
	ValidDays int `json:"validDays,omitempty"`
	// VPCIDs are the VPCs whose instances trust the CA.
	VPCIDs []string `json:"vpcIds,omitempty"`
	// Global marks the platform's public root, trusted by every machine and
	// every instance. Only the platform creates it.
	Global bool `json:"global,omitempty"`
}

// Validate reports whether the spec is well-formed for a write.
func (s SSLCASpec) Validate() error {
	if s.CommonName == "" {
		return fmt.Errorf("ssl_ca: commonName is required")
	}
	if s.ValidDays < 0 {
		return fmt.Errorf("ssl_ca: validDays must not be negative")
	}
	return nil
}

// SSLCAStatus is the observed state of a CA. CertPEM is public; EncryptedKey is
// the envelope-encrypted private key, never returned in clear.
type SSLCAStatus struct {
	StatusBase
	CertPEM      []byte `json:"certPem,omitempty"`
	EncryptedKey []byte `json:"encryptedKey,omitempty"`
}

// SSLCA is a certificate authority resource.
type SSLCA = Resource[SSLCASpec, SSLCAStatus]

// KindSSLCert is the resource kind for SSL leaf certificates.
const KindSSLCert = "ssl_cert"

// SSLCertSpec is the desired state of a leaf certificate signed by a CA.
type SSLCertSpec struct {
	CAID        string   `json:"caId"`
	CommonName  string   `json:"commonName"`
	DNSNames    []string `json:"dnsNames,omitempty"`
	IPAddresses []string `json:"ipAddresses,omitempty"`
	// ValidDays is the certificate lifetime; zero means the server default.
	ValidDays int `json:"validDays,omitempty"`
}

// Validate reports whether the spec is well-formed for a write.
func (s SSLCertSpec) Validate() error {
	if s.CAID == "" {
		return fmt.Errorf("ssl_cert: caId is required")
	}
	if s.CommonName == "" {
		return fmt.Errorf("ssl_cert: commonName is required")
	}
	if s.ValidDays < 0 {
		return fmt.Errorf("ssl_cert: validDays must not be negative")
	}
	return nil
}

// SSLCertStatus is the observed state of a certificate. CertPEM is public;
// EncryptedKey is the envelope-encrypted private key, never returned in clear.
type SSLCertStatus struct {
	StatusBase
	CertPEM      []byte `json:"certPem,omitempty"`
	EncryptedKey []byte `json:"encryptedKey,omitempty"`
}

// SSLCert is a leaf-certificate resource.
type SSLCert = Resource[SSLCertSpec, SSLCertStatus]
