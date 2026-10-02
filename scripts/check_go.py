#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Validate a registered Go executable and its actual internal dependencies."""

import argparse
import json
import os
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


def affected_packages(owned, changed_files, entries):
    if changed_files is None or any(path in {"go.mod", "go.sum"} for path in changed_files):
        return owned
    module = run("go", "list", "-m")
    touched = {module + "/" + str(Path(path).parent) for path in changed_files if path.endswith(".go")}
    documents = run("go", "list", "-deps", "-json", *entries)
    decoder = json.JSONDecoder()
    graph = {}
    while documents.strip():
        package, end = decoder.raw_decode(documents.lstrip())
        graph[package["ImportPath"]] = package
        documents = documents.lstrip()[end:]
    affected = {package for package in owned
                if package in touched or touched.intersection(graph[package].get("Deps", []))}
    if any(path.startswith("www/") or path == "assets/cloud.svg" for path in changed_files) and "./cmd/ssr" in entries:
        # Exercise the server side of the browser/server integration pair.
        _, server_packages = packages("cloud-ssr")
        affected.update(server_packages)
    return sorted(affected)


def check_components(components, changed_files=None):
    entries, owned = set(), set()
    for component in components:
        component_entries, component_packages = packages(component)
        entries.update(component_entries)
        owned.update(component_packages)
    if not entries:
        raise ValueError("No Go components selected")
    entries = sorted(entries)
    selected = affected_packages(sorted(owned), changed_files, entries)
    module = run("go", "list", "-m") + "/"
    local = ["./" + package.removeprefix(module) for package in selected]
    print("Go components: " + ", ".join(components))
    print("Affected packages: " + (", ".join(local) or "none; validate executable builds"))
    if local:
        files = [str(path) for directory in local for path in Path(directory).glob("*.go")]
        formatted = run("gofmt", "-l", *files) if files else ""
        if formatted:
            raise ValueError("Go formatting required: " + formatted)
        subprocess.run(["go", "vet", *selected], check=True)
        subprocess.run(["go", "test", "-race", *selected], check=True)
        subprocess.run(["go", "run", "github.com/securego/gosec/v2/cmd/gosec@v2.29.0", *local], check=True)
    # Rebuild affected executables even when only a dependent package changed.
    subprocess.run(["go", "build", "-o", "/dev/null", *entries], check=True)


def check(component):
    check_components([component])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("component", nargs="?")
    parser.add_argument("--ci", action="store_true", help="Use selected components and changed files from CI")
    args = parser.parse_args()
    if args.ci:
        components = json.loads(os.environ["GO_COMPONENTS"])
        files = json.loads(os.environ["CHANGED_FILES"])
        if not isinstance(components, list) or not all(isinstance(name, str) for name in components):
            raise ValueError("Invalid Go component list")
        if files is not None and (not isinstance(files, list) or not all(isinstance(path, str) for path in files)):
            raise ValueError("Invalid changed file list")
        check_components(components, files)
    elif args.component:
        check(args.component)
    else:
        parser.error("provide a component or --ci")


if __name__ == "__main__":
    main()
