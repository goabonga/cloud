// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package transporttls loads management credentials independently of tenant PKI.
package transporttls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// FromEnvironment loads the dedicated management trust root and node identity.
// No credentials returns nil; a partial or invalid configuration is an error.
func FromEnvironment() (*tls.Config, error) {
	ca, cert, key := os.Getenv("GOA_MANAGEMENT_TLS_CA"), os.Getenv("GOA_MANAGEMENT_TLS_CERT"), os.Getenv("GOA_MANAGEMENT_TLS_KEY")
	if ca == "" && cert == "" && key == "" {
		return nil, nil
	}
	if ca == "" || cert == "" || key == "" {
		return nil, fmt.Errorf("management TLS requires CA, certificate and key files")
	}
	return Load(ca, cert, key)
}

// Load builds TLS 1.3 credentials for authenticated management clients/servers.
func Load(caFile, certFile, keyFile string) (*tls.Config, error) {
	root, err := os.OpenRoot(filepath.Dir(caFile))
	if err != nil {
		return nil, fmt.Errorf("management TLS CA directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	pem, err := root.ReadFile(filepath.Base(caFile))
	if err != nil {
		return nil, fmt.Errorf("management TLS CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("management TLS CA has no valid certificates")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("management TLS identity: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ClientCAs: pool, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
