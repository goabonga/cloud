// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"

	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/httpsec"
)

// Port extracts the port infra-agent's GOA_REPLICATION_ADDR/-replication-addr
// listens on, so a caller holding a peer's bare address (NodeSpec.Address)
// can build that peer's replication address with net.JoinHostPort. Kept
// here rather than inlined at call sites because cmd/agent/main.go already
// has an unrelated local variable named "net" shadowing the package.
func Port(listenAddr string) (string, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	return port, err
}

// validUID matches the UID shape resources are given throughout the
// codebase (see e.g. ExecDiskBackend.mapperName). Rejecting anything else
// before it reaches filepath.Join keeps a disk UID from ever being read as
// a path (no "/", no "..").
var validUID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// Server serves this node's disk backing files to other nodes pulling a
// replica, and answers a reachability probe. Every route but /ping requires
// a request signed with key (see sign/verifyRequest in auth.go).
type Server struct {
	dir        string
	key        []byte
	nodeID     string
	logger     *slog.Logger
	mux        *http.ServeMux
	snapshotMu sync.Mutex
	clone      func(*os.File, *os.File) error
	tlsConfig  *tls.Config
}

// NewServer returns a Server serving disk backing files out of dir (the
// same directory ExecDiskBackend manages, typically <stateDir>/disks). key
// authenticates both directions: it must be the same key every other node
// in the cluster was given.
func NewServer(dir string, key []byte, nodeID string, logger *slog.Logger, configs ...*tls.Config) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{dir: dir, key: key, nodeID: nodeID, logger: logger, mux: http.NewServeMux()}
	s.clone = func(dst, src *os.File) error { return unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())) }
	if len(configs) > 0 && configs[0] != nil {
		s.tlsConfig = configs[0].Clone()
		s.tlsConfig.MinVersion = tls.VersionTLS13
		s.tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}
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

// handlePullDisk streams an immutable point-in-time snapshot.
// http.ServeContent handles conditional GETs and Range requests, which is
// what makes a pull resumable: a client retrying after a partial transfer
// sends Range: bytes=<have>- and picks up where it left off.
func (s *Server) handlePullDisk(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if !validUID.MatchString(uid) {
		http.Error(w, "invalid disk uid", http.StatusBadRequest)
		return
	}
	snapshot, digest, err := s.openSnapshot(uid, r.URL.Query().Get("version"))
	if err != nil {
		switch {
		case errors.Is(err, errInvalidVersion):
			http.Error(w, "invalid snapshot version", http.StatusBadRequest)
		case errors.Is(err, os.ErrNotExist):
			http.Error(w, "disk or snapshot not found", http.StatusNotFound)
		default:
			s.logger.Error("replication: snapshot", "uid", uid, "err", err)
			http.Error(w, "immutable snapshot unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	defer func() { _ = snapshot.Close() }()
	info, err := snapshot.Stat()
	if err != nil {
		http.Error(w, "snapshot unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", `"`+digest+`"`)
	w.Header().Set("X-Infra-SHA256", digest)
	http.ServeContent(w, r, uid+".img", info.ModTime(), snapshot)
}

// ListenAndServe runs the replication server on addr until ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if s.tlsConfig == nil {
		return fmt.Errorf("replication: management TLS credentials required")
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		TLSConfig:         s.tlsConfig,
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
	err := srv.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

var errInvalidVersion = errors.New("invalid snapshot version")

var validDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// openSnapshot captures an atomic filesystem reflink, never a mutable live copy.
func (s *Server) openSnapshot(uid, version string) (*os.File, string, error) {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	if !validUID.MatchString(uid) {
		return nil, "", fmt.Errorf("invalid disk uid")
	}
	if version != "" && !validDigest.MatchString(version) {
		return nil, "", errInvalidVersion
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = root.Close() }()
	source, err := root.Open(uid + ".img")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = source.Close() }()
	dir := filepath.Join(".snapshots", uid)
	if err := root.MkdirAll(dir, 0700); err != nil {
		return nil, "", err
	}
	cache, err := root.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = cache.Close() }()
	if version != "" {
		f, err := cache.Open(version + ".img")
		return f, version, err
	}
	temp := ".capture-" + rand.Text()
	snapshot, err := cache.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = snapshot.Close(); _ = cache.Remove(temp) }()
	if err := s.clone(snapshot, source); err != nil {
		return nil, "", fmt.Errorf("atomic reflink snapshot required: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, snapshot); err != nil {
		return nil, "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if err := snapshot.Sync(); err != nil {
		return nil, "", err
	}
	if err := snapshot.Close(); err != nil {
		return nil, "", err
	}
	name := digest + ".img"
	if err := cache.Rename(temp, name); err != nil {
		return nil, "", err
	}
	directory, err := cache.Open(".")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = directory.Close() }()
	files, err := directory.ReadDir(-1)
	if err != nil {
		return nil, "", err
	}
	// Existing readers retain their immutable inode; an evicted resume restarts.
	for _, file := range files {
		if file.Name() != name {
			_ = cache.Remove(file.Name())
		}
	}
	if err := directory.Sync(); err != nil {
		return nil, "", err
	}
	f, err := cache.Open(name)
	return f, digest, err
}
