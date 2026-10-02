// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

// ProviderConfig is the data the provider passes to each resource via
// ConfigureRequest.ProviderData.
type ProviderConfig struct {
	Endpoint  string
	Token     string
	ProjectID string
}
