// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam

import (
	"sync"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

// BindingSource lists every iam_binding currently stored, for Authorizer to
// search. The generic Handler supplies one backed by the live registry.
type BindingSource func() ([]resource.IAMBinding, error)

// Authorizer decides whether a caller may perform an action on a resource.
type Authorizer struct {
	Lookup   ScopeLookup
	Bindings BindingSource
	decide   func(string, string, Permission) (bool, error)
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
	if a.decide != nil {
		return a.decide(subject, projectID, perm)
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

// ForRequest memoizes binding reads and hierarchy lookups for one request only.
// A fresh instance keeps revocations visible on the following request.
func (a *Authorizer) ForRequest() *Authorizer {
	copy := *a
	var once sync.Once
	var bindings []resource.IAMBinding
	var err error
	copy.Bindings = func() ([]resource.IAMBinding, error) {
		once.Do(func() { bindings, err = a.Bindings() })
		return bindings, err
	}
	copy.Lookup = &requestLookup{source: a.Lookup, projects: make(map[string]parentResult), folders: make(map[string]parentResult)}
	live := &Authorizer{Lookup: copy.Lookup, Bindings: copy.Bindings}
	type decisionKey struct {
		subject, project string
		perm             Permission
	}
	type decision struct {
		allowed bool
		err     error
	}
	decisions := make(map[decisionKey]decision)
	var mu sync.Mutex
	copy.decide = func(subject, project string, perm Permission) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		key := decisionKey{subject, project, perm}
		result, ok := decisions[key]
		if !ok {
			result.allowed, result.err = live.Allowed(subject, false, "", project, perm)
			decisions[key] = result
		}
		return result.allowed, result.err
	}
	return &copy
}

type parentResult struct {
	kind, id string
	err      error
}
type requestLookup struct {
	mu                sync.Mutex
	source            ScopeLookup
	projects, folders map[string]parentResult
}

func (l *requestLookup) ProjectParent(id string) (string, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.projects[id]
	if !ok {
		r.kind, r.id, r.err = l.source.ProjectParent(id)
		l.projects[id] = r
	}
	return r.kind, r.id, r.err
}
func (l *requestLookup) FolderParent(id string) (string, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.folders[id]
	if !ok {
		r.kind, r.id, r.err = l.source.FolderParent(id)
		l.folders[id] = r
	}
	return r.kind, r.id, r.err
}
