// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command terraform-provider-infra serves the Terraform provider for the
// control-plane API.
package main

import (
	"context"
	"log"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stderr, providerserver.Serve); err != nil {
		log.Fatalf("terraform-provider-infra: %v", err)
	}
}
