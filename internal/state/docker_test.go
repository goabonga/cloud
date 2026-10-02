// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/goabonga/infrastructure/internal/state"
)

// postgresDSN and etcdEndpoint address the ephemeral backends started on
// first use by ensurePostgresContainer/ensureEtcdContainer, when docker is
// available. Tests that need a real backend call the matching ensure*
// function and then check the *Available flag, skipping otherwise. Backends
// are started lazily, one per package test run, so a test run that never
// touches PostgresStore or EtcdStore never shells out to docker at all.
var (
	postgresOnce      sync.Once
	postgresDSN       string
	postgresAvailable bool

	etcdOnce      sync.Once
	etcdEndpoint  string
	etcdAvailable bool

	cleanupMu  sync.Mutex
	cleanupFns []func()
)

// TestMain runs the package's tests and then, only if a docker-backed
// container was actually started by one of them, tears it down. Startup is
// intentionally NOT done here: it happens lazily in ensurePostgresContainer
// and ensureEtcdContainer so that test runs which never exercise
// PostgresStore or EtcdStore never invoke docker.
func TestMain(m *testing.M) {
	code := m.Run()

	cleanupMu.Lock()
	fns := cleanupFns
	cleanupMu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}

	os.Exit(code)
}

func registerCleanup(fn func()) {
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	cleanupFns = append(cleanupFns, fn)
}

// ensurePostgresContainer starts the shared postgres container on first
// call, if docker is available; later calls are no-ops. Callers must check
// postgresAvailable afterwards.
func ensurePostgresContainer() {
	postgresOnce.Do(func() {
		if !dockerAvailable() {
			fmt.Fprintln(os.Stderr, "internal/state tests: docker not available, skipping postgres-backed tests")
			return
		}
		dsn, cleanup, err := startPostgresContainer()
		if err != nil {
			fmt.Fprintf(os.Stderr, "internal/state tests: postgres container unavailable: %v\n", err)
			return
		}
		// NewPostgresStore's CREATE TABLE IF NOT EXISTS races when two
		// sessions both see the table missing and collide inserting into
		// pg_type - so the schema is created once here, serially, before any
		// parallel test gets a chance to open its own store concurrently.
		schema, err := state.NewPostgresStore(context.Background(), dsn)
		if err != nil {
			cleanup()
			fmt.Fprintf(os.Stderr, "internal/state tests: postgres schema setup failed: %v\n", err)
			return
		}
		_ = schema.Close()
		postgresDSN = dsn
		postgresAvailable = true
		registerCleanup(cleanup)
	})
}

// ensureEtcdContainer starts the shared etcd container on first call, if
// docker is available; later calls are no-ops. Callers must check
// etcdAvailable afterwards.
func ensureEtcdContainer() {
	etcdOnce.Do(func() {
		if !dockerAvailable() {
			fmt.Fprintln(os.Stderr, "internal/state tests: docker not available, skipping etcd-backed tests")
			return
		}
		endpoint, cleanup, err := startEtcdContainer()
		if err != nil {
			fmt.Fprintf(os.Stderr, "internal/state tests: etcd container unavailable: %v\n", err)
			return
		}
		etcdEndpoint = endpoint
		etcdAvailable = true
		registerCleanup(cleanup)
	})
}

// dockerAvailable reports whether the docker CLI can reach a daemon.
func dockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

// startPostgresContainer runs a disposable postgres:16 container and waits
// for it to accept connections, returning a connection DSN and a cleanup
// that stops the container.
func startPostgresContainer() (dsn string, cleanup func(), err error) {
	out, err := exec.Command("docker", "run", "--rm", "-d",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_USER=postgres",
		"-e", "POSTGRES_DB=state",
		"-p", "0:5432/tcp",
		"postgres:16",
	).Output()
	if err != nil {
		return "", nil, fmt.Errorf("docker run postgres: %w", err)
	}
	id := strings.TrimSpace(string(out))
	cleanup = func() { _ = exec.Command("docker", "rm", "-f", id).Run() }

	port, err := dockerHostPort(id, "5432/tcp")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	dsn = fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%s/state?sslmode=disable", port)

	err = waitFor(30*time.Second, func() error {
		pool, perr := pgxpool.New(context.Background(), dsn)
		if perr != nil {
			return perr
		}
		defer pool.Close()
		return pool.Ping(context.Background())
	})
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("postgres did not become ready: %w", err)
	}
	return dsn, cleanup, nil
}

// startEtcdContainer runs a disposable etcd container (the server release
// matching go.mod's go.etcd.io/etcd/client/v3 version) and waits for it to
// accept client requests, returning its endpoint and a cleanup that stops
// the container.
func startEtcdContainer() (endpoint string, cleanup func(), err error) {
	out, err := exec.Command("docker", "run", "--rm", "-d",
		"-p", "0:2379/tcp",
		"gcr.io/etcd-development/etcd:v3.7.2",
		"/usr/local/bin/etcd",
		"--data-dir=/etcd-data",
		"--listen-client-urls=http://0.0.0.0:2379",
		"--advertise-client-urls=http://0.0.0.0:2379",
	).Output()
	if err != nil {
		return "", nil, fmt.Errorf("docker run etcd: %w", err)
	}
	id := strings.TrimSpace(string(out))
	cleanup = func() { _ = exec.Command("docker", "rm", "-f", id).Run() }

	port, err := dockerHostPort(id, "2379/tcp")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	endpoint = "127.0.0.1:" + port

	err = waitFor(30*time.Second, func() error {
		cli, cerr := clientv3.New(clientv3.Config{
			Endpoints:   []string{endpoint},
			DialTimeout: 2 * time.Second,
		})
		if cerr != nil {
			return cerr
		}
		defer func() { _ = cli.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, cerr = cli.Get(ctx, "readiness-probe")
		return cerr
	})
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("etcd did not become ready: %w", err)
	}
	return endpoint, cleanup, nil
}

// dockerHostPort returns the ephemeral host port docker assigned to
// containerPort (e.g. "5432/tcp") on the container identified by id.
func dockerHostPort(id, containerPort string) (string, error) {
	format := fmt.Sprintf(`{{(index (index .NetworkSettings.Ports %q) 0).HostPort}}`, containerPort)
	out, err := exec.Command("docker", "inspect", "--format", format, id).Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect %s: %w", id, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// waitFor retries probe until it succeeds or timeout elapses.
func waitFor(timeout time.Duration, probe func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = probe(); lastErr == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return lastErr
}
