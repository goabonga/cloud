// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package protocol_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

func TestRequestResponseRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	done := make(chan error, 1)
	go func() {
		dec := protocol.NewDecoder(server)
		var req protocol.Request
		if err := dec.Decode(&req); err != nil {
			done <- err
			return
		}
		if req.Type != protocol.TypeCreate {
			done <- fmt.Errorf("server: req.Type = %q, want %q", req.Type, protocol.TypeCreate)
			return
		}
		var params protocol.CreateParams
		if err := json.Unmarshal(req.Payload, &params); err != nil {
			done <- err
			return
		}
		if params.VCPUs != 1 || params.MemoryMB != 256 {
			done <- fmt.Errorf("server: params = %+v, want VCPUs=1 MemoryMB=256", params)
			return
		}

		result, _ := json.Marshal(protocol.StatusResult{Phase: protocol.PhaseBooting, PID: 1234})
		enc := protocol.NewEncoder(server)
		done <- enc.Encode(protocol.Response{ID: req.ID, OK: true, Result: result})
	}()

	payload, err := json.Marshal(protocol.CreateParams{VCPUs: 1, MemoryMB: 256, KernelPath: "/boot/vmlinuz"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	enc := protocol.NewEncoder(client)
	if err := enc.Encode(protocol.Request{ID: 1, Type: protocol.TypeCreate, Payload: payload}); err != nil {
		t.Fatalf("Encode request: %v", err)
	}

	dec := protocol.NewDecoder(client)
	resp, event, err := dec.DecodeServerMessage()
	if err != nil {
		t.Fatalf("DecodeServerMessage: %v", err)
	}
	if event != nil {
		t.Fatalf("got an Event, want a Response: %+v", event)
	}
	if resp.ID != 1 || !resp.OK {
		t.Fatalf("resp = %+v, want {ID:1 OK:true ...}", resp)
	}
	var status protocol.StatusResult
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if status.Phase != protocol.PhaseBooting || status.PID != 1234 {
		t.Errorf("status = %+v, want {Phase:booting PID:1234}", status)
	}

	if err := <-done; err != nil {
		t.Fatalf("server goroutine: %v", err)
	}
}

func TestDecodeServerMessageDistinguishesEventFromResponse(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	done := make(chan error, 1)
	go func() {
		enc := protocol.NewEncoder(server)
		done <- enc.Encode(protocol.Event{Type: "exited"})
	}()

	dec := protocol.NewDecoder(client)
	resp, event, err := dec.DecodeServerMessage()
	if err != nil {
		t.Fatalf("DecodeServerMessage: %v", err)
	}
	if resp != nil {
		t.Fatalf("got a Response, want an Event: %+v", resp)
	}
	if event == nil || event.Type != "exited" {
		t.Fatalf("event = %+v, want {Type:exited}", event)
	}

	if err := <-done; err != nil {
		t.Fatalf("server goroutine: %v", err)
	}
}

func TestResponseIDSurvivesZeroValue(t *testing.T) {
	// A Response with ID 0 is a real, distinct message (request IDs are
	// just client-chosen integers, 0 is a valid one) and must still be
	// recognized as a Response, not misread as an Event for having a
	// "zero-looking" id.
	b, err := json.Marshal(protocol.Response{ID: 0, OK: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dec := protocol.NewDecoder(bytes.NewReader(append(b, '\n')))
	resp, event, err := dec.DecodeServerMessage()
	if err != nil {
		t.Fatalf("DecodeServerMessage: %v", err)
	}
	if event != nil {
		t.Fatalf("got an Event for a Response with ID 0: %+v", event)
	}
	if resp == nil || resp.ID != 0 {
		t.Fatalf("resp = %+v, want {ID:0 ...}", resp)
	}
}
