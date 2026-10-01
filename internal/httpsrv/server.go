// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package httpsrv wires the resource handlers onto an HTTP mux and runs the
// API server.
package httpsrv

import (
	"net/http"
	"time"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/function"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/httpsec"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/secret"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

// APIBase is the path prefix for all resource routes.
const APIBase = "/api/v1"

// healthPath is served without authentication.
const healthPath = "/healthz"

// Server holds the routed HTTP handler for the control-plane API.
type Server struct {
	mux   *http.ServeMux
	store state.Store
	kek   *crypto.KEK
	authn auth.Authenticator
}

// Option configures a Server.
type Option func(*Server)

// WithSecretEncryption enables the secret resource, encrypting at rest with kek.
// Without it the API serves no secret routes.
func WithSecretEncryption(kek *crypto.KEK) Option {
	return func(s *Server) { s.kek = kek }
}

// WithAuth requires authentication on every API route (health stays open).
func WithAuth(a auth.Authenticator) Option {
	return func(s *Server) { s.authn = a }
}

// New builds a Server backed by store with every resource handler registered.
func New(store state.Store, opts ...Option) *Server {
	s := &Server{mux: http.NewServeMux(), store: store}
	for _, o := range opts {
		o(s)
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	// Control-plane CRUD resources, served by the generic handler. The agent
	// reconciles the kernel-backed ones (vpc, subnet, ...) out of band.
	register[resource.VPCSpec, resource.VPCStatus](s, resource.KindVPC)
	register[resource.SubnetSpec, resource.SubnetStatus](s, resource.KindSubnet)
	register[resource.SecurityGroupSpec, resource.SecurityGroupStatus](s, resource.KindSecurityGroup)
	register[resource.SecurityGroupRuleSpec, resource.SecurityGroupRuleStatus](s, resource.KindSecurityGroupRule)
	register[resource.IPAddressSpec, resource.IPAddressStatus](s, resource.KindIPAddress)
	register[resource.IGWSpec, resource.IGWStatus](s, resource.KindIGW)
	register[resource.RouteSpec, resource.RouteStatus](s, resource.KindRoute)
	register[resource.KMSKeyringSpec, resource.KMSKeyringStatus](s, resource.KindKMSKeyring)
	register[resource.KMSKeySpec, resource.KMSKeyStatus](s, resource.KindKMSKey)
	register[resource.DiskSpec, resource.DiskStatus](s, resource.KindDisk)
	register[resource.DiskFileSpec, resource.DiskFileStatus](s, resource.KindDiskFile)
	register[resource.ComputeSpec, resource.ComputeStatus](s, resource.KindCompute)
	register[resource.MicroVMSpec, resource.MicroVMStatus](s, resource.KindMicroVM)
	register[resource.ACLPolicySpec, resource.ACLPolicyStatus](s, resource.KindACLPolicy)
	register[resource.DNSZoneSpec, resource.DNSZoneStatus](s, resource.KindDNSZone)
	register[resource.DNSRecordSpec, resource.DNSRecordStatus](s, resource.KindDNSRecord)
	register[resource.PeeringSpec, resource.PeeringStatus](s, resource.KindPeering)
	register[resource.LoadBalancerSpec, resource.LoadBalancerStatus](s, resource.KindLoadBalancer)
	register[resource.LBBackendSpec, resource.LBBackendStatus](s, resource.KindLBBackend)
	register[resource.LBTargetGroupSpec, resource.LBTargetGroupStatus](s, resource.KindLBTargetGroup)
	register[resource.LBTargetSpec, resource.LBTargetStatus](s, resource.KindLBTarget)
	register[resource.LBListenerSpec, resource.LBListenerStatus](s, resource.KindLBListener)
	register[resource.WAFPolicySpec, resource.WAFPolicyStatus](s, resource.KindWAFPolicy)
	register[resource.WAFRuleSpec, resource.WAFRuleStatus](s, resource.KindWAFRule)
	register[resource.NodeSpec, resource.NodeStatus](s, resource.KindNode)
	register[resource.NodePoolSpec, resource.NodePoolStatus](s, resource.KindNodePool)

	// function and function_instance are registered directly, rather than
	// through the register() helper, so their registries can be shared with
	// the invoke handler below. computes and nodes are already routed by
	// register() above: these are second Registry instances over the same
	// store and kind, for the invoke service to read, not to serve again.
	functions := registry.New[resource.FunctionSpec, resource.FunctionStatus](s.store, resource.KindFunction)
	functionInstances := registry.New[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](s.store, resource.KindFunctionInstance)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](s.store, resource.KindCompute)
	nodes := registry.New[resource.NodeSpec, resource.NodeStatus](s.store, resource.KindNode)
	handler.New(functions, resource.KindFunction).Register(s.mux, APIBase)
	handler.New(functionInstances, resource.KindFunctionInstance).Register(s.mux, APIBase)
	handler.NewFunctionInvokeHandler(function.NewService(functions, functionInstances, computes, nodes)).Register(s.mux, APIBase)

	// Encryption-backed resources need a KEK.
	if s.kek != nil {
		secrets := registry.New[resource.SecretSpec, resource.SecretStatus](s.store, resource.KindSecret)
		secretVersions := registry.New[resource.SecretVersionSpec, resource.SecretVersionStatus](s.store, resource.KindSecretVersion)
		handler.NewSecretHandler(secret.NewService(secrets, secretVersions, s.kek)).Register(s.mux, APIBase)

		cas := registry.New[resource.SSLCASpec, resource.SSLCAStatus](s.store, resource.KindSSLCA)
		certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](s.store, resource.KindSSLCert)
		handler.NewSSLHandler(ssl.NewService(cas, certs, s.kek)).Register(s.mux, APIBase)
	}

	s.mux.HandleFunc("GET "+healthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// register mounts the generic CRUD handler for a resource kind.
func register[S any, ST any](s *Server, kind string) {
	reg := registry.New[S, ST](s.store, kind)
	handler.New(reg, kind).Register(s.mux, APIBase)
}

// Handler returns the routed HTTP handler. When authentication is enabled every
// route requires a valid token except the health check.
func (s *Server) Handler() http.Handler {
	if s.authn == nil {
		return httpsec.Headers(s.mux)
	}
	guarded := auth.Middleware(s.authn, s.mux)
	// The security headers wrap the auth check rather than sitting inside it,
	// so a 401 carries them too - an error response is still a response a
	// browser renders.
	return httpsec.Headers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthPath {
			s.mux.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	}))
}

// ListenAndServe runs the API server on addr until it errors.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}
