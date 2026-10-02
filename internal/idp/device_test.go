// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp

import (
	"testing"
	"time"
)

func TestDeviceStoreApproveThenConsume(t *testing.T) {
	t.Parallel()

	store := newDeviceStore(time.Minute)
	da, err := store.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if da.Status != deviceStatusPending {
		t.Fatalf("status = %q, want pending", da.Status)
	}

	if !store.approve(da.UserCode, "alice", []string{"admin"}) {
		t.Fatal("approve: expected success")
	}

	got, ok := store.byDeviceCode(da.DeviceCode)
	if !ok {
		t.Fatal("byDeviceCode: expected the authorization to still be present")
	}
	if got.Status != deviceStatusApproved || got.Subject != "alice" || len(got.Roles) != 1 || got.Roles[0] != "admin" {
		t.Fatalf("unexpected authorization: %+v", got)
	}

	store.delete(da.DeviceCode)
	if _, ok := store.byDeviceCode(da.DeviceCode); ok {
		t.Fatal("byDeviceCode: expected the authorization to be gone after delete")
	}
	if store.approve(da.UserCode, "alice", nil) {
		t.Fatal("approve: expected failure after delete")
	}
}

func TestDeviceStoreDeny(t *testing.T) {
	t.Parallel()

	store := newDeviceStore(time.Minute)
	da, _ := store.create()

	if !store.deny(da.UserCode) {
		t.Fatal("deny: expected success")
	}
	got, ok := store.byDeviceCode(da.DeviceCode)
	if !ok || got.Status != deviceStatusDenied {
		t.Fatalf("status = %+v, want denied", got)
	}

	// A resolved authorization cannot be approved or denied again.
	if store.approve(da.UserCode, "alice", nil) {
		t.Fatal("approve: expected failure on an already-denied code")
	}
	if store.deny(da.UserCode) {
		t.Fatal("deny: expected failure on an already-denied code")
	}
}

func TestDeviceStoreExpiry(t *testing.T) {
	t.Parallel()

	now := time.Now()
	store := newDeviceStore(time.Minute)
	store.now = func() time.Time { return now }

	da, _ := store.create()
	store.now = func() time.Time { return now.Add(2 * time.Minute) }

	if _, ok := store.byDeviceCode(da.DeviceCode); ok {
		t.Fatal("byDeviceCode: expected the authorization to have expired")
	}
	if store.approve(da.UserCode, "alice", nil) {
		t.Fatal("approve: expected failure on an expired code")
	}
}

func TestGenerateUserCodeShape(t *testing.T) {
	t.Parallel()

	code, err := generateUserCode()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("code %q does not match XXXX-XXXX", code)
	}
}
