// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package metrics exposes the declarative state as Prometheus metrics. The
// collector is kind-agnostic: it counts objects per kind from the store and
// breaks them down by status phase, so new resource kinds need only be listed.
package metrics

import (
	"encoding/json"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/goabonga/infrastructure/internal/state"
)

// Collector reports per-kind resource counts read from the store on each scrape.
type Collector struct {
	store   state.Store
	kinds   []string
	total   *prometheus.Desc
	byPhase *prometheus.Desc
	success *prometheus.Desc
}

// NewCollector returns a collector that scrapes the given kinds from store.
func NewCollector(store state.Store, kinds ...string) *Collector {
	return &Collector{
		success: prometheus.NewDesc("infra_resource_collection_success", "Whether resource collection succeeded.", []string{"kind"}, nil),
		store:   store,
		kinds:   kinds,
		total: prometheus.NewDesc(
			"infra_resources_total",
			"Number of resources of a kind in the store.",
			[]string{"kind"}, nil,
		),
		byPhase: prometheus.NewDesc(
			"infra_resources_by_phase",
			"Number of resources of a kind grouped by status phase.",
			[]string{"kind", "phase"}, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.success
	ch <- c.total
	ch <- c.byPhase
}

// phaseEnvelope is the minimal shape needed to read a resource's phase.
type phaseEnvelope struct {
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// Collect implements prometheus.Collector. A store error for one kind yields a
// failed collection signal and omits counts rather than reporting a false zero.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	for _, kind := range c.kinds {
		kvs, err := c.store.List(kind)
		if err != nil {
			ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 0, kind)
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 1, kind)
		ch <- prometheus.MustNewConstMetric(c.total, prometheus.GaugeValue, float64(len(kvs)), kind)

		byPhase := make(map[string]int)
		for _, kv := range kvs {
			var env phaseEnvelope
			phase := "Unknown"
			if err := json.Unmarshal(kv.Value, &env); err == nil && env.Status.Phase != "" {
				phase = env.Status.Phase
			}
			byPhase[phase]++
		}
		for phase, n := range byPhase {
			ch <- prometheus.MustNewConstMetric(c.byPhase, prometheus.GaugeValue, float64(n), kind, phase)
		}
	}
}
