#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Validate a registered Go executable and its actual internal dependencies."""

import argparse
from pathlib import Path
import subprocess
import tomllib

from ci_automation import run


def packages(component):
    config = tomllib.loads(Path("multicz.toml").read_text())
    declared = config["plugins"]["go-deps"]["packages"]
    if component not in declared:
        raise ValueError(f"Unknown Go component: {component}")
    entry = declared[component]
    entry = [entry] if isinstance(entry, str) else entry
    module = run("go", "list", "-m")
    paths = run("go", "list", "-deps", "-f", "{{.ImportPath}}", *entry).splitlines()
    owned = sorted(path for path in set(paths) if path.startswith(module + "/"))
    if not owned:
        raise ValueError(f"No Go packages resolved for {component}")
    return entry, owned


PHASES = ("vet", "test", "build", "gosec")


def check(components, phase):
    """Run one validation phase for the given Go components.

    vet checks formatting and runs go vet, test runs the race-enabled tests,
    build builds the executables and gosec scans their packages. CI runs each
    phase as its own step so the tests and gosec get their own reports.
    """
    owned, entries, local = [], [], []
    root = run("go", "list", "-m") + "/"
    for component in components:
        entry, packages_ = packages(component)
        entries += [item for item in entry if item not in entries]
        owned += [package for package in packages_ if package not in owned]
    local = ["./" + package.removeprefix(root) for package in owned]
    if phase == "vet":
        formatted = run("gofmt", "-l", *[str(path) for directory in local for path in Path(directory).glob("*.go")])
        if formatted:
            raise ValueError("Go formatting required: " + formatted)
        subprocess.run(["go", "vet", *owned], check=True)
    elif phase == "test":
        subprocess.run(["go", "test", "-race", *owned], check=True)
    elif phase == "build":
        subprocess.run(["go", "build", "-o", "/dev/null", *entries], check=True)
    else:
        subprocess.run(["go", "run", "github.com/securego/gosec/v2/cmd/gosec@v2.29.0", *local], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--phase", choices=PHASES, required=True)
    parser.add_argument("components", nargs="+")
    args = parser.parse_args()
    check(args.components, args.phase)


if __name__ == "__main__":
    main()
