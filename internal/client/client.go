// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package client is the HTTP client for the control-plane API. A generic Client
// mirrors the server handler: it speaks the resource envelope for one kind.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

// ErrNotFound is returned when the API responds 404 for a resource.
var ErrNotFound = errors.New("client: resource not found")

// APIBase is the path prefix shared with the server.
const APIBase = "/api/v1"

// Client talks to the API for a single resource kind. S is the spec type, ST
// the status type.
type Client[S any, ST any] struct {
	base  string
	kind  string
	hc    *http.Client
	token string
}

type options struct {
	token string
}

// Option configures a Client.
type Option func(*options)

// WithToken sends "Authorization: Bearer <token>" on every request.
func WithToken(token string) Option {
	return func(o *options) { o.token = token }
}

// New returns a Client for kind rooted at baseURL (e.g. "http://localhost:8080").
func New[S any, ST any](baseURL, kind string, opts ...Option) *Client[S, ST] {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return &Client[S, ST]{
		base:  strings.TrimRight(baseURL, "/"),
		kind:  kind,
		hc:    http.DefaultClient,
		token: o.token,
	}
}

func (c *Client[S, ST]) collectionURL() string {
	return c.base + APIBase + "/" + c.kind
}

func (c *Client[S, ST]) itemURL(uid string) string {
	return c.collectionURL() + "/" + uid
}

// List returns every resource of this kind.
func (c *Client[S, ST]) List(ctx context.Context) ([]resource.Resource[S, ST], error) {
	var out resource.List[S, ST]
	if err := c.do(ctx, http.MethodGet, c.collectionURL(), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Get returns the resource with the given UID, or ErrNotFound.
func (c *Client[S, ST]) Get(ctx context.Context, uid string) (*resource.Resource[S, ST], error) {
	var out resource.Resource[S, ST]
	if err := c.do(ctx, http.MethodGet, c.itemURL(uid), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Put creates or updates res and returns the stored resource.
func (c *Client[S, ST]) Put(ctx context.Context, res *resource.Resource[S, ST]) (*resource.Resource[S, ST], error) {
	var out resource.Resource[S, ST]
	if err := c.do(ctx, http.MethodPut, c.itemURL(res.Metadata.UID), res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes the resource with the given UID.
func (c *Client[S, ST]) Delete(ctx context.Context, uid string) error {
	return c.do(ctx, http.MethodDelete, c.itemURL(uid), nil, nil)
}

// do performs an HTTP request, encoding body (if any) and decoding into out (if
// any). It maps a 404 to ErrNotFound and any other non-2xx to an error.
func (c *Client[S, ST]) do(ctx context.Context, method, url string, body, out any) error {
	_, err := c.doStatus(ctx, method, url, body, out)
	return err
}

func (c *Client[S, ST]) doStatus(ctx context.Context, method, url string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("client: marshal request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, fmt.Errorf("client: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("client: %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("client: %s %s: status %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return 0, fmt.Errorf("client: decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// DeleteAndWait waits for finalization only when deletion is asynchronous.
func (c *Client[S, ST]) DeleteAndWait(ctx context.Context, uid string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	status, err := c.doStatus(ctx, http.MethodDelete, c.itemURL(uid), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil || status != http.StatusAccepted {
		return err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := c.Get(ctx, uid)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("client: wait for deletion of %s: %w", uid, ctx.Err())
		case <-ticker.C:
		}
	}
}
