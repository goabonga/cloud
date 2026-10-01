// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"flag"
	"io"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	infraprovider "github.com/goabonga/infrastructure/internal/provider"
)

// serveFunc matches providerserver.Serve's signature. Injecting it lets
// tests exercise run's flag parsing and ServeOpts construction without
// starting the real (blocking) Terraform plugin RPC server.
type serveFunc func(ctx context.Context, providerFunc func() provider.Provider, opts providerserver.ServeOpts) error

// run parses CLI flags and hands off to serve. args excludes the program
// name.
func run(ctx context.Context, args []string, stderr io.Writer, serve serveFunc) error {
	fs := flag.NewFlagSet("terraform-provider-infra", flag.ContinueOnError)
	fs.SetOutput(stderr)
	debug := fs.Bool("debug", false, "run with debugger support (attaches a reattach provider)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return serve(ctx, infraprovider.New(Version), providerserver.ServeOpts{
		Address: "registry.terraform.io/goabonga/infra",
		Debug:   *debug,
	})
}
