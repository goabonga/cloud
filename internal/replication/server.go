// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/goabonga/infrastructure/internal/httpsec"
)

// validUID matches the UID shape resources are given throughout the
// codebase (see e.g. ExecDiskBackend.mapperName). Rejecting anything else
// before it reaches filepath.Join keeps a disk UID from ever being read as
// a path (no "/", no "..").
var validUID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// Server serves this node's disk backing files to other nodes pulling a
// replica, and answers a reachability probe. Every route but /ping requires
// a request signed with key (see sign/verifyRequest in auth.go).
type Server struct {
	dir    string
	key    []byte
	nodeID string
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewServer returns a Server serving disk backing files out of dir (the
// same directory ExecDiskBackend manages, typically <stateDir>/disks). key
// authenticates both directions: it must be the same key every other node
// in the cluster was given.
func NewServer(dir string, key []byte, nodeID string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{dir: dir, key: key, nodeID: nodeID, logger: logger, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /ping", s.handlePing)
	s.mux.HandleFunc("GET /disks/{uid}", s.handlePullDisk)
}

// Handler returns the routed handler, every route but /ping gated on a
// valid signature.
func (s *Server) Handler() http.Handler {
	return httpsec.Headers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			s.mux.ServeHTTP(w, r)
			return
		}
		if !verifyRequest(r, s.key, time.Now()) {
			http.Error(w, "signature invalid or expired", http.StatusUnauthorized)
			return
		}
		s.mux.ServeHTTP(w, r)
	}))
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Infra-Node", s.nodeID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handlePullDisk streams the requested disk's current backing file.
// http.ServeContent handles conditional GETs and Range requests, which is
// what makes a pull resumable: a client retrying after a partial transfer
// sends Range: bytes=<have>- and picks up where it left off.
func (s *Server) handlePullDisk(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if !validUID.MatchString(uid) {
		http.Error(w, "invalid disk uid", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.dir, uid+".img")
	f, err := os.Open(path) // #nosec G304 G703 -- uid is validated against validUID above
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "disk not found", http.StatusNotFound)
			return
		}
		s.logger.Error("replication: open disk", "uid", uid, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		s.logger.Error("replication: stat disk", "uid", uid, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, uid+".img", info.ModTime(), f)
}

// ListenAndServe runs the replication server on addr until ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// ctx is awaited below and then Done; the shutdown timeout deliberately
	// starts a fresh, unexpired context rather than reusing it.
	go func() { // #nosec G118 -- shutdown needs a context that is not already canceled
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
