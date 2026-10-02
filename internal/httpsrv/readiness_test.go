// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package httpsrv_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/state"
)

type offlineStore struct{ state.Store }

func (offlineStore) Get(string) ([]byte, error) { return nil, errors.New("offline") }
func TestReadinessSeparatesStorageFromLiveness(t *testing.T) {
	h := httpsrv.New(offlineStore{}).Handler()
	for _, check := range []struct {
		path string
		code int
	}{{"/healthz", http.StatusOK}, {"/readyz", http.StatusServiceUnavailable}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", check.path, nil))
		if w.Code != check.code {
			t.Fatalf("%s: %d", check.path, w.Code)
		}
	}
}
