# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Release dependency bumps only when they affect an implemented artifact."""

import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def git(*args):
    return subprocess.check_output(["git", "-c", "core.hooksPath=/dev/null", *args]).decode()


def linked_modules(directory):
    env = {**os.environ, "GOFLAGS": "-mod=readonly", "GOWORK": "off", "GOTOOLCHAIN": "auto"}
    template = "{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{with .Replace}} => {{.Path}}@{{.Version}}{{end}}{{end}}{{end}}"
    result = subprocess.check_output(["go", "list", "-deps", "-f", template, "./cmd/..."],
                                     cwd=directory, env=env).decode()
    return {line for line in result.splitlines() if line.strip()}


def go_bump_ships():
    # The bootstrap has no Go binaries, so its toolchain cannot ship yet.
    files = git("ls-tree", "-r", "--name-only", "HEAD", "--", "cmd").splitlines()
    if not any(p.endswith(".go") and not p.endswith("_test.go") for p in files):
        return False
    parent = git("rev-parse", "HEAD^").strip()
    diff = git("diff", parent, "HEAD", "--", "go.mod")
    if re.search(r"^[+-](?:go|toolchain) ", diff, re.M):
        return True
    # Resolve the module graph only. Never build or run PR code, or download
    # tools from that code. Git hooks stay disabled even in temporary trees.
    with tempfile.TemporaryDirectory(prefix="cloud-linked-modules-") as temp:
        before = Path(temp, "before")
        git("worktree", "add", "--quiet", "--detach", str(before), parent)
        try:
            return linked_modules(before) != linked_modules(Path.cwd())
        finally:
            git("worktree", "remove", "--force", str(before))


def npm_bump_ships(message):
    package = Path("www/package.json")
    if not package.is_file():
        return False
    manifest = json.loads(package.read_text())
    if not manifest.get("scripts", {}).get("build"):
        return False
    name = None
    types = []
    for line in message.splitlines():
        match = re.match(r"^- dependency-name:\s*(.+)$", line)
        if match:
            name = match[1].strip("\"'")
        match = re.match(r"^\s+dependency-type:\s*(.+)$", line)
        if match:
            kind = match[1].strip("\"'")
            types.append((name, kind))
    # Runtime, Vite/plugins, indirect or absent metadata can change the
    # emitted bundle. Ordinary direct development tooling does not.
    return not types or any(kind != "direct:development" or name == "vite"
                            or (name or "").startswith("@vitejs/") for name, kind in types)
