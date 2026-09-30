// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package idp is the identity provider: it issues ES256 JWTs for authenticated
// clients and publishes the public key as a JWKS so the API can verify them.
package idp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
)

// GenerateKey returns a fresh ECDSA P-256 signing key.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("idp: generate key: %w", err)
	}
	return key, nil
}

// MarshalPrivateKeyPEM encodes an EC private key as PEM.
func MarshalPrivateKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("idp: marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM decodes a PEM-encoded EC private key.
func ParsePrivateKeyPEM(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("idp: invalid private key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("idp: parse private key: %w", err)
	}
	return key, nil
}

// MarshalPublicKeyPEM encodes an EC public key as PKIX PEM, which the API parses
// to verify tokens.
func MarshalPublicKeyPEM(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("idp: marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// jwk is a single JSON Web Key for an EC P-256 public key.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// JWKS is a JSON Web Key Set.
type JWKS struct {
	Keys []jwk `json:"keys"`
}

// PublicJWKS builds a JWKS exposing pub for ES256 signature verification.
func PublicJWKS(pub *ecdsa.PublicKey, kid string) (JWKS, error) {
	const coordLen = 32 // P-256
	// Uncompressed SEC 1 point: 0x04 || X || Y, each coordinate fixed-width.
	point, err := pub.Bytes()
	if err != nil {
		return JWKS{}, fmt.Errorf("encode public key: %w", err)
	}
	if len(point) != 1+2*coordLen {
		return JWKS{}, fmt.Errorf("encode public key: not a P-256 point (%d bytes)", len(point))
	}
	return JWKS{Keys: []jwk{{
		Kty: "EC",
		Crv: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(point[1 : 1+coordLen]),
		Y:   base64.RawURLEncoding.EncodeToString(point[1+coordLen:]),
		Use: "sig",
		Alg: "ES256",
		Kid: kid,
	}}}, nil
}
