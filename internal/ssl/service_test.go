// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package ssl_test

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

func newService(t *testing.T) (*ssl.Service, state.Store) {
	t.Helper()
	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
	return ssl.NewService(reg, certs, kek), store
}

func TestCreateCARedactsKeyButKeepsCert(t *testing.T) {
	t.Parallel()

	svc, store := newService(t)
	ca, err := svc.CreateCA("ca-1", "root", resource.SSLCASpec{CommonName: "infra root"})
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}
	if len(ca.Status.CertPEM) == 0 {
		t.Fatal("cert PEM should be present")
	}
	if ca.Status.EncryptedKey != nil {
		t.Fatal("response should not expose the encrypted key")
	}
	if !ca.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", ca.Status.Phase)
	}

	// The private key must not be readable in clear on disk.
	raw, err := store.Get("ssl_ca/ca-1")
	if err != nil {
		t.Fatalf("raw get: %v", err)
	}
	if bytes.Contains(raw, []byte("EC PRIVATE KEY")) {
		t.Fatal("private key stored in clear")
	}
}

func TestIssueCertVerifiesAgainstCA(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	ca, err := svc.CreateCA("ca-1", "root", resource.SSLCASpec{CommonName: "infra root"})
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}

	certPEM, keyPEM, err := svc.Issue("ca-1", "web", []string{"example.com"}, 30)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if block, _ := pem.Decode(keyPEM); block == nil {
		t.Fatal("invalid leaf key PEM")
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.Status.CertPEM) {
		t.Fatal("add CA to pool")
	}
	block, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "example.com"}); err != nil {
		t.Fatalf("leaf should verify against the CA: %v", err)
	}
}

func TestCreateCertPersistsAndReveals(t *testing.T) {
	t.Parallel()

	svc, store := newService(t)
	ca, err := svc.CreateCA("ca-1", "root", resource.SSLCASpec{CommonName: "infra root"})
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}

	cert, err := svc.CreateCert("cert-1", "web", resource.SSLCertSpec{
		CAID:        "ca-1",
		CommonName:  "web.example.com",
		DNSNames:    []string{"web.example.com"},
		IPAddresses: []string{"10.0.1.10"},
		ValidDays:   30,
	})
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	if len(cert.Status.CertPEM) == 0 || cert.Status.EncryptedKey != nil {
		t.Fatalf("cert response should expose the cert but redact the key: %+v", cert.Status)
	}

	// The leaf private key must not be readable in clear on disk.
	raw, err := store.Get("ssl_cert/cert-1")
	if err != nil {
		t.Fatalf("raw get: %v", err)
	}
	if bytes.Contains(raw, []byte("EC PRIVATE KEY")) {
		t.Fatal("leaf private key stored in clear")
	}

	certPEM, keyPEM, err := svc.RevealCert("cert-1")
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if block, _ := pem.Decode(keyPEM); block == nil {
		t.Fatal("invalid revealed key PEM")
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.Status.CertPEM) {
		t.Fatal("add CA to pool")
	}
	block, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "web.example.com"}); err != nil {
		t.Fatalf("persisted leaf should verify against the CA: %v", err)
	}

	certs, err := svc.ListCert()
	if err != nil || len(certs) != 1 || certs[0].Status.EncryptedKey != nil {
		t.Fatalf("list certs = %d %v (key must be redacted)", len(certs), err)
	}
	if err := svc.DeleteCert("cert-1"); err != nil {
		t.Fatalf("delete cert: %v", err)
	}
	if _, err := svc.GetCert("cert-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSSLValidationListDelete(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	if _, err := svc.CreateCA("", "n", resource.SSLCASpec{CommonName: "x"}); err == nil {
		t.Fatal("expected error for empty uid")
	}
	if _, err := svc.CreateCA("ca-1", "n", resource.SSLCASpec{}); err == nil {
		t.Fatal("expected error for empty common name")
	}
	if _, _, err := svc.Issue("ca-1", "", nil, 0); err == nil {
		t.Fatal("expected error for empty cert common name")
	}

	if _, err := svc.CreateCA("ca-1", "n", resource.SSLCASpec{CommonName: "root"}); err != nil {
		t.Fatalf("create ca: %v", err)
	}
	cas, err := svc.List()
	if err != nil || len(cas) != 1 {
		t.Fatalf("list = %d %v", len(cas), err)
	}
	if cas[0].Status.EncryptedKey != nil {
		t.Fatal("list should redact the key")
	}
	if err := svc.Delete("ca-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get("ca-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// Terraform sends a PUT on every update: an unchanged CA must keep its key,
// or every certificate it signed stops verifying.
func TestCreateCAKeepsItsKeyAcrossAnUnchangedPut(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	spec := resource.SSLCASpec{CommonName: "Internal CA", Organization: "lab"}
	first, err := svc.CreateCA("ca", "ca", spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.CreateCA("ca", "ca", spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Status.CertPEM) != string(first.Status.CertPEM) {
		t.Fatal("an unchanged PUT regenerated the CA")
	}
	spec.CommonName = "Renamed CA"
	renamed, err := svc.CreateCA("ca", "ca", spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(renamed.Status.CertPEM) == string(first.Status.CertPEM) {
		t.Fatal("a new subject must get a new certificate")
	}
}

func TestCreateCertKeepsItsKeyAcrossAnUnchangedPut(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	if _, err := svc.CreateCA("ca", "ca", resource.SSLCASpec{CommonName: "CA"}); err != nil {
		t.Fatal(err)
	}
	spec := resource.SSLCertSpec{CAID: "ca", CommonName: "web.internal.demo", DNSNames: []string{"web.internal.demo"}}
	first, err := svc.CreateCert("web", "web", spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.CreateCert("web", "web", spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Status.CertPEM) != string(first.Status.CertPEM) {
		t.Fatal("an unchanged PUT reissued the certificate")
	}
	spec.DNSNames = append(spec.DNSNames, "www.internal.demo")
	wider, err := svc.CreateCert("web", "web", spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(wider.Status.CertPEM) == string(first.Status.CertPEM) {
		t.Fatal("new SANs must get a new certificate")
	}
}
