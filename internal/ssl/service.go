// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package ssl manages certificate authorities and the leaf certificates they
// sign. Private keys (CA and leaf) are encrypted at rest with the KEK; public
// certificates are stored and served in clear. A leaf can also be issued
// ephemerally without being persisted.
package ssl

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"time"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/pki"
	"github.com/goabonga/infrastructure/internal/registry"
)

// defaultCADays is used when a CA spec sets no validity.
const defaultCADays = 3650

// defaultCertDays is the default lifetime of an issued leaf certificate.
const defaultCertDays = 365

// Registry is the typed store for CAs.
type Registry = registry.Registry[resource.SSLCASpec, resource.SSLCAStatus]

// CertRegistry is the typed store for leaf certificates.
type CertRegistry = registry.Registry[resource.SSLCertSpec, resource.SSLCertStatus]

// Service creates CAs and issues certificates.
type Service struct {
	reg   *Registry
	certs *CertRegistry
	kek   *crypto.KEK
	now   func() time.Time
}

// NewService returns an SSL Service backed by reg and kek.
func NewService(reg *Registry, certs *CertRegistry, kek *crypto.KEK) *Service {
	return &Service{reg: reg, certs: certs, kek: kek, now: time.Now}
}

// CreateCA generates a CA, encrypts its key at rest, and stores it.
func (s *Service) CreateCA(uid, name string, spec resource.SSLCASpec) (*resource.SSLCA, error) {
	if uid == "" {
		return nil, fmt.Errorf("ssl: uid is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	existing, err := s.reg.Get(uid)
	if err == nil && sameCAIdentity(existing.Spec, spec) && len(existing.Status.CertPEM) > 0 {
		// Same subject and lifetime: keep the key, so the certificates it
		// signed stay valid. Terraform sends a PUT on every update.
		if !reflect.DeepEqual(existing.Spec, spec) {
			existing.Spec = spec
			existing.Metadata.Generation++
			existing.Status.MarkReconciled(existing.Metadata.Generation)
			if err := s.reg.Put(existing); err != nil {
				return nil, err
			}
		}
		return redact(existing), nil
	}

	days := spec.ValidDays
	if days == 0 {
		days = defaultCADays
	}
	ca, err := pki.NewCA(pki.CASpec{
		CommonName:   spec.CommonName,
		Organization: spec.Organization,
		ValidFor:     time.Duration(days) * 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	env, err := s.kek.Encrypt(ca.KeyPEM)
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("ssl: marshal envelope: %w", err)
	}

	res := &resource.SSLCA{Metadata: resource.ObjectMeta{UID: uid, Name: name}, Spec: spec}
	if existing != nil {
		res.Metadata.CreatedAt = existing.Metadata.CreatedAt
		res.Metadata.Generation = existing.Metadata.Generation + 1
	} else {
		res.Metadata.CreatedAt = s.now()
		res.Metadata.Generation = 1
	}
	res.Status.CertPEM = ca.CertPEM
	res.Status.EncryptedKey = blob
	res.Status.MarkReconciled(res.Metadata.Generation)
	res.Status.SetPhase(resource.PhaseReady, "Issued", "CA created")

	if err := s.reg.Put(res); err != nil {
		return nil, err
	}
	return redact(res), nil
}

// Get returns the CA with its public certificate, key redacted.
func (s *Service) Get(uid string) (*resource.SSLCA, error) {
	res, err := s.reg.Get(uid)
	if err != nil {
		return nil, err
	}
	return redact(res), nil
}

// List returns every CA, keys redacted.
func (s *Service) List() ([]resource.SSLCA, error) {
	cas, err := s.reg.List()
	if err != nil {
		return nil, err
	}
	for i := range cas {
		cas[i] = *redact(&cas[i])
	}
	return cas, nil
}

// Delete removes a CA.
func (s *Service) Delete(uid string) error {
	return s.reg.Delete(uid)
}

// Issue signs an ephemeral leaf certificate with the named CA and returns the
// certificate and its private key as PEM without persisting them.
func (s *Service) Issue(caUID, commonName string, dnsNames []string, validDays int) (certPEM, keyPEM []byte, err error) {
	if commonName == "" {
		return nil, nil, fmt.Errorf("ssl: certificate commonName is required")
	}
	ca, err := s.loadCA(caUID)
	if err != nil {
		return nil, nil, err
	}
	return ca.Issue(pki.CertSpec{
		CommonName: commonName,
		DNSNames:   dnsNames,
		ValidFor:   time.Duration(certDays(validDays)) * 24 * time.Hour,
	})
}

// CreateCert signs a leaf certificate with the named CA and persists it under
// uid, encrypting the private key at rest. Returns the redacted certificate.
func (s *Service) CreateCert(uid, name string, spec resource.SSLCertSpec) (*resource.SSLCert, error) {
	if uid == "" {
		return nil, fmt.Errorf("ssl: uid is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if existing, err := s.certs.Get(uid); err == nil && reflect.DeepEqual(existing.Spec, spec) && len(existing.Status.CertPEM) > 0 {
		// Nothing to sign anew: keep the certificate and its key.
		return redactCert(existing), nil
	}
	ca, err := s.loadCA(spec.CAID)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(spec.IPAddresses))
	for _, raw := range spec.IPAddresses {
		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, fmt.Errorf("ssl: invalid ip address %q", raw)
		}
		ips = append(ips, ip)
	}
	certPEM, keyPEM, err := ca.Issue(pki.CertSpec{
		CommonName:  spec.CommonName,
		DNSNames:    spec.DNSNames,
		IPAddresses: ips,
		ValidFor:    time.Duration(certDays(spec.ValidDays)) * 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	env, err := s.kek.Encrypt(keyPEM)
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("ssl: marshal envelope: %w", err)
	}

	res := &resource.SSLCert{
		Metadata: resource.ObjectMeta{UID: uid, Name: name, CreatedAt: s.now(), Generation: 1},
		Spec:     spec,
	}
	res.Status.CertPEM = certPEM
	res.Status.EncryptedKey = blob
	res.Status.MarkReconciled(1)
	res.Status.SetPhase(resource.PhaseReady, "Issued", "certificate signed")
	if err := s.certs.Put(res); err != nil {
		return nil, err
	}
	return redactCert(res), nil
}

// GetCert returns the certificate under uid with its key redacted.
func (s *Service) GetCert(uid string) (*resource.SSLCert, error) {
	res, err := s.certs.Get(uid)
	if err != nil {
		return nil, err
	}
	return redactCert(res), nil
}

// ListCert returns every certificate, keys redacted.
func (s *Service) ListCert() ([]resource.SSLCert, error) {
	certs, err := s.certs.List()
	if err != nil {
		return nil, err
	}
	for i := range certs {
		certs[i] = *redactCert(&certs[i])
	}
	return certs, nil
}

// RevealCert returns the certificate and decrypted private key PEM under uid.
func (s *Service) RevealCert(uid string) (certPEM, keyPEM []byte, err error) {
	res, err := s.certs.Get(uid)
	if err != nil {
		return nil, nil, err
	}
	var env crypto.Envelope
	if err := json.Unmarshal(res.Status.EncryptedKey, &env); err != nil {
		return nil, nil, fmt.Errorf("ssl: unmarshal envelope: %w", err)
	}
	keyPEM, err = s.kek.Decrypt(&env)
	if err != nil {
		return nil, nil, err
	}
	return res.Status.CertPEM, keyPEM, nil
}

// DeleteCert removes the certificate under uid.
func (s *Service) DeleteCert(uid string) error {
	return s.certs.Delete(uid)
}

// loadCA decrypts a CA's private key and loads it ready to sign.
func (s *Service) loadCA(caUID string) (*pki.CA, error) {
	res, err := s.reg.Get(caUID)
	if err != nil {
		return nil, err
	}
	var env crypto.Envelope
	if err := json.Unmarshal(res.Status.EncryptedKey, &env); err != nil {
		return nil, fmt.Errorf("ssl: unmarshal envelope: %w", err)
	}
	keyPEMRaw, err := s.kek.Decrypt(&env)
	if err != nil {
		return nil, err
	}
	return pki.LoadCA(res.Status.CertPEM, keyPEMRaw)
}

// certDays returns the certificate lifetime in days, defaulting when zero.
func certDays(d int) int {
	if d == 0 {
		return defaultCertDays
	}
	return d
}

// redact returns a copy with the encrypted private key stripped.
func redact(res *resource.SSLCA) *resource.SSLCA {
	out := *res
	out.Status.EncryptedKey = nil
	return &out
}

// redactCert returns a copy with the encrypted private key stripped.
func redactCert(res *resource.SSLCert) *resource.SSLCert {
	out := *res
	out.Status.EncryptedKey = nil
	return &out
}

// GlobalRootUID is the uid of the platform's public root CA.
const GlobalRootUID = "public-root"

// EnsureGlobalRoot creates the platform's public root CA unless it exists.
// It signs the certificates of public names, and every machine and instance
// trusts it.
func (s *Service) EnsureGlobalRoot(commonName, organization string) (*resource.SSLCA, error) {
	if existing, err := s.reg.Get(GlobalRootUID); err == nil && len(existing.Status.CertPEM) > 0 {
		return redact(existing), nil
	}
	return s.CreateCA(GlobalRootUID, GlobalRootUID, resource.SSLCASpec{CommonName: commonName, Organization: organization, Global: true})
}

// sameCAIdentity reports whether two CA specs describe the same certificate:
// same subject and lifetime. Other fields can change without a new key.
func sameCAIdentity(a, b resource.SSLCASpec) bool {
	return a.CommonName == b.CommonName && a.Organization == b.Organization && a.ValidDays == b.ValidDays
}
