// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package metrics_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/goabonga/infrastructure/internal/metrics"
	"github.com/goabonga/infrastructure/internal/state"
)

type failingStore struct{ state.Store }

func (failingStore) List(string) ([]state.KeyValue, error) { return nil, errors.New("offline") }
func TestCollectionFailureDoesNotReportEmptyCluster(t *testing.T) {
	c := metrics.NewCollector(failingStore{}, "vpc")
	expected := "# HELP infra_resource_collection_success Whether resource collection succeeded.\n# TYPE infra_resource_collection_success gauge\ninfra_resource_collection_success{kind=\"vpc\"} 0\n"
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "infra_resource_collection_success", "infra_resources_total"); err != nil {
		t.Fatal(err)
	}
}
