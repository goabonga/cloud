// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package function

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/functionpool"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type invokeEnv struct {
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus]
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]
	computes  *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	nodes     *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	svc       *Service
}

func newInvokeEnv(t *testing.T) *invokeEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &invokeEnv{
		functions: registry.New[resource.FunctionSpec, resource.FunctionStatus](store, resource.KindFunction),
		instances: registry.New[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](store, resource.KindFunctionInstance),
		computes:  registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		nodes:     registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
	}
	env.svc = NewService(env.functions, env.instances, env.computes, env.nodes)
	env.svc.pollInterval = time.Millisecond
	env.svc.coldStartTimeout = 2 * time.Second
	return env
}

func (env *invokeEnv) putFunction(t *testing.T, uid string, ready, allowColdStart bool) {
	t.Helper()
	fn := &resource.Function{
		Metadata: resource.ObjectMeta{UID: uid},
		Spec: resource.FunctionSpec{
			SubnetID: "sn-1", Image: "example/fn:latest", Port: 8080,
			WarmPool: resource.WarmPoolPolicy{AllowColdStart: allowColdStart},
		},
	}
	if ready {
		fn.Status.SetPhase(resource.PhaseReady, "Ready", "ok")
	} else {
		fn.Status.SetPhase(resource.PhasePending, "Pending", "waiting")
	}
	if err := env.functions.Put(fn); err != nil {
		t.Fatalf("seed function: %v", err)
	}
}

func (env *invokeEnv) putNode(t *testing.T, uid, address string) {
	t.Helper()
	node := &resource.Node{
		Metadata: resource.ObjectMeta{UID: uid},
		Spec:     resource.NodeSpec{Hostname: uid, Address: address, Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 4096}},
	}
	if err := env.nodes.Put(node); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

// putWarmInstance seeds a Ready compute on nodeID and a Warm instance of
// functionID pointing at it, listening on port.
func (env *invokeEnv) putWarmInstance(t *testing.T, functionID, nodeID string, port int) string {
	t.Helper()
	computeUID := "compute-" + functionID
	cp := &resource.Compute{
		Metadata: resource.ObjectMeta{UID: computeUID},
		Spec:     resource.ComputeSpec{SubnetID: "sn-1", Image: "example/fn:latest"},
	}
	cp.Status.NodeName = nodeID
	cp.Status.SetPhase(resource.PhaseReady, "Running", "ready")
	if err := env.computes.Put(cp); err != nil {
		t.Fatalf("seed compute: %v", err)
	}

	instUID := "fninst-" + functionID
	inst := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: instUID},
		Spec:     resource.FunctionInstanceSpec{FunctionID: functionID, ComputeID: computeUID},
	}
	inst.Status.State = resource.FunctionInstanceWarm
	inst.Status.Port = port
	inst.Status.NodeName = nodeID
	if err := env.instances.Put(inst); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	return instUID
}

// echoServer starts an HTTP server that echoes the request body back prefixed
// with "echo:", and returns the host and port to seed a Node/instance with.
func echoServer(t *testing.T) (host string, port int, close func()) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("echo:" + string(body)))
	}))
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	portNum, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return h, portNum, ts.Close
}

func TestInvoke_WarmPathForwardsAndReleases(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)
	host, port, closeServer := echoServer(t)
	defer closeServer()

	env.putFunction(t, "fn-1", true, false)
	env.putNode(t, "node-1", host)
	instUID := env.putWarmInstance(t, "fn-1", "node-1", port)

	resp, err := env.svc.Invoke(context.Background(), "fn-1", strings.NewReader("hello"), "text/plain")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "echo:hello" {
		t.Fatalf("body = %q, want echo:hello", body)
	}

	inst, err := env.instances.Get(instUID)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.Status.State != resource.FunctionInstanceWarm {
		t.Fatalf("State = %q, want the instance released back to Warm", inst.Status.State)
	}
	if inst.Status.LastUsedAt == "" {
		t.Fatal("want LastUsedAt stamped on release")
	}
}

func TestInvoke_NotReadyFunction(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)
	env.putFunction(t, "fn-1", false, false)

	resp, err := env.svc.Invoke(context.Background(), "fn-1", strings.NewReader(""), "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
}

func TestInvoke_MissingFunction(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)

	resp, err := env.svc.Invoke(context.Background(), "fn-missing", strings.NewReader(""), "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("err = %v, want state.ErrNotFound", err)
	}
}

func TestInvoke_NoWarmInstanceAndColdStartDisallowed(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)
	env.putFunction(t, "fn-1", true, false)

	resp, err := env.svc.Invoke(context.Background(), "fn-1", strings.NewReader(""), "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if !errors.Is(err, ErrNoWarmInstance) {
		t.Fatalf("err = %v, want ErrNoWarmInstance", err)
	}
}

func TestInvoke_ColdStartCreatesInstanceAndReleasesAfter(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)
	env.putFunction(t, "fn-1", true, true)
	env.putNode(t, "node-1", "127.0.0.1")

	// Stand in for the scheduler and the agent: as soon as the cold-started
	// compute appears, place it on node-1 and mark it Ready. Nothing listens
	// on its allocated port, so the forward itself will fail - this test is
	// only about the create/wait/release orchestration, not the HTTP hop.
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			computes, _ := env.computes.List()
			if len(computes) > 0 {
				cp := computes[0]
				cp.Status.NodeName = "node-1"
				cp.Status.SetPhase(resource.PhaseReady, "Running", "ready")
				_ = env.computes.Put(&cp)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	resp, invokeErr := env.svc.Invoke(context.Background(), "fn-1", strings.NewReader("hi"), "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	<-done
	if invokeErr == nil {
		t.Fatal("want an error forwarding to a port nothing listens on")
	}

	computes, err := env.computes.List()
	if err != nil || len(computes) != 1 {
		t.Fatalf("computes = %v, err = %v, want exactly 1 created", computes, err)
	}
	insts, err := env.instances.List()
	if err != nil || len(insts) != 1 {
		t.Fatalf("instances = %v, err = %v, want exactly 1 created", insts, err)
	}
	if insts[0].Status.Port < functionInstancePortRangeLo || insts[0].Status.Port > functionInstancePortRangeHi {
		t.Fatalf("Port = %d, want in [%d, %d]", insts[0].Status.Port, functionInstancePortRangeLo, functionInstancePortRangeHi)
	}
	if insts[0].Status.State != resource.FunctionInstanceWarm {
		t.Fatalf("State = %q, want released back to Warm despite the forward failure", insts[0].Status.State)
	}
}

func TestInvoke_ColdStartTimesOutWhenComputeNeverReady(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)
	env.svc.coldStartTimeout = 20 * time.Millisecond
	env.putFunction(t, "fn-1", true, true)

	resp, err := env.svc.Invoke(context.Background(), "fn-1", strings.NewReader(""), "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("want a timeout error when the compute never becomes ready")
	}
}

func TestClaimWarmInstance_SkipsNotReadyComputeAndAlreadyAssigned(t *testing.T) {
	t.Parallel()
	env := newInvokeEnv(t)

	// Instance 1: Warm, but its compute is not Ready yet - must be skipped.
	notReady := &resource.Compute{Metadata: resource.ObjectMeta{UID: "compute-1"}}
	notReady.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "")
	if err := env.computes.Put(notReady); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	inst1 := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: "inst-1"},
		Spec:     resource.FunctionInstanceSpec{FunctionID: "fn-1", ComputeID: "compute-1"},
	}
	inst1.Status.State = resource.FunctionInstanceWarm
	if err := env.instances.Put(inst1); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	// Instance 2: already Assigned - must be skipped.
	ready := &resource.Compute{Metadata: resource.ObjectMeta{UID: "compute-2"}}
	ready.Status.SetPhase(resource.PhaseReady, "Running", "")
	if err := env.computes.Put(ready); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	inst2 := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: "inst-2"},
		Spec:     resource.FunctionInstanceSpec{FunctionID: "fn-1", ComputeID: "compute-2"},
	}
	inst2.Status.State = resource.FunctionInstanceAssigned
	if err := env.instances.Put(inst2); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	claimed, err := env.svc.claimWarmInstance("fn-1")
	if err != nil {
		t.Fatalf("claimWarmInstance: %v", err)
	}
	if claimed != nil {
		t.Fatalf("want no claimable instance, got %s", claimed.Metadata.UID)
	}
}

func TestAllocatePort_SkipsPortsAlreadyInUse(t *testing.T) {
	t.Parallel()

	used := &resource.Compute{
		Metadata: resource.ObjectMeta{UID: "compute-1"},
		Spec:     resource.ComputeSpec{Ports: []string{"30000:8080/tcp"}},
	}
	env := newInvokeEnv(t)
	if err := env.computes.Put(used); err != nil {
		t.Fatal(err)
	}
	_, port, err := functionpool.CreateCompute(env.computes, &resource.Function{}, time.Now())
	if err != nil {
		t.Fatalf("allocatePort: %v", err)
	}
	if port != functionInstancePortRangeLo+1 {
		t.Fatalf("port = %d, want %d (the next free one)", port, functionInstancePortRangeLo+1)
	}
}

func TestStreamingInvocationRetainsExclusiveInstanceUntilClose(t *testing.T) {
	env := newInvokeEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("stream"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portString, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	env.putFunction(t, "fn-stream", true, false)
	env.putNode(t, "node", host)
	uid := env.putWarmInstance(t, "fn-stream", "node", port)
	resp, err := env.svc.Invoke(context.Background(), "fn-stream", strings.NewReader("request"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	inst, err := env.instances.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status.State != resource.FunctionInstanceAssigned {
		t.Fatal("stream released before the caller consumed it")
	}
	second, err := env.svc.Invoke(context.Background(), "fn-stream", strings.NewReader("second"), "")
	if second != nil {
		_ = second.Body.Close()
	}
	if !errors.Is(err, ErrNoWarmInstance) {
		t.Fatalf("concurrent invocation reused streaming instance: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	inst, err = env.instances.Get(uid)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status.State != resource.FunctionInstanceWarm {
		t.Fatal("closed response did not release instance")
	}
}

func TestResponseClaimReleasedOnceOnEOFAndClose(t *testing.T) {
	calls := 0
	body := &assignedBody{ReadCloser: io.NopCloser(strings.NewReader("done")), release: func() error { calls++; return nil }}
	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("claim released %d times", calls)
	}
}

func TestFailedColdStartIsRetiredAfterCancellation(t *testing.T) {
	env := newInvokeEnv(t)
	env.putFunction(t, "fn-timeout", true, true)
	env.svc.coldStartTimeout = 100 * time.Millisecond
	response, err := env.svc.Invoke(context.Background(), "fn-timeout", strings.NewReader(""), "")
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("expected cold-start timeout")
	}
	computes, err := env.computes.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(computes) != 1 || !computes[0].Metadata.IsDeleting() {
		t.Fatal("failed cold-start compute remained active")
	}
	instances, err := env.instances.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || !instances[0].Metadata.IsDeleting() || instances[0].Status.State == resource.FunctionInstanceAssigned {
		t.Fatal("failed cold-start instance remained assigned")
	}
}
