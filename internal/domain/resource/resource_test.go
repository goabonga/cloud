// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestObjectMetaIsDeleting(t *testing.T) {
	t.Parallel()

	var m resource.ObjectMeta
	if m.IsDeleting() {
		t.Fatal("fresh meta should not be deleting")
	}

	now := time.Now()
	m.DeletionTimestamp = &now
	if !m.IsDeleting() {
		t.Fatal("meta with deletion timestamp should be deleting")
	}
}

func TestObjectMetaFinalizers(t *testing.T) {
	t.Parallel()

	var m resource.ObjectMeta

	// Removing from an empty list is a no-op.
	m.RemoveFinalizer("missing")
	if len(m.Finalizers) != 0 {
		t.Fatalf("expected no finalizers, got %v", m.Finalizers)
	}

	m.AddFinalizer("a")
	m.AddFinalizer("a") // duplicate ignored
	m.AddFinalizer("b")
	if got := m.Finalizers; len(got) != 2 {
		t.Fatalf("expected 2 finalizers, got %v", got)
	}
	if !m.HasFinalizer("a") || !m.HasFinalizer("b") {
		t.Fatal("expected finalizers a and b to be present")
	}
	if m.HasFinalizer("c") {
		t.Fatal("did not expect finalizer c")
	}

	m.RemoveFinalizer("a")
	if m.HasFinalizer("a") {
		t.Fatal("finalizer a should have been removed")
	}
	if !m.HasFinalizer("b") {
		t.Fatal("finalizer b should remain")
	}

	m.RemoveFinalizer("missing") // removing an absent finalizer keeps the rest
	if len(m.Finalizers) != 1 {
		t.Fatalf("expected 1 finalizer, got %v", m.Finalizers)
	}
}
