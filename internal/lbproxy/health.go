// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// backendRoots returns the pool verifying a group's https backends: its CA,
// or nil for the system roots.
func backendRoots(g TargetGroup) *x509.CertPool {
	if g.BackendCAPEM == "" {
		return nil
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(g.BackendCAPEM))
	return pool
}

// backendTLS is the client configuration towards an https backend. With a
// server name the backend is verified against it, as usual. Without one -
// a health check, which has no request Host to take it from - the chain is
// still verified against roots, but not the name.
func backendTLS(roots *x509.CertPool, serverName string) *tls.Config {
	if serverName != "" {
		return &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12}
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verified below, against roots, minus the host name. VerifyConnection
		// runs on every connection, resumed ones included.
		InsecureSkipVerify: true, // #nosec G402 -- the chain is verified in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("lbproxy: backend sent no certificate")
			}
			inter := x509.NewCertPool()
			for _, c := range cs.PeerCertificates[1:] {
				inter.AddCert(c)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
			return err
		},
	}
}

// checker probes one target of a group until its context ends.
type checker struct {
	group  string
	hc     HealthCheck
	target Target
	roots  *x509.CertPool
	server string
	state  *targetState
	unit   time.Duration
}

func (c *checker) run(ctx context.Context) {
	interval := time.Duration(c.hc.IntervalSeconds) * c.unit
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		cctx, cancel := context.WithTimeout(ctx, time.Duration(c.hc.TimeoutSeconds)*c.unit)
		err := c.probe(cctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		c.state.observeCheck(err, c.hc.HealthyThreshold, c.hc.UnhealthyThreshold)
		timer.Reset(interval)
	}
}

func (c *checker) probe(ctx context.Context) error {
	port := c.target.Port
	if c.hc.Port != 0 {
		port = c.hc.Port
	}
	addr := net.JoinHostPort(c.target.Address, strconv.Itoa(port))
	if c.hc.Protocol == ProtocolTCP {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	tr := &http.Transport{DisableKeepAlives: true}
	if c.hc.Protocol == ProtocolHTTPS {
		tr.TLSClientConfig = backendTLS(c.roots, c.server)
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.hc.Protocol+"://"+addr+c.hc.Path, nil)
	if err != nil {
		return err
	}
	if c.server != "" {
		req.Host = c.server
	}
	req.Header.Set("User-Agent", "infra-lb-health-check")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("health check answered %d", resp.StatusCode)
	}
	return nil
}
