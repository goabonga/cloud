// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/client"
	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestDeleteAndWaitKeepsPollingUntilFinalized(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if reads.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"metadata":{"uid":"vpc"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	c := client.New[resource.VPCSpec, resource.VPCStatus](server.URL, resource.KindVPC)
	if err := c.DeleteAndWait(context.Background(), "vpc"); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatalf("reads: %d", reads.Load())
	}
}
func TestDeleteAndWaitHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"uid":"vpc"}}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	c := client.New[resource.VPCSpec, resource.VPCStatus](server.URL, resource.KindVPC)
	if err := c.DeleteAndWait(ctx, "vpc"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestDeleteAndWaitHandlesImmediateDeletion(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Error("unexpected polling for immediate deletion")
			}
			w.WriteHeader(status)
		}))
		c := client.New[resource.VPCSpec, resource.VPCStatus](server.URL, resource.KindVPC)
		if err := c.DeleteAndWait(context.Background(), "vpc"); err != nil {
			t.Fatal(err)
		}
		server.Close()
	}
}
