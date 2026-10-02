#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Compile scripts and select pytest files through their local import graph."""

import argparse
import ast
import json
import os
from pathlib import Path
import subprocess

SHARED_FILES = {"pytest.ini", "scripts/pyproject.toml", "scripts/uv.lock",
                "scripts/check_scripts.py", "scripts/ci_automation.py",
                "scripts/dependency_release.py"}


def imports(path):
    tree = ast.parse(path.read_text(), filename=str(path))
    modules = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            modules.update(alias.name.split(".")[0] for alias in node.names)
        elif isinstance(node, ast.ImportFrom) and node.module:
            modules.add(node.module.split(".")[0])
    return modules


def select_tests(files):
    """Return selected files, or None to conservatively run the full suite."""
    if files is None or SHARED_FILES.intersection(files):
        return None
    modules = {path.stem: path for path in Path("scripts").glob("*.py")}
    tests = sorted(Path("scripts/tests").glob("test_*.py"))
    graph = {name: imports(path) for name, path in modules.items()}
    selected, changed = set(), set()
    for filename in files:
        path = Path(filename)
        if filename == "scripts/CHANGELOG.md" or not filename.startswith("scripts/"):
            continue
        if filename.startswith("scripts/tests/test_") and path.suffix == ".py" and path in tests:
            selected.add(filename)
        elif path.parent == Path("scripts") and path.suffix == ".py" and path.is_file():
            changed.add(path.stem)
        else:
            # Deleted files, fixtures, nested helpers and new formats need all tests.
            return None
    affected = set(changed)
    while True:
        consumers = {name for name, deps in graph.items() if deps.intersection(affected)}
        if consumers.issubset(affected):
            break
        affected.update(consumers)
    matched = set()
    for test in tests:
        dependencies = imports(test)
        if dependencies.intersection(affected):
            selected.add(str(test))
            matched.update(dependencies.intersection(affected))
    # Imports cannot model subprocess execution or dynamic imports. An unmapped
    # changed script therefore falls back to all tests, never to no tests.
    for module in changed:
        reachable = {module}
        while True:
            consumers = {name for name, deps in graph.items() if deps.intersection(reachable)}
            if consumers.issubset(reachable):
                break
            reachable.update(consumers)
        if not matched.intersection(reachable):
            return None
    return sorted(selected)


def check(files=None):
    subprocess.run(["python3", "-m", "compileall", "-q", "scripts", "-x", "/\\.venv/"], check=True)
    tests = select_tests(files)
    print("Script tests: " + ("full suite" if tests is None else ", ".join(tests) or "none"), flush=True)
    if tests is None or tests:
        subprocess.run(["uv", "run", "--project", "scripts", "--locked", "pytest", *(tests or [])], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ci", action="store_true", help="Select tests using CHANGED_FILES from component detection")
    args = parser.parse_args()
    files = json.loads(os.environ["CHANGED_FILES"]) if args.ci else None
    if files is not None and (not isinstance(files, list) or not all(isinstance(path, str) for path in files)):
        parser.error("CHANGED_FILES must be null or a list of paths")
    check(files)


if __name__ == "__main__":
    main()
