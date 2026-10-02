// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package pki creates certificate authorities and issues leaf certificates
// signed by them. It is pure (no I/O): callers persist the PEM material and are
// responsible for protecting private keys.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// CASpec describes a certificate authority to create.
type CASpec struct {
	CommonName   string
	Organization string
	ValidFor     time.Duration
}

// CertSpec describes a leaf certificate to issue.
type CertSpec struct {
	CommonName  string
	DNSNames    []string
	IPAddresses []net.IP
	ValidFor    time.Duration
}

// CA is a certificate authority able to issue leaf certificates.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	// CertPEM and KeyPEM are the PEM-encoded CA certificate and private key.
	CertPEM []byte
	KeyPEM  []byte
}

// NewCA generates a self-signed certificate authority.
func NewCA(spec CASpec) (*CA, error) {
	if spec.CommonName == "" {
		return nil, fmt.Errorf("pki: CA common name is required")
	}
	if spec.ValidFor <= 0 {
		return nil, fmt.Errorf("pki: CA validity must be positive")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pki: generate CA key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: spec.CommonName, Organization: orgs(spec.Organization)},
		NotBefore:             now,
		NotAfter:              now.Add(spec.ValidFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("pki: create CA certificate: %w", err)
	}
	return assemble(der, key)
}

// LoadCA reconstructs a CA from its PEM-encoded certificate and private key.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("pki: invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse CA certificate: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("pki: invalid CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: parse CA key: %w", err)
	}
	return &CA{cert: cert, key: key, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

// Issue creates a leaf certificate signed by the CA, returning the certificate
// and its private key as PEM.
func (ca *CA) Issue(spec CertSpec) (certPEM, keyPEM []byte, err error) {
	if spec.CommonName == "" {
		return nil, nil, fmt.Errorf("pki: certificate common name is required")
	}
	if spec.ValidFor <= 0 {
		return nil, nil, fmt.Errorf("pki: certificate validity must be positive")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: generate leaf key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: spec.CommonName},
		DNSNames:     spec.DNSNames,
		IPAddresses:  spec.IPAddresses,
		NotBefore:    now,
		NotAfter:     now.Add(spec.ValidFor),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("pki: sign leaf certificate: %w", err)
	}
	leaf, err := assemble(der, key)
	if err != nil {
		return nil, nil, err
	}
	return leaf.CertPEM, leaf.KeyPEM, nil
}

// assemble encodes a DER certificate and an EC key into a CA value.
func assemble(der []byte, key *ecdsa.PrivateKey) (*CA, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("pki: parse certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("pki: marshal key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return &CA{cert: cert, key: key, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

func serialNumber() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("pki: serial number: %w", err)
	}
	return serial, nil
}

func orgs(org string) []string {
	if org == "" {
		return nil
	}
	return []string{org}
}
