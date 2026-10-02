// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevicePollingRecognizesSlowDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"slow_down"}`))
	}))
	defer server.Close()
	_, _, pending, err := pollDeviceToken(server.URL, "device")
	if !pending || !errors.Is(err, errDeviceSlowDown) {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
}
