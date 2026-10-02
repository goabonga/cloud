// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/goabonga/infrastructure/internal/client"
	"github.com/goabonga/infrastructure/internal/domain/resource"
)

// vpcClient is the API client specialized to the VPC kind.
type vpcClient = client.Client[resource.VPCSpec, resource.VPCStatus]

func apiURL() string {
	if v := os.Getenv("GOA_API_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

// resolveToken returns the bearer token to use: GOA_API_TOKEN always wins
// (unchanged behavior), otherwise a still-valid `infra login` credential.
func resolveToken() string {
	if token := os.Getenv("GOA_API_TOKEN"); token != "" {
		return token
	}
	if creds, ok := loadCredentials(); ok {
		return creds.Token
	}
	return ""
}

func newVPCClient() *vpcClient {
	var opts []client.Option
	if token := resolveToken(); token != "" {
		opts = append(opts, client.WithToken(token))
	}
	return client.New[resource.VPCSpec, resource.VPCStatus](apiURL(), resource.KindVPC, opts...)
}

// run dispatches a CLI invocation. args excludes the program name.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: infra <login|vpc> [args]")
	}
	switch args[0] {
	case "login":
		return runLogin(ctx, stdout)
	case "vpc":
		return runVPC(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runVPC(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: infra vpc <list|get|create|delete> [args]")
	}
	c := newVPCClient()
	switch args[0] {
	case "list":
		items, err := c.List(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, items)
	case "get":
		uid, err := requireUID(args[1:], "get")
		if err != nil {
			return err
		}
		res, err := c.Get(ctx, uid)
		if err != nil {
			return err
		}
		return writeJSON(stdout, res)
	case "create":
		return createVPC(ctx, c, args[1:], stdout)
	case "delete":
		uid, err := requireUID(args[1:], "delete")
		if err != nil {
			return err
		}
		return c.Delete(ctx, uid)
	default:
		return fmt.Errorf("unknown vpc command %q", args[0])
	}
}

func createVPC(ctx context.Context, c *vpcClient, args []string, stdout io.Writer) error {
	uid, err := requireUID(args, "create")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	cidr := fs.String("cidr", "", "VPC CIDR (e.g. 10.0.0.0/16)")
	name := fs.String("name", "", "display name")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	res := &resource.VPC{
		Metadata: resource.ObjectMeta{UID: uid, Name: *name},
		Spec:     resource.VPCSpec{CIDR: *cidr},
	}
	out, err := c.Put(ctx, res)
	if err != nil {
		return err
	}
	return writeJSON(stdout, out)
}

// requireUID returns the positional UID argument for a subcommand.
func requireUID(args []string, cmd string) (string, error) {
	if len(args) == 0 || args[0] == "" {
		return "", fmt.Errorf("usage: infra vpc %s <uid>", cmd)
	}
	return args[0], nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
