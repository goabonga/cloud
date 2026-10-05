// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		fail bool
	}{
		{[]string{"--version"}, "cloud 0.0.0\n", false},
		{[]string{"--help"}, "cloud [command]", false},
		{nil, "cloud [command]", false},
		{[]string{"completion", "bash"}, "# bash completion V2 for cloud", false},
		{[]string{"completion", "zsh"}, "#compdef cloud", false},
		{[]string{"status", "extra"}, "", true},
		{[]string{"create"}, "", true},
		{[]string{"--unknown"}, "", true},
	} {
		var output bytes.Buffer
		err := Run(context.Background(), tc.args, &output, "0.0.0")
		if (err != nil) != tc.fail || !strings.Contains(output.String(), tc.want) {
			t.Fatalf("args %v: output %q, error %v", tc.args, output.String(), err)
		}
	}
}
