// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package pki_test

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/pki"
)

func parseCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func TestNewCA(t *testing.T) {
	t.Parallel()

	ca, err := pki.NewCA(pki.CASpec{CommonName: "infra root", Organization: "goabonga", ValidFor: 24 * time.Hour})
	if err != nil {
		t.Fatalf("new ca: %v", err)
	}
	cert := parseCert(t, ca.CertPEM)
	if !cert.IsCA {
		t.Fatal("CA certificate should have IsCA set")
	}
	if cert.Subject.CommonName != "infra root" {
		t.Fatalf("CN = %q", cert.Subject.CommonName)
	}
}

func TestNewCAValidation(t *testing.T) {
	t.Parallel()

	if _, err := pki.NewCA(pki.CASpec{ValidFor: time.Hour}); err == nil {
		t.Fatal("expected error for empty common name")
	}
	if _, err := pki.NewCA(pki.CASpec{CommonName: "x"}); err == nil {
		t.Fatal("expected error for zero validity")
	}
}

func TestIssueSignsVerifiableLeaf(t *testing.T) {
	t.Parallel()

	ca, err := pki.NewCA(pki.CASpec{CommonName: "root", ValidFor: 24 * time.Hour})
	if err != nil {
		t.Fatalf("new ca: %v", err)
	}
	certPEM, keyPEM, err := ca.Issue(pki.CertSpec{CommonName: "web", DNSNames: []string{"example.com"}, ValidFor: time.Hour})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if block, _ := pem.Decode(keyPEM); block == nil {
		t.Fatal("invalid leaf key PEM")
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.CertPEM) {
		t.Fatal("failed to add CA to pool")
	}
	leaf := parseCert(t, certPEM)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "example.com"}); err != nil {
		t.Fatalf("leaf should verify against the CA: %v", err)
	}
}

func TestLoadCARoundtrip(t *testing.T) {
	t.Parallel()

	ca, err := pki.NewCA(pki.CASpec{CommonName: "root", ValidFor: 24 * time.Hour})
	if err != nil {
		t.Fatalf("new ca: %v", err)
	}
	loaded, err := pki.LoadCA(ca.CertPEM, ca.KeyPEM)
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}
	// A reloaded CA can still issue verifiable certs.
	certPEM, _, err := loaded.Issue(pki.CertSpec{CommonName: "svc", ValidFor: time.Hour})
	if err != nil {
		t.Fatalf("issue from loaded ca: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM)
	if _, err := parseCert(t, certPEM).Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestLoadCARejectsGarbage(t *testing.T) {
	t.Parallel()

	if _, err := pki.LoadCA([]byte("nope"), []byte("nope")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}
