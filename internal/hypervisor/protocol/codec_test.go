// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package protocol_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

// errWriter always fails, simulating a transport write failure (a closed
// socket, a broken pipe, ...).
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("errWriter: simulated write failure")
}

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

func TestEncodeMarshalError(t *testing.T) {
	enc := protocol.NewEncoder(&bytes.Buffer{})
	// Channels can't be marshaled to JSON.
	if err := enc.Encode(make(chan int)); err == nil {
		t.Fatal("Encode: want error for an unmarshalable value, got nil")
	}
}

func TestEncodeWriteError(t *testing.T) {
	enc := protocol.NewEncoder(errWriter{})
	// A payload bigger than bufio's default 4096-byte buffer forces an
	// immediate pass-through write to the underlying writer, so the
	// failure surfaces from e.w.Write itself rather than from Flush.
	big := strings.Repeat("a", 5000)
	if err := enc.Encode(big); err == nil {
		t.Fatal("Encode: want error when the underlying writer fails, got nil")
	}
}

func TestEncodeWriteByteError(t *testing.T) {
	enc := protocol.NewEncoder(errWriter{})
	// A marshaled value exactly as big as bufio's default 4096-byte buffer
	// fills it without overflowing, so Write succeeds by buffering; the
	// trailing WriteByte('\n') is then the one that must flush the full
	// buffer, and that's where the underlying writer's failure surfaces.
	exact := strings.Repeat("a", 4094) // + 2 quote bytes from json.Marshal == 4096
	if err := enc.Encode(exact); err == nil {
		t.Fatal("Encode: want error from the trailing WriteByte when flush fails, got nil")
	}
}

func TestDecodeReadError(t *testing.T) {
	dec := protocol.NewDecoder(strings.NewReader(""))
	var v any
	if err := dec.Decode(&v); err == nil {
		t.Fatal("Decode: want error on empty input, got nil")
	}
}

func TestDecodeUnmarshalError(t *testing.T) {
	dec := protocol.NewDecoder(strings.NewReader("not json\n"))
	var v any
	if err := dec.Decode(&v); err == nil {
		t.Fatal("Decode: want error for malformed JSON, got nil")
	}
}

func TestDecodeServerMessageReadError(t *testing.T) {
	dec := protocol.NewDecoder(strings.NewReader(""))
	resp, event, err := dec.DecodeServerMessage()
	if err == nil {
		t.Fatal("DecodeServerMessage: want error on empty input, got nil")
	}
	if resp != nil || event != nil {
		t.Fatalf("DecodeServerMessage: want nil resp/event on error, got resp=%+v event=%+v", resp, event)
	}
}

func TestDecodeServerMessageProbeUnmarshalError(t *testing.T) {
	dec := protocol.NewDecoder(strings.NewReader("not json\n"))
	if _, _, err := dec.DecodeServerMessage(); err == nil {
		t.Fatal("DecodeServerMessage: want error for malformed JSON, got nil")
	}
}

func TestDecodeServerMessageResponseUnmarshalError(t *testing.T) {
	// Has an "id" field (so the probe succeeds and picks the Response
	// shape) but "ok" is a string where Response expects a bool.
	dec := protocol.NewDecoder(strings.NewReader(`{"id":1,"ok":"nope"}` + "\n"))
	if _, _, err := dec.DecodeServerMessage(); err == nil {
		t.Fatal("DecodeServerMessage: want error when the Response shape doesn't match, got nil")
	}
}

func TestDecodeServerMessageEventUnmarshalError(t *testing.T) {
	// No "id" field (so the probe picks the Event shape), but "type" is a
	// number where Event expects a string.
	dec := protocol.NewDecoder(strings.NewReader(`{"type":123}` + "\n"))
	if _, _, err := dec.DecodeServerMessage(); err == nil {
		t.Fatal("DecodeServerMessage: want error when the Event shape doesn't match, got nil")
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
