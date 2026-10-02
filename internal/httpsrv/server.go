// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package httpsrv wires the resource handlers onto an HTTP mux and runs the
// API server.
package httpsrv

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/function"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/httpsec"
	"github.com/goabonga/infrastructure/internal/iam"
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
	az    *iam.Authorizer
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
	// Authorization only runs when a request can actually carry an identity:
	// with no authenticator configured, every generically-registered kind
	// stays exactly as open as it is without this block at all (see
	// register below), which is what the many tests that build an
	// unauthenticated Server rely on.
	bindings := registry.New[resource.IAMBindingSpec, resource.IAMBindingStatus](s.store, resource.KindIAMBinding)
	if s.authn != nil {
		s.az = &iam.Authorizer{
			Lookup: iam.RegistryLookup{
				Projects: registry.New[resource.ProjectSpec, resource.ProjectStatus](s.store, resource.KindProject),
				Folders:  registry.New[resource.FolderSpec, resource.FolderStatus](s.store, resource.KindFolder),
			},
			Bindings: bindings.List,
		}
	}

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
	register[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus](s, resource.KindAsyncDiskReplica)
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

	// function and function_instance get the same ownership/grant enforcement
	// as everything registered through register() above, but are wired by
	// hand rather than through that helper, so their registries can also be
	// shared with the invoke handler below. computes and nodes are already
	// routed by register() above: these are second Registry instances over
	// the same store and kind, for the invoke service to read, not to serve
	// again.
	functions := registry.New[resource.FunctionSpec, resource.FunctionStatus](s.store, resource.KindFunction)
	functionInstances := registry.New[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](s.store, resource.KindFunctionInstance)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](s.store, resource.KindCompute)
	nodes := registry.New[resource.NodeSpec, resource.NodeStatus](s.store, resource.KindNode)
	var functionOpts []handler.Option[resource.FunctionSpec, resource.FunctionStatus]
	var functionInstanceOpts []handler.Option[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]
	if s.az != nil {
		functionOpts = append(functionOpts, handler.WithAuthorization[resource.FunctionSpec, resource.FunctionStatus](s.az))
		functionInstanceOpts = append(functionInstanceOpts, handler.WithAuthorization[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](s.az))
	}
	handler.New(functions, resource.KindFunction, functionOpts...).Register(s.mux, APIBase)
	handler.New(functionInstances, resource.KindFunctionInstance, functionInstanceOpts...).Register(s.mux, APIBase)
	handler.NewFunctionInvokeHandler(function.NewService(functions, functionInstances, computes, nodes)).Register(s.mux, APIBase)

	// Organization/folder/project get the same ownership/grant enforcement as
	// everything else above; since nothing populates their own
	// metadata.projectId, only their creator or a global admin can manage
	// one. iam_binding is different: writing one grants access, so letting
	// its own creator manage it via the owner fast path would let anyone
	// grant themselves a role on a project they otherwise can't touch -
	// it is admin-only instead, with no self-service path, whenever
	// authentication is enabled; with none, it stays exactly as open as
	// every other kind above.
	register[resource.OrganizationSpec, resource.OrganizationStatus](s, resource.KindOrganization)
	register[resource.FolderSpec, resource.FolderStatus](s, resource.KindFolder)
	register[resource.ProjectSpec, resource.ProjectStatus](s, resource.KindProject)
	if s.authn != nil {
		handler.New(bindings, resource.KindIAMBinding, handler.WithAdminOnly[resource.IAMBindingSpec, resource.IAMBindingStatus]()).Register(s.mux, APIBase)
	} else {
		handler.New(bindings, resource.KindIAMBinding).Register(s.mux, APIBase)
	}

	// Encryption-backed resources need a KEK.
	if s.kek != nil {
		secrets := registry.New[resource.SecretSpec, resource.SecretStatus](s.store, resource.KindSecret)
		secretVersions := registry.New[resource.SecretVersionSpec, resource.SecretVersionStatus](s.store, resource.KindSecretVersion)
		handler.NewSecretHandler(secret.NewService(secrets, secretVersions, s.kek)).Register(s.mux, APIBase)

		cas := registry.New[resource.SSLCASpec, resource.SSLCAStatus](s.store, resource.KindSSLCA)
		certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](s.store, resource.KindSSLCert)
		handler.NewSSLHandler(ssl.NewService(cas, certs, s.kek)).Register(s.mux, APIBase)
	}

	s.mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		_, err := s.store.Get("health/readiness")
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("GET "+healthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// register mounts the generic CRUD handler for a resource kind, enforcing
// ownership and project grants whenever the server has an Authorizer.
func register[S any, ST any](s *Server, kind string) {
	reg := registry.New[S, ST](s.store, kind)
	var opts []handler.Option[S, ST]
	if s.az != nil {
		opts = append(opts, handler.WithAuthorization[S, ST](s.az))
	}
	handler.New(reg, kind, opts...).Register(s.mux, APIBase)
}

// Handler returns the routed HTTP handler. When authentication is enabled every
// route requires a valid token except the health check.
func (s *Server) Handler() http.Handler {
	if s.authn == nil {
		return httpsec.Headers(s.mux)
	}
	guarded := auth.Middleware(s.authn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeSpecialized(w, r) {
			return
		}
		s.mux.ServeHTTP(w, r)
	}))
	// The security headers wrap the auth check rather than sitting inside it,
	// so a 401 carries them too - an error response is still a response a
	// browser renders.
	return httpsec.Headers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthPath || r.URL.Path == "/readyz" {
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

// authorizeSpecialized protects operations outside the generic CRUD handler.
// Encryption resources are platform-managed until they carry tenant ownership.
func (s *Server) authorizeSpecialized(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, APIBase+"/"), "/")
	if len(parts) == 0 {
		return true
	}
	id, ok := auth.IdentityFrom(r.Context())
	switch parts[0] {
	case resource.KindSecret, resource.KindSecretVersion, resource.KindSSLCA, resource.KindSSLCert:
		if !ok || !id.HasRole(handler.AdminRole) {
			http.Error(w, "admin role required", http.StatusForbidden)
			return false
		}
	case resource.KindFunction:
		if r.Method != http.MethodPost || len(parts) != 3 || parts[2] != "invoke" {
			return true
		}
		fn, err := registry.New[resource.FunctionSpec, resource.FunctionStatus](s.store, resource.KindFunction).Get(parts[1])
		if err != nil {
			http.Error(w, "function unavailable", http.StatusForbidden)
			return false
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
		allowed, err := s.az.Allowed(id.Subject, id.HasRole(handler.AdminRole), fn.Metadata.OwnerUID, fn.Metadata.ProjectID, iam.PermissionWrite)
		if err != nil || !allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
	}
	return true
}
