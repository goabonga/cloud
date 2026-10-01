// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestFolderSpecValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    resource.FolderSpec
		wantErr bool
	}{
		{
			name: "valid under organization",
			spec: resource.FolderSpec{
				DisplayName: "Engineering",
				ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"},
			},
		},
		{
			name: "valid under folder",
			spec: resource.FolderSpec{
				DisplayName: "Platform",
				ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindFolder, ParentID: "folder-1"},
			},
		},
		{
			name: "missing displayName",
			spec: resource.FolderSpec{
				ParentRef: resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"},
			},
			wantErr: true,
		},
		{
			name: "missing parentId",
			spec: resource.FolderSpec{
				DisplayName: "Engineering",
				ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindOrganization},
			},
			wantErr: true,
		},
		{
			name: "invalid parentKind",
			spec: resource.FolderSpec{
				DisplayName: "Engineering",
				ParentRef:   resource.ParentRef{ParentKind: "project", ParentID: "project-1"},
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.spec.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for spec %+v", tc.spec)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for spec %+v: %v", tc.spec, err)
			}
		})
	}
}

func TestFolderSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.FolderSpec{}
}
