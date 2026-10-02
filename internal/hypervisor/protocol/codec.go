// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Encoder writes NDJSON messages: one json.Marshal'd value per line.
type Encoder struct {
	w *bufio.Writer
}

// NewEncoder wraps w. Every Encode call flushes immediately — there is no
// batching to opt out of on a protocol this low-volume.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: bufio.NewWriter(w)}
}

// Encode marshals v and writes it as one line.
func (e *Encoder) Encode(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("protocol: marshal %T: %w", v, err)
	}
	if _, err := e.w.Write(b); err != nil {
		return fmt.Errorf("protocol: write: %w", err)
	}
	if err := e.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("protocol: write: %w", err)
	}
	return e.w.Flush()
}

// Decoder reads NDJSON messages: one json.Unmarshal per line.
type Decoder struct {
	r *bufio.Reader
}

// NewDecoder wraps r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// Decode reads the next line and unmarshals it into v.
func (d *Decoder) Decode(v any) error {
	line, err := d.r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return err // typically io.EOF; let the caller decide whether that's expected
	}
	if err := json.Unmarshal(line, v); err != nil {
		return fmt.Errorf("protocol: unmarshal %q: %w", line, err)
	}
	return nil
}

// idProbe checks whether a line is a Response (has an "id" field) or an
// Event (doesn't), without committing to either shape up front.
type idProbe struct {
	ID *int `json:"id"`
}

// DecodeServerMessage reads the next line from a server (cmd/hypervisor)
// and decodes it as either a Response to some earlier Request (ID set) or
// an unsolicited Event (no ID field in the encoded JSON) — exactly one of
// the two returned pointers is non-nil on success.
func (d *Decoder) DecodeServerMessage() (resp *Response, event *Event, err error) {
	line, err := d.r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, nil, err
	}

	var probe idProbe
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, nil, fmt.Errorf("protocol: unmarshal %q: %w", line, err)
	}

	if probe.ID != nil {
		resp = &Response{}
		if err := json.Unmarshal(line, resp); err != nil {
			return nil, nil, fmt.Errorf("protocol: unmarshal %q: %w", line, err)
		}
		return resp, nil, nil
	}

	event = &Event{}
	if err := json.Unmarshal(line, event); err != nil {
		return nil, nil, fmt.Errorf("protocol: unmarshal %q: %w", line, err)
	}
	return nil, event, nil
}
