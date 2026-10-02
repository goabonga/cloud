// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package protocol is the wire format between cmd/hypervisor and whatever
// controls it (eventually a microvm backend in internal/manager, today
// only this package's own tests and, later, cmd/hypervisor itself):
// newline-delimited JSON over a Unix domain socket. A persistent 1:1
// connection with no external/browser client has nothing to gain from
// HTTP's request/response machinery, and NDJSON framing lets the server
// push unsolicited Events (e.g. "the vm exited") on the same connection a
// request/response exchange uses.
package protocol

import "encoding/json"

// Request is one client->server message. ID is chosen by the client and
// echoed back on the matching Response, so pipelined requests (unused
// today, but not precluded by the framing) can be matched up.
type Request struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is one server->client reply to a Request with the same ID.
// Result's shape depends on the request Type (CreateResult, StatusResult,
// or omitted for Shutdown).
type Response struct {
	ID     int             `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Event is an unsolicited server->client message, distinguished from a
// Response by having no ID field in the encoded JSON (ID is the zero
// value and omitted). None are defined yet; this exists so Decoder's
// contract (a line is a Response if it has an "id" field, an Event
// otherwise) doesn't need to change when one is added.
type Event struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Request types this package's codec carries. Handling them is
// cmd/hypervisor's job, not this package's.
const (
	TypeCreate   = "create"
	TypeStatus   = "status"
	TypeShutdown = "shutdown"
)

// CreateParams is the payload of a TypeCreate request: everything needed
// to boot one VM, combining cloud-hypervisor's separate vm.create and
// vm.boot into a single call (see internal/hypervisor's Config — this
// process only ever boots once). Every field is honored; an empty
// TapName/DiskPath boots with no network device/disk at all, matching
// Config's own defaults.
type CreateParams struct {
	VCPUs        int    `json:"vcpus"`
	MemoryMB     int    `json:"memory_mb"`
	KernelPath   string `json:"kernel_path"`
	InitrdPath   string `json:"initrd_path,omitempty"`
	CmdLine      string `json:"cmdline,omitempty"`
	DiskPath     string `json:"disk_path,omitempty"`
	DiskReadonly bool   `json:"disk_readonly,omitempty"`
	TapName      string `json:"tap_name,omitempty"`
	MAC          string `json:"mac,omitempty"`
}

// Phase values for StatusResult.Phase.
const (
	PhaseBooting = "booting"
	PhaseRunning = "running"
	PhaseStopped = "stopped"
	PhaseError   = "error"
)

// StatusResult is the Result of a TypeStatus request.
type StatusResult struct {
	Phase         string  `json:"phase"`
	PID           int     `json:"pid,omitempty"`
	UptimeSeconds float64 `json:"uptime_seconds,omitempty"`
	Error         string  `json:"error,omitempty"`
}
