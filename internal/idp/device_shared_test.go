// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package idp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestDeviceSharedConsumptionHasOneWinner(t *testing.T) {
	backend := state.NewFileStore(t.TempDir())
	a, b := newDeviceStore(time.Minute), newDeviceStore(time.Minute)
	a.backend = backend
	b.backend = backend
	da, err := a.create()
	if err != nil {
		t.Fatal(err)
	}
	if !b.approve(da.UserCode, "alice", nil) {
		t.Fatal("other instance cannot approve")
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for _, store := range []*deviceStore{a, b} {
		wg.Add(1)
		go func(d *deviceStore) {
			defer wg.Done()
			out, failure, err := d.poll(da.DeviceCode)
			if err != nil {
				t.Error(err)
			}
			if failure == "" && out.Status == deviceStatusApproved {
				winners.Add(1)
			}
		}(store)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d winners", winners.Load())
	}
}
func TestDeviceExpirySweepAndPollLimit(t *testing.T) {
	d := newDeviceStore(time.Minute)
	now := time.Now()
	d.now = func() time.Time { return now }
	da, err := d.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, failure, err := d.poll(da.DeviceCode); err != nil || failure != "" {
		t.Fatalf("first poll: %s %v", failure, err)
	}
	if _, failure, _ := d.poll(da.DeviceCode); failure != "slow_down" {
		t.Fatal("poll interval not enforced")
	}
	now = now.Add(5 * time.Second)
	if _, failure, _ := d.poll(da.DeviceCode); failure != "slow_down" {
		t.Fatal("slow_down did not increase the polling interval")
	}
	now = now.Add(2 * time.Minute)
	if _, err := d.create(); err != nil {
		t.Fatal(err)
	}
	if len(d.byDevice) != 1 {
		t.Fatal("expired grants retained")
	}
}

func TestDeviceAuthorizationCapacityIsBounded(t *testing.T) {
	d := newDeviceStore(time.Minute)
	for i := 0; i < maxDeviceAuthorizations; i++ {
		code := fmt.Sprint(i)
		da := &deviceAuth{DeviceCode: code, UserCode: code, ExpiresAt: time.Now().Add(time.Minute)}
		d.byDevice[code], d.byUser[code] = da, da
	}
	server := &Server{devices: d}
	response := httptest.NewRecorder()
	server.deviceAuthorization(response, httptest.NewRequest(http.MethodPost, "/device_authorization", nil))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("capacity status: %d", response.Code)
	}
	if len(d.byDevice) != maxDeviceAuthorizations {
		t.Fatal("capacity exceeded")
	}
}
