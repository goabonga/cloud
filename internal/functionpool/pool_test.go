// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package functionpool_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/functionpool"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestConcurrentWritersClaimDifferentFunctionPorts(t *testing.T) {
	dir := t.TempDir()
	fn := &resource.Function{Metadata: resource.ObjectMeta{UID: "fn", OwnerUID: "alice", ProjectID: "project", OrganizationID: "org"}, Spec: resource.FunctionSpec{Port: 8080, Image: "image", SubnetID: "subnet"}}
	type result struct {
		compute *resource.Compute
		port    int
		err     error
	}
	results := make(chan result, 24)
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			reg := registry.New[resource.ComputeSpec, resource.ComputeStatus](state.NewFileStore(dir), resource.KindCompute)
			cp, port, err := functionpool.CreateCompute(reg, fn, time.Now())
			results <- result{cp, port, err}
		})
	}
	wg.Wait()
	close(results)
	ports := make(map[int]bool)
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if ports[r.port] {
			t.Fatalf("port allocated twice: %d", r.port)
		}
		ports[r.port] = true
		if r.compute.Metadata.UID != fmt.Sprintf("function-compute-%d", r.port) {
			t.Fatal("reservation is detached from compute identity")
		}
		if r.compute.Metadata.OwnerUID != "alice" || r.compute.Metadata.ProjectID != "project" || r.compute.Metadata.OrganizationID != "org" {
			t.Fatal("tenant metadata lost")
		}
		if !r.compute.Metadata.HasFinalizer(resource.ComputeFinalizer) {
			t.Fatal("failed creation cannot be cleaned up")
		}
	}
}
