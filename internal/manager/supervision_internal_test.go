// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package manager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkloadIdentityDetectsExitAndPIDReuse(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	start, err := processStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !processMatches(cmd.Process.Pid, start) {
		t.Fatal("live workload not detected")
	}
	if processMatches(cmd.Process.Pid, start+"0") {
		t.Fatal("reused PID accepted")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if processMatches(cmd.Process.Pid, start) || processMatches(1, start) {
		t.Fatal("dead or init process accepted")
	}
}
func TestEntrypointStartErrorsAreReported(t *testing.T) {
	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(filepath.Join(rootfs, "bin"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "bin", "sh"), []byte("shell"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	backend := NewExecComputeBackendWithRunner(dir, filepath.Join(dir, "netns"), filepath.Join(dir, "cgroup"), func(context.Context, string, ...string) (string, error) { return "", nil })
	if _, _, err := backend.startEntrypoint(ComputeRequest{UID: "c"}, "ns-c", rootfs, "true"); err == nil {
		t.Fatal("launcher failure ignored")
	}
}
func TestRuntimeSnapshotPreservesAppliedRequest(t *testing.T) {
	backend := NewExecComputeBackend(t.TempDir())
	req := ComputeRequest{UID: "c", Image: "image", Command: "old", Env: map[string]string{"KEY": "value"}}
	if err := backend.saveComputeState(req.UID, computeState{Request: req, PID: 2, StartTime: "123"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := backend.loadComputeState(req.UID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Request.Command != "old" || snapshot.Request.Env["KEY"] != "value" || snapshot.StartTime != "123" {
		t.Fatalf("invalid snapshot: %#v", snapshot)
	}
}

func TestComputeRecreatesDriftAndMissingRuntimeState(t *testing.T) {
	dir := t.TempDir()
	ns, _, _ := computeNames("c")
	exists := false
	additions, deletions := 0, 0
	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "ip" && len(args) >= 2 && args[0] == "netns" {
			switch args[1] {
			case "list":
				if exists {
					return ns + "\n", nil
				}
			case "add":
				exists = true
				additions++
			case "del":
				exists = false
				deletions++
			}
		}
		return "", nil
	}
	backend := NewExecComputeBackendWithRunner(dir, filepath.Join(dir, "netns"), filepath.Join(dir, "cgroup"), run)
	req := ComputeRequest{UID: "c", Bridge: "br-v", IP: "10.0.0.2", Prefix: 24, Gateway: "10.0.0.1", SGChain: "SG", CPU: 1, MemoryMB: 128}
	for range 2 {
		if _, err := backend.EnsureCompute(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if additions != 1 || deletions != 0 {
		t.Fatalf("unchanged workload recreated: adds=%d dels=%d", additions, deletions)
	}
	req.IP = "10.0.0.3"
	if _, err := backend.EnsureCompute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if additions != 2 || deletions != 1 {
		t.Fatalf("drift ignored: adds=%d dels=%d", additions, deletions)
	}
	oldRulesRemoved := false
	for _, call := range calls {
		if strings.Contains(call, "-D") && strings.Contains(call, "10.0.0.2") {
			oldRulesRemoved = true
		}
	}
	if !oldRulesRemoved {
		t.Fatal("old firewall rules retained after address drift")
	}
	if err := os.Remove(filepath.Join(dir, "compute", "c.runtime.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.EnsureCompute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if additions != 3 || deletions != 2 {
		t.Fatal("namespace without runtime state was accepted")
	}
}
