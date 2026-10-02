// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestCollectionPagesAreStableAndBounded(t *testing.T) {
	mux, reg := newMux(t)
	for i := range 205 {
		if err := reg.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: fmt.Sprintf("vpc-%03d", i)}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/vpc"
	seen := make(map[string]bool)
	for page := 0; page < 3; page++ {
		rec := do(t, mux, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		var out resource.List[resource.VPCSpec, resource.VPCStatus]
		mustDecode(t, rec, &out)
		if len(out.Items) > 100 {
			t.Fatalf("page has %d items", len(out.Items))
		}
		for _, item := range out.Items {
			if seen[item.Metadata.UID] {
				t.Fatal("duplicate item")
			}
			seen[item.Metadata.UID] = true
		}
		if page < 2 && out.Continue == "" {
			t.Fatal("missing next page")
		}
		if page == 2 && out.Continue != "" {
			t.Fatal("unexpected final continuation")
		}
		path = "/api/v1/vpc?continue=" + url.QueryEscape(out.Continue)
	}
	if len(seen) != 205 {
		t.Fatalf("lost resources: %d", len(seen))
	}
	for _, query := range []string{"limit=0", "limit=-1", "limit=1001", "limit=no", "continue=%21", "continue=" + strings.Repeat("a", 4097)} {
		if rec := do(t, mux, http.MethodGet, "/api/v1/vpc?"+query, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", query, rec.Code)
		}
	}
}

func TestPaginationFiltersAuthorizationBeforeSelectingPage(t *testing.T) {
	mux, reg, _ := newAuthzVPC(t, nil)
	for _, item := range []struct{ uid, owner string }{{"a-hidden", "bob"}, {"b-visible", "alice"}, {"c-hidden", "bob"}, {"d-visible", "alice"}} {
		if err := reg.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: item.uid, OwnerUID: item.owner}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); err != nil {
			t.Fatal(err)
		}
	}
	rec := doAs(t, mux, "alice", nil, http.MethodGet, "/api/v1/vpc?limit=1", nil)
	var first resource.List[resource.VPCSpec, resource.VPCStatus]
	mustDecode(t, rec, &first)
	if len(first.Items) != 1 || first.Items[0].Metadata.UID != "b-visible" {
		t.Fatalf("unauthorized page: %+v", first)
	}
	if first.Continue != base64.RawURLEncoding.EncodeToString([]byte("b-visible")) {
		t.Fatal("cursor disclosed hidden metadata")
	}
	rec = doAs(t, mux, "alice", nil, http.MethodGet, "/api/v1/vpc?limit=1&continue="+first.Continue, nil)
	var last resource.List[resource.VPCSpec, resource.VPCStatus]
	mustDecode(t, rec, &last)
	if len(last.Items) != 1 || last.Items[0].Metadata.UID != "d-visible" || last.Continue != "" {
		t.Fatalf("invalid final page: %+v", last)
	}
}
