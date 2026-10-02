// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package fleet

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunHelpAndVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"root version", []string{"--version"}, "cloud-fleet 0.0.0\n"},
		{"root help", []string{"--help"}, "Modes: controller, agent"},
		{"no mode", nil, "Usage: cloud-fleet"},
		{"controller version", []string{"controller", "--version"}, "cloud-fleet controller 0.0.0\n"},
		{"agent version", []string{"agent", "--version"}, "cloud-fleet agent 0.0.0\n"},
		{"controller help", []string{"controller", "--help"}, "127.0.0.1:8090"},
		{"agent help", []string{"agent", "--help"}, "127.0.0.1:8091"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(context.Background(), tc.args, &output, "0.0.0"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tc.want) {
				t.Fatalf("output %q, want %q", output.String(), tc.want)
			}
		})
	}
}

func TestRunRejectsInvalidModesAndArguments(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"--unknown"}, {"--version", "controller"},
		{"controller", "--unknown"}, {"agent", "unexpected"},
		{"controller", "--addr", "invalid"}, {"agent", "--addr", "invalid"},
	} {
		if err := Run(context.Background(), args, io.Discard, "0.0.0"); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}

func TestCancelledModesStopTheirServer(t *testing.T) {
	for _, mode := range []string{"controller", "agent"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := Run(ctx, []string{mode, "--addr", "127.0.0.1:0"}, io.Discard, "0.0.0"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
