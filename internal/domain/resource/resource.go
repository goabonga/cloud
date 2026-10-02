// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package resource defines the declarative resource model shared by the control
// plane and the agent. Every resource follows the metadata / spec / status
// envelope:
//
//   - metadata: identity, ownership and lifecycle (generation, finalizers,
//     deletion timestamp);
//   - spec: desired state, written by the client/API;
//   - status: observed state, written by controllers and the agent.
package resource

import "time"

// APIVersion is the schema version stamped onto every resource envelope.
const APIVersion = "infra/v1"

// Resource is the generic envelope for all declarative objects. S is the spec
// type and ST is the status type.
type Resource[S any, ST any] struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       S          `json:"spec"`
	Status     ST         `json:"status,omitempty"`
}

// List wraps a homogeneous collection of resources.
type List[S any, ST any] struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Items      []Resource[S, ST] `json:"items"`
	Continue   string            `json:"continue,omitempty"`
}

// ObjectMeta holds identity and lifecycle fields common to all resources.
type ObjectMeta struct {
	// UID is the unique system identifier (e.g. "vpc-a1b2c3").
	UID string `json:"uid"`

	// Name is the user-facing display name.
	Name string `json:"name"`

	// OrganizationID scopes the resource to an organization (empty for
	// platform-global resources).
	OrganizationID string `json:"organizationId,omitempty"`

	// ProjectID scopes the resource to a project within an organization. The
	// project is the primary unit of isolation, IAM, quotas and billing.
	ProjectID string `json:"projectId,omitempty"`

	// OwnerUID is the subject that created the resource. The API stamps it at
	// creation time and never honors a client-supplied value; it is the fast
	// path an owner uses to reach their own resources without needing an
	// iam_binding.
	OwnerUID string `json:"ownerUid,omitempty"`

	// Labels are arbitrary key-value pairs for filtering and grouping.
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations are arbitrary key-value pairs for non-identifying metadata.
	Annotations map[string]string `json:"annotations,omitempty"`

	// OwnerRefs lists the resources that own this one (cascade delete).
	OwnerRefs []OwnerReference `json:"ownerRefs,omitempty"`

	// Finalizers are cleanup hooks that must run before the resource is removed
	// from the store. Each controller adds and removes its own key.
	Finalizers []string `json:"finalizers,omitempty"`

	// Generation is incremented on every spec change. Controllers compare it
	// with status.observedGeneration to detect drift.
	Generation int64 `json:"generation"`

	// ResourceVersion identifies the stored revision for optimistic concurrency.
	ResourceVersion string `json:"resourceVersion,omitempty"`

	// DeletionTimestamp is set when a delete is requested. The resource stays
	// in the store until all finalizers are removed.
	DeletionTimestamp *time.Time `json:"deletionTimestamp,omitempty"`

	// CreatedAt is the creation timestamp.
	CreatedAt time.Time `json:"createdAt"`
}

// IsDeleting reports whether a deletion has been requested.
func (m *ObjectMeta) IsDeleting() bool {
	return m.DeletionTimestamp != nil
}

// HasFinalizer reports whether the named finalizer is present.
func (m *ObjectMeta) HasFinalizer(name string) bool {
	for _, f := range m.Finalizers {
		if f == name {
			return true
		}
	}
	return false
}

// AddFinalizer adds a finalizer if it is not already present.
func (m *ObjectMeta) AddFinalizer(name string) {
	if !m.HasFinalizer(name) {
		m.Finalizers = append(m.Finalizers, name)
	}
}

// RemoveFinalizer removes the named finalizer if present.
func (m *ObjectMeta) RemoveFinalizer(name string) {
	if len(m.Finalizers) == 0 {
		return
	}
	kept := make([]string, 0, len(m.Finalizers))
	for _, f := range m.Finalizers {
		if f != name {
			kept = append(kept, f)
		}
	}
	m.Finalizers = kept
}

// OwnerReference identifies a parent resource for cascade operations.
type OwnerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	UID        string `json:"uid"`
	Name       string `json:"name"`
}

// ObjectReference identifies a resource by kind, UID and name.
type ObjectReference struct {
	Kind string `json:"kind"`
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// Validator is implemented by spec types that can check their own validity.
// The API handler calls Validate before accepting a write.
type Validator interface {
	Validate() error
}
