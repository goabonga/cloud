// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/transporttls"
)

func managementIdentity(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "management test root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: root.NotBefore, NotAfter: root.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, block := range map[string]*pem.Block{"ca.crt": {Type: "CERTIFICATE", Bytes: caDER}, "node.crt": {Type: "CERTIFICATE", Bytes: leafDER}, "node.key": {Type: "EC PRIVATE KEY", Bytes: keyDER}} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := transporttls.Load(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "node.crt"), filepath.Join(dir, "node.key"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestManagementTLSReplication(t *testing.T) {
	cfg := managementIdentity(t)
	dir := t.TempDir()
	content := []byte("authenticated immutable snapshot")
	if err := os.WriteFile(filepath.Join(dir, "disk-1.img"), content, 0600); err != nil {
		t.Fatal(err)
	}
	key := []byte("shared replication key")
	srv := NewServer(dir, key, "primary", nil, cfg)
	// The fixture is offline; production uses an atomic filesystem reflink.
	srv.clone = func(dst, src *os.File) error {
		if _, err := io.Copy(dst, src); err != nil {
			return err
		}
		_, err := dst.Seek(0, io.SeekStart)
		return err
	}
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.TLS = srv.tlsConfig
	ts.StartTLS()
	defer ts.Close()
	addr := ts.Listener.Addr().String()
	client := NewClient(key, "secondary", cfg)
	if err := client.Ping(t.Context(), addr); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "replica.img")
	if err := client.PullDisk(t.Context(), addr, "disk-1", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("replica=%q, error=%v", got, err)
	}
	cases := map[string]*tls.Config{}
	missing := cfg.Clone()
	missing.Certificates = nil
	cases["missing client identity"] = missing
	foreign := cfg.Clone()
	foreign.Certificates = managementIdentity(t).Certificates
	cases["foreign authority"] = foreign
	wrongHost := cfg.Clone()
	wrongHost.ServerName = "wrong.example"
	cases["wrong server name"] = wrongHost
	noTrust := cfg.Clone()
	noTrust.RootCAs = x509.NewCertPool()
	cases["untrusted server"] = noTrust
	oldVersion := cfg.Clone()
	oldVersion.MaxVersion = tls.VersionTLS12
	cases["TLS 1.2"] = oldVersion
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if err := NewClient(key, "invalid", bad).Ping(t.Context(), addr); err == nil {
				t.Fatal("invalid TLS credentials accepted")
			}
		})
	}
	if err := NewClient([]byte("wrong key"), "secondary", cfg).PullDisk(t.Context(), addr, "disk-1", filepath.Join(t.TempDir(), "rejected.img")); err == nil {
		t.Fatal("TLS bypassed request signature")
	}
}

func TestReplicationRequiresTLSAndRejectsRedirects(t *testing.T) {
	if err := NewServer(t.TempDir(), nil, "node", nil).ListenAndServe(context.Background(), "127.0.0.1:0"); err == nil {
		t.Fatal("plaintext listener accepted")
	}
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if err := NewClient([]byte("key"), "node").Ping(t.Context(), redirect.Listener.Addr().String()); err == nil {
		t.Fatal("redirect accepted")
	}
	if reached {
		t.Fatal("followed redirect")
	}
}
