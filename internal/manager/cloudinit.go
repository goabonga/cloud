// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cloudInitPort is the port the agent serves the NoCloud datasource on. It
// listens on every interface, so a VPC's instances reach it at their own
// subnet gateway - the host's address on their bridge - without any extra
// routing.
const cloudInitPort = 8912

// cloudInitSeed is one instance's NoCloud datasource: meta-data identifies
// it, user-data is its cloud-config (or a raw user-data override).
type cloudInitSeed struct {
	MetaData string
	UserData string
}

// cloudInitSeeds serves every micro-VM's NoCloud seed from one HTTP listener,
// started the first time it is needed. Cloud-init on the guest is pointed at
// it with a "ds=nocloud-net;s=http://<gateway>:8912/<uid>/" kernel cmdline
// directive; it fetches "<uid>/meta-data", "<uid>/user-data" and
// "<uid>/vendor-data" (always empty - cloud-init requires the file to exist).
type cloudInitSeeds struct {
	mu      sync.Mutex
	started bool
	port    int // 0 lets the kernel pick one, used in tests
	addr    string
	seeds   map[string]cloudInitSeed
}

func newCloudInitSeeds(port int) *cloudInitSeeds {
	return &cloudInitSeeds{port: port, seeds: map[string]cloudInitSeed{}}
}

// ensureStarted starts the HTTP listener once; later calls are a no-op.
func (c *cloudInitSeeds) ensureStarted() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return nil
	}
	hostport := net.JoinHostPort("", strconv.Itoa(c.port))
	ln, err := net.Listen("tcp", hostport)
	if err != nil {
		return fmt.Errorf("manager: cloud-init listen %s: %w", hostport, err)
	}
	c.addr = ln.Addr().String()
	srv := &http.Server{Handler: http.HandlerFunc(c.handle), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "manager: cloud-init server %s: %v\n", hostport, err)
		}
	}()
	c.started = true
	return nil
}

// set records uid's seed, replacing any previous one.
func (c *cloudInitSeeds) set(uid string, seed cloudInitSeed) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seeds[uid] = seed
}

// remove drops uid's seed; removing an absent one is not an error.
func (c *cloudInitSeeds) remove(uid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.seeds, uid)
}

// handle serves "/<uid>/meta-data", "/<uid>/user-data" and
// "/<uid>/vendor-data" from the matching registered seed.
func (c *cloudInitSeeds) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	uid, kind := parts[0], parts[1]

	c.mu.Lock()
	seed, ok := c.seeds[uid]
	c.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	switch kind {
	case "meta-data":
		_, _ = w.Write([]byte(seed.MetaData))
	case "user-data":
		_, _ = w.Write([]byte(seed.UserData))
	case "vendor-data":
		// Intentionally empty: cloud-init requires the file to exist, not to
		// hold anything.
	default:
		http.NotFound(w, r)
	}
}

// buildCloudInitSeed renders req's NoCloud seed: req.UserData verbatim when
// set (the caller owns its format, typically "#cloud-config" or a "#!"
// script), or else a minimal cloud-config from the hostname and SSH key.
func buildCloudInitSeed(req MicroVMRequest) cloudInitSeed {
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", req.UID, seedHostname(req))

	if req.UserData != "" {
		return cloudInitSeed{MetaData: metaData, UserData: req.UserData}
	}

	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if req.Hostname != "" {
		fmt.Fprintf(&b, "hostname: %s\n", req.Hostname)
	}
	if req.SSHAuthorizedKey != "" {
		b.WriteString("ssh_authorized_keys:\n")
		fmt.Fprintf(&b, "  - %s\n", req.SSHAuthorizedKey)
	}
	return cloudInitSeed{MetaData: metaData, UserData: b.String()}
}

// seedHostname returns the instance's hostname, falling back to its UID.
func seedHostname(req MicroVMRequest) string {
	if req.Hostname != "" {
		return req.Hostname
	}
	return req.UID
}

// cloudInitCmdline returns the "ds=nocloud-net;..." kernel cmdline directive
// pointing cloud-init at the agent's seed server through the subnet gateway.
func cloudInitCmdline(req MicroVMRequest) string {
	return fmt.Sprintf("ds=nocloud-net;s=http://%s/%s/", net.JoinHostPort(req.Gateway, strconv.Itoa(cloudInitPort)), req.UID)
}
