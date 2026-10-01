// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam

import "github.com/goabonga/infrastructure/internal/domain/resource"

// BindingSource lists every iam_binding currently stored, for Authorizer to
// search. The generic Handler supplies one backed by the live registry.
type BindingSource func() ([]resource.IAMBinding, error)

// Authorizer decides whether a caller may perform an action on a resource.
type Authorizer struct {
	Lookup   ScopeLookup
	Bindings BindingSource
}

// Allowed reports whether subject may exercise perm on a resource owned by
// ownerUID and scoped to projectID (either may be empty). isAdmin and the
// owner fast path both bypass every other check; with neither, a resource
// with an empty projectID has no scope chain to walk and is denied to
// everyone else. subject is the caller's bare id (e.g. "alice"), compared
// against ownerUID directly and, prefixed "user:", against iam_binding
// members.
func (a *Authorizer) Allowed(subject string, isAdmin bool, ownerUID, projectID string, perm Permission) (bool, error) {
	if isAdmin || (ownerUID != "" && ownerUID == subject) {
		return true, nil
	}
	if projectID == "" {
		return false, nil
	}
	chain, err := ScopeChain(a.Lookup, projectID)
	if err != nil {
		return false, err
	}
	bindings, err := a.Bindings()
	if err != nil {
		return false, err
	}
	principal := resource.MemberPrefixUser + ":" + subject
	return EffectivePermissions(bindings, chain, principal)[perm], nil
}
