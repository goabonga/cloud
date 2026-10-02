// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "time"

// Event records a notable occurrence for a resource.
type Event struct {
	// Type is "Normal" or "Warning".
	Type string `json:"type"`
	// Reason is a one-word CamelCase reason.
	Reason string `json:"reason"`
	// Message is a human-readable description.
	Message string `json:"message"`
	// Source identifies the component that generated the event.
	Source string `json:"source"`
	// Regarding identifies the resource this event is about.
	Regarding ObjectReference `json:"regarding"`
	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`
}

// Event type constants.
const (
	// EventNormal marks an informational event.
	EventNormal = "Normal"
	// EventWarning marks a warning event.
	EventWarning = "Warning"
)
