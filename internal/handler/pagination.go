// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func pageParameters(r *http.Request) (int, string, error) {
	size := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			return 0, "", fmt.Errorf("limit must be between 1 and 1000")
		}
		size = n
	}
	token := r.URL.Query().Get("continue")
	if len(token) > 4096 {
		return 0, "", fmt.Errorf("invalid continuation token")
	}
	var after string
	if token != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(decoded) == 0 {
			return 0, "", fmt.Errorf("invalid continuation token")
		}
		after = string(decoded)
	}
	return size, after, nil
}

// writeList paginates only the already-authorized/redacted resource set.
func writeList[S any, ST any](w http.ResponseWriter, r *http.Request, kind string, items []resource.Resource[S, ST]) {
	size, after, err := pageParameters(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Metadata.UID < items[j].Metadata.UID })
	start := sort.Search(len(items), func(i int) bool { return items[i].Metadata.UID > after })
	end := min(len(items), start+size)
	next := ""
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(items[end-1].Metadata.UID))
	}
	writeJSON(w, http.StatusOK, resource.List[S, ST]{APIVersion: resource.APIVersion, Kind: kind, Items: items[start:end], Continue: next})
}
