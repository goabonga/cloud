// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package manager

import (
	"strings"
	"testing"
)

func TestUnprivilegedDevicesDoNotExposeHost(t *testing.T) {
	script := buildEntryScript(ComputeRequest{}, "/rootfs", "true", "/bin/sh", "/pid", "/cg", false, true)
	for _, unsafe := range []string{"mount --rbind /dev", "mount --rbind /sys", "mount -t cgroup2"} {
		if strings.Contains(script, unsafe) {
			t.Fatalf("host exposure: %s", unsafe)
		}
	}
	for _, required := range []string{"mount --make-rprivate /", "-o ro,nosuid,nodev,noexec", "mknod -m 666 /rootfs/dev/null c 1 3", "newinstance"} {
		if !strings.Contains(script, required) {
			t.Fatalf("missing protection: %s", required)
		}
	}
}
