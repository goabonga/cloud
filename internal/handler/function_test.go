// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/function"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type invokeHandlerEnv struct {
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus]
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]
	computes  *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	nodes     *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	mux       *http.ServeMux
}

func newInvokeHandlerEnv(t *testing.T) *invokeHandlerEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &invokeHandlerEnv{
		functions: registry.New[resource.FunctionSpec, resource.FunctionStatus](store, resource.KindFunction),
		instances: registry.New[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](store, resource.KindFunctionInstance),
		computes:  registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		nodes:     registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
	}
	env.mux = http.NewServeMux()
	handler.NewFunctionInvokeHandler(function.NewService(env.functions, env.instances, env.computes, env.nodes)).Register(env.mux, "/api/v1")
	return env
}

func (env *invokeHandlerEnv) putFunction(t *testing.T, uid string, phase resource.Phase, allowColdStart bool) {
	t.Helper()
	fn := &resource.Function{
		Metadata: resource.ObjectMeta{UID: uid},
		Spec: resource.FunctionSpec{
			SubnetID: "sn-1", Image: "example/fn:latest", Port: 8080,
			WarmPool: resource.WarmPoolPolicy{AllowColdStart: allowColdStart},
		},
	}
	fn.Status.SetPhase(phase, "Test", "test fixture")
	if err := env.functions.Put(fn); err != nil {
		t.Fatalf("seed function: %v", err)
	}
}

func TestFunctionInvokeHandler_NotFound(t *testing.T) {
	t.Parallel()
	env := newInvokeHandlerEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/function/missing/invoke", strings.NewReader(""))
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestFunctionInvokeHandler_NotReady(t *testing.T) {
	t.Parallel()
	env := newInvokeHandlerEnv(t)
	env.putFunction(t, "fn-1", resource.PhasePending, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/function/fn-1/invoke", strings.NewReader(""))
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestFunctionInvokeHandler_NoWarmInstance(t *testing.T) {
	t.Parallel()
	env := newInvokeHandlerEnv(t)
	env.putFunction(t, "fn-1", resource.PhaseReady, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/function/fn-1/invoke", strings.NewReader(""))
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestFunctionInvokeHandler_ForwardsToWarmInstance(t *testing.T) {
	t.Parallel()
	env := newInvokeHandlerEnv(t)
	env.putFunction(t, "fn-1", resource.PhaseReady, false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("echo:" + string(body)))
	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	node := &resource.Node{
		Metadata: resource.ObjectMeta{UID: "node-1"},
		Spec:     resource.NodeSpec{Hostname: "node-1", Address: host, Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 4096}},
	}
	if err := env.nodes.Put(node); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	cp := &resource.Compute{Metadata: resource.ObjectMeta{UID: "compute-1"}, Spec: resource.ComputeSpec{SubnetID: "sn-1", Image: "example/fn:latest"}}
	cp.Status.NodeName = "node-1"
	cp.Status.SetPhase(resource.PhaseReady, "Running", "ready")
	if err := env.computes.Put(cp); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	inst := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: "inst-1"},
		Spec:     resource.FunctionInstanceSpec{FunctionID: "fn-1", ComputeID: "compute-1"},
	}
	inst.Status.State = resource.FunctionInstanceWarm
	inst.Status.Port = port
	inst.Status.NodeName = "node-1"
	if err := env.instances.Put(inst); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/function/fn-1/invoke", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "echo:hello" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "echo:hello")
	}

	updated, err := env.instances.Get("inst-1")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if updated.Status.State != resource.FunctionInstanceWarm {
		t.Fatalf("State = %q, want released back to Warm", updated.Status.State)
	}
}

// TestFunctionInvokeHandler_ForwardError exercises the generic "something
// went wrong talking to the instance" branch: a warm instance exists but its
// node address has nothing listening, so the proxied request itself fails
// with a plain connection error, which isn't ErrNotFound, ErrNotReady or
// ErrNoWarmInstance.
func TestFunctionInvokeHandler_ForwardError(t *testing.T) {
	t.Parallel()
	env := newInvokeHandlerEnv(t)
	env.putFunction(t, "fn-1", resource.PhaseReady, false)

	// Grab a port and immediately release it, so nothing answers there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	node := &resource.Node{
		Metadata: resource.ObjectMeta{UID: "node-1"},
		Spec:     resource.NodeSpec{Hostname: "node-1", Address: "127.0.0.1", Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 4096}},
	}
	if err := env.nodes.Put(node); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	cp := &resource.Compute{Metadata: resource.ObjectMeta{UID: "compute-1"}, Spec: resource.ComputeSpec{SubnetID: "sn-1", Image: "example/fn:latest"}}
	cp.Status.NodeName = "node-1"
	cp.Status.SetPhase(resource.PhaseReady, "Running", "ready")
	if err := env.computes.Put(cp); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	inst := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: "inst-1"},
		Spec:     resource.FunctionInstanceSpec{FunctionID: "fn-1", ComputeID: "compute-1"},
	}
	inst.Status.State = resource.FunctionInstanceWarm
	inst.Status.Port = port
	inst.Status.NodeName = "node-1"
	if err := env.instances.Put(inst); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/function/fn-1/invoke", strings.NewReader(""))
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}
