// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package function implements the synchronous function-invoke path: claiming
// a warm instance (or creating one, when the function's policy allows a cold
// start), proxying a request to it, and releasing it back to the pool.
package function

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// ErrNotReady is returned when the named function has not resolved its
// dependencies yet (its pool controller has not reached Ready).
var ErrNotReady = errors.New("function: not ready")

// ErrNoWarmInstance is returned when no warm instance is available and the
// function's warm-pool policy does not allow a cold start.
var ErrNoWarmInstance = errors.New("function: no warm instance available and cold start is not allowed")

// errAlreadyClaimed is an internal signal from a claim attempt's mutate
// callback: another invoke (or a pool-controller resync) changed the
// instance first, so the caller should move on to the next candidate rather
// than treat it as a hard failure.
var errAlreadyClaimed = errors.New("function: instance already claimed")

// functionInstancePortRangeLo and Hi bound the host ports a cold start
// allocates, matching FunctionController's own range: both create compute
// records that share one pool of host ports.
const (
	functionInstancePortRangeLo = 30000
	functionInstancePortRangeHi = 32767
)

// Service realizes Invoke. It holds no isolation logic of its own: claiming
// and releasing instances, and cold-starting one when needed, are all just
// ordinary writes to the same resource stores the function controller and
// scheduler already reconcile against.
type Service struct {
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus]
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]
	computes  *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	nodes     *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	client    *http.Client

	coldStartTimeout time.Duration
	pollInterval     time.Duration
	releaseAttempts  int
	now              func() time.Time
}

// NewService returns a Service with reasonable defaults: a 30s cold-start
// timeout and a 30s invoke timeout (enforced via the HTTP client's own
// Timeout, which - unlike a context cancelled right after Do returns - stays
// in effect while the caller streams the response body back).
func NewService(
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus],
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus],
	computes *registry.Registry[resource.ComputeSpec, resource.ComputeStatus],
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus],
) *Service {
	return &Service{
		functions:        functions,
		instances:        instances,
		computes:         computes,
		nodes:            nodes,
		client:           &http.Client{Timeout: 30 * time.Second},
		coldStartTimeout: 30 * time.Second,
		pollInterval:     200 * time.Millisecond,
		releaseAttempts:  3,
		now:              time.Now,
	}
}

// Invoke claims a warm instance of functionID - or, when none is available
// and the policy allows it, creates one - proxies body to it over HTTP, and
// keeps it assigned until the response body reaches EOF, fails or is closed.
// The caller owns closing the returned response's body.
func (s *Service) Invoke(ctx context.Context, functionID string, body io.Reader, contentType string) (*http.Response, error) {
	fn, err := s.functions.Get(functionID)
	if err != nil {
		return nil, err // state.ErrNotFound propagates as-is
	}
	if fn.Status.Phase != resource.PhaseReady {
		return nil, ErrNotReady
	}

	inst, err := s.claimWarmInstance(functionID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		if !fn.Spec.WarmPool.AllowColdStart {
			return nil, ErrNoWarmInstance
		}
		if inst, err = s.coldStart(ctx, fn); err != nil {
			return nil, err
		}
	}

	resp, invokeErr := s.forward(ctx, inst, body, contentType)
	if invokeErr != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, errors.Join(invokeErr, s.release(inst.Metadata.UID))
	}
	resp.Body = &assignedBody{ReadCloser: resp.Body, release: func() error { return s.release(inst.Metadata.UID) }}
	return resp, nil
}

// claimWarmInstance tries to atomically claim a Warm instance of functionID
// backed by a Ready compute, trying each live candidate once. A nil instance
// with a nil error means none were available - not an error: the caller
// decides whether to cold-start or fail.
func (s *Service) claimWarmInstance(functionID string) (*resource.FunctionInstance, error) {
	insts, err := s.instances.List()
	if err != nil {
		return nil, fmt.Errorf("function: list instances: %w", err)
	}
	for i := range insts {
		inst := insts[i]
		if inst.Spec.FunctionID != functionID || inst.Metadata.IsDeleting() || inst.Status.State != resource.FunctionInstanceWarm {
			continue
		}
		cp, err := s.computes.Get(inst.Spec.ComputeID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("function: get compute %s: %w", inst.Spec.ComputeID, err)
		}
		if cp.Status.Phase != resource.PhaseReady {
			continue
		}

		ok, err := s.instances.TryUpdate(inst.Metadata.UID, func(r *resource.Resource[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]) error {
			if r.Status.State != resource.FunctionInstanceWarm {
				return errAlreadyClaimed
			}
			r.Status.State = resource.FunctionInstanceAssigned
			return nil
		})
		if errors.Is(err, errAlreadyClaimed) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("function: claim instance %s: %w", inst.Metadata.UID, err)
		}
		if !ok {
			continue // lost the compare-and-swap race; try the next candidate
		}
		inst.Status.State = resource.FunctionInstanceAssigned
		return &inst, nil
	}
	return nil, nil
}

// coldStart creates a fresh compute and function instance directly in the
// Assigned state, bypassing the pool, and waits for the compute to become
// Ready. It mirrors FunctionController.createInstance's shape rather than
// sharing code with it: the two live in different packages serving different
// callers, and this codebase already prefers that kind of small duplication
// over a premature shared abstraction (see the microvm scheduler controller,
// which duplicates the compute scheduler rather than generalizing it).
func (s *Service) coldStart(ctx context.Context, fn *resource.Function) (*resource.FunctionInstance, error) {
	computes, err := s.computes.List()
	if err != nil {
		return nil, fmt.Errorf("function: list computes: %w", err)
	}
	port, err := allocatePort(computes)
	if err != nil {
		return nil, fmt.Errorf("function: allocate port: %w", err)
	}

	computeUID := newUID("compute")
	cp := &resource.Compute{
		Metadata: resource.ObjectMeta{UID: computeUID, Name: fn.Spec.Name, Generation: 1},
		Spec: resource.ComputeSpec{
			SubnetID:        fn.Spec.SubnetID,
			SecurityGroupID: fn.Spec.SecurityGroupID,
			NodePoolID:      fn.Spec.NodePoolID,
			CPU:             fn.Spec.CPU,
			MemoryMB:        fn.Spec.MemoryMB,
			PidsMax:         fn.Spec.PidsMax,
			Image:           fn.Spec.Image,
			Command:         fn.Spec.Command,
			Env:             fn.Spec.Env,
			Ports:           []string{fmt.Sprintf("%d:%d/tcp", port, fn.Spec.Port)},
		},
	}
	if err := s.computes.Put(cp); err != nil {
		return nil, fmt.Errorf("function: create compute: %w", err)
	}

	inst := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: newUID("fninst"), Generation: 1},
		Spec:     resource.FunctionInstanceSpec{FunctionID: fn.Metadata.UID, ComputeID: computeUID},
	}
	inst.Metadata.AddFinalizer(resource.FunctionInstanceFinalizer)
	inst.Status.State = resource.FunctionInstanceAssigned
	inst.Status.Port = port
	inst.Status.SetPhase(resource.PhasePending, "ColdStart", "created for a cold invoke")
	if err := s.instances.Put(inst); err != nil {
		return nil, fmt.Errorf("function: create function instance: %w", err)
	}

	if err := s.waitReady(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// waitReady polls until inst's backing compute is Ready, bounded by
// coldStartTimeout, and fills in inst.Status.NodeName once it is.
func (s *Service) waitReady(ctx context.Context, inst *resource.FunctionInstance) error {
	deadline := s.now().Add(s.coldStartTimeout)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		cp, err := s.computes.Get(inst.Spec.ComputeID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("function: get compute %s: %w", inst.Spec.ComputeID, err)
		}
		if err == nil && cp.Status.Phase == resource.PhaseReady {
			inst.Status.NodeName = cp.Status.NodeName
			return nil
		}
		if s.now().After(deadline) {
			return fmt.Errorf("function: cold start timed out waiting for instance %s", inst.Metadata.UID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// forward proxies body to inst over HTTP.
func (s *Service) forward(ctx context.Context, inst *resource.FunctionInstance, body io.Reader, contentType string) (*http.Response, error) {
	node, err := s.nodes.Get(inst.Status.NodeName)
	if err != nil {
		return nil, fmt.Errorf("function: get node %s: %w", inst.Status.NodeName, err)
	}
	url := fmt.Sprintf("http://%s:%d/", node.Spec.Address, inst.Status.Port)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("function: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("function: invoke %s: %w", url, err)
	}
	return resp, nil
}

// release returns instanceID to the pool as Warm, retrying its
// compare-and-swap a few times against concurrent writers (the pool
// controller's own resync is the only expected one) before giving up.
func (s *Service) release(instanceID string) error {
	for i := 0; i < s.releaseAttempts; i++ {
		ok, err := s.instances.TryUpdate(instanceID, func(r *resource.Resource[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]) error {
			r.Status.State = resource.FunctionInstanceWarm
			r.Status.LastUsedAt = s.now().UTC().Format(time.RFC3339)
			return nil
		})
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("too many concurrent writers")
}

// allocatePort picks a host port in the function-instance range not already
// used by any compute's port mapping, so a cold-started instance never
// collides with another compute (including one the pool controller just
// created in the same instant).
func allocatePort(computes []resource.Compute) (int, error) {
	used := make(map[int]bool, len(computes))
	for i := range computes {
		for _, mapping := range computes[i].Spec.Ports {
			if port, ok := hostPortOf(mapping); ok {
				used[port] = true
			}
		}
	}
	for port := functionInstancePortRangeLo; port <= functionInstancePortRangeHi; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", functionInstancePortRangeLo, functionInstancePortRangeHi)
}

// hostPortOf extracts the host-side port from a "host:container/proto"
// mapping, as used by ComputeSpec.Ports.
func hostPortOf(mapping string) (int, bool) {
	host, _, ok := strings.Cut(mapping, ":")
	if !ok {
		return 0, false
	}
	port, err := strconv.Atoi(host)
	if err != nil {
		return 0, false
	}
	return port, true
}

// newUID generates a short, kind-prefixed identifier, mirroring the
// controller package's own helper (see its doc comment for why this is
// duplicated rather than shared).
func newUID(kind string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return kind + "-" + hex.EncodeToString(b[:])
}

// assignedBody retains the claim for the entire response stream.
type assignedBody struct {
	io.ReadCloser
	once       sync.Once
	release    func() error
	releaseErr error
}

func (b *assignedBody) done() error {
	b.once.Do(func() { b.releaseErr = b.release() })
	return b.releaseErr
}
func (b *assignedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		if releaseErr := b.done(); releaseErr != nil {
			return n, errors.Join(err, releaseErr)
		}
	}
	return n, err
}
func (b *assignedBody) Close() error { return errors.Join(b.ReadCloser.Close(), b.done()) }
