// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam

import (
	"fmt"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

// ScopeLookup resolves one level of the resource hierarchy at a time.
// Phase 2 supplies an implementation backed by the folder and project
// registries; tests supply an in-memory fake, so this package stays free of
// any registry or HTTP dependency.
type ScopeLookup interface {
	// ProjectParent returns the ParentKind/ParentID of the named project.
	ProjectParent(projectID string) (parentKind, parentID string, err error)
	// FolderParent returns the ParentKind/ParentID of the named folder.
	FolderParent(folderID string) (parentKind, parentID string, err error)
}

// maxChainDepth bounds how many folders ScopeChain will walk through before
// giving up, so a parent cycle fails loudly instead of looping forever.
const maxChainDepth = 64

// ScopeChain returns the ordered chain [project, folder..., organization] a
// project belongs to, walking parents until an organization is reached.
func ScopeChain(lookup ScopeLookup, projectID string) ([]resource.ObjectReference, error) {
	chain := []resource.ObjectReference{{Kind: resource.KindProject, UID: projectID}}

	kind, id, err := lookup.ProjectParent(projectID)
	if err != nil {
		return nil, err
	}
	for range maxChainDepth {
		switch kind {
		case resource.ParentKindOrganization:
			chain = append(chain, resource.ObjectReference{Kind: resource.KindOrganization, UID: id})
			return chain, nil
		case resource.ParentKindFolder:
			chain = append(chain, resource.ObjectReference{Kind: resource.KindFolder, UID: id})
			kind, id, err = lookup.FolderParent(id)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("iam: unknown parent kind %q", kind)
		}
	}
	return nil, fmt.Errorf("iam: parent chain exceeds %d hops (cycle?)", maxChainDepth)
}

// EffectivePermissions unions the permissions granted to subject by every
// binding whose target appears in chain. subject is already in the
// "<kind>:<id>" form stored in IAMBindingSpec.Members (e.g. "user:alice").
func EffectivePermissions(bindings []resource.IAMBinding, chain []resource.ObjectReference, subject string) map[Permission]bool {
	effective := map[Permission]bool{}
	for _, b := range bindings {
		if !inChain(b.Spec.Resource, chain) || !hasMember(b.Spec.Members, subject) {
			continue
		}
		for _, p := range Permissions(b.Spec.Role) {
			effective[p] = true
		}
	}
	return effective
}

func inChain(ref resource.ObjectReference, chain []resource.ObjectReference) bool {
	for _, c := range chain {
		if c.Kind == ref.Kind && c.UID == ref.UID {
			return true
		}
	}
	return false
}

func hasMember(members []string, subject string) bool {
	for _, m := range members {
		if m == subject {
			return true
		}
	}
	return false
}
