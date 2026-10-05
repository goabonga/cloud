#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Re-author and sign a Dependabot commit, one commit per updated dependency.

It runs as ``git rebase --exec`` on each Dependabot commit, using only trusted
code and Git plumbing. Every dependency Dependabot announces becomes its own
signed commit, ``<prefix>: bump `<name>` from <old> to <new>``, without a body.
When a changed hunk cannot be tied to exactly one dependency, the update stays a
single commit, still without a body. The final tree is always Dependabot's.
"""

import difflib
import re
import subprocess

WORKFLOW_DIRS = (".github/workflows/", ".github/actions/")
GO_FILES = {"go.mod", "go.sum"}
PYTHON_FILES = {"scripts/pyproject.toml", "scripts/uv.lock"}
SUBJECT_LIMIT = 72


def git(*args, data=None):
    return subprocess.check_output(["git", "-c", "core.hooksPath=/dev/null", *args], input=data)


def ecosystem(paths):
    if all(path.startswith(WORKFLOW_DIRS) and path.endswith((".yml", ".yaml")) for path in paths):
        return "actions"
    if all(path in GO_FILES for path in paths):
        return "go"
    if all(path in PYTHON_FILES for path in paths):
        return "python"
    raise SystemExit("Refusing to rewrite files outside the configured dependency ecosystems")


def updates(message):
    """``[(name, old, new)]`` in the order Dependabot announces them."""
    found = re.findall(r"^Updates `([^`\s]+)` from (\S+) to (\S+?)\.?$", message, re.M)
    return found or re.findall(r"^Bumps \[([^\]\s]+)\]\([^)]*\) from (\S+) to (\S+?)\.?$", message, re.M)


def metadata(message):
    """``{name: {key: value}}`` from Dependabot's ``updated-dependencies`` block."""
    result, current = {}, None
    for line in message.splitlines():
        start = re.match(r"^- dependency-name:\s*(\S+)\s*$", line)
        field = re.match(r"^\s+([a-z-]+):\s*(\S+)\s*$", line)
        if start:
            current = result.setdefault(start.group(1), {})
        elif field and current is not None:
            current[field.group(1)] = field.group(2)
        else:
            current = None
    return result


def prefix(kind, types):
    if kind == "actions":
        return "ci"
    if kind == "python" and types and all(value.endswith(":development") for value in types):
        # The Python tooling is not shipped: development updates do not release.
        return "chore(deps)"
    # Go modules are linked into the binaries and runtime Python dependencies
    # run the tooling, so both release; missing metadata errs on releasing.
    return "fix(deps)"


def subject(text):
    text = re.sub(r"\s+", " ", text).strip()
    if not text:
        raise SystemExit("Refusing an empty commit subject")
    return text[:SUBJECT_LIMIT].rstrip(" .")


def bump_subject(kind_prefix, name, old, new):
    text = f"{kind_prefix}: bump `{name}` from {old} to {new}"
    return subject(text if len(text) <= SUBJECT_LIMIT else f"{kind_prefix}: bump `{name}` to {new}")


def name_pattern(name):
    # Python treats -, _ and . alike in names; uv.lock lowercases them.
    parts = (re.escape(part) for part in re.split(r"[-_.]", name))
    return re.compile(r"(?<![\w.-])" + "[-_.]".join(parts) + r"(?![\w.])", re.I)


def owners(text, patterns):
    return {name for name, pattern in patterns.items() if pattern.search(text)}


def line_owner(lines, index, patterns):
    """The dependency a line belongs to: named in it or, in ``uv.lock``, owning
    the ``[[package]]`` block it sits in. ``None`` when not exactly one."""
    found = owners(lines[index], patterns)
    if not found:
        block = next(
            (match for line in reversed(lines[: index + 1]) if (match := re.match(r'^name = "([^"]+)"', line))), None
        )
        found = owners(block.group(1), patterns) if block else set()
    return found.pop() if len(found) == 1 else None


def split_plan(contents, names):
    """Per file, the diff segments and the dependency each one belongs to.

    Inside a changed hunk every line is attributed on its own, so adjacent
    lines of different dependencies (a go.mod require block) still split.
    ``None`` when a line matches no dependency or several of them.
    """
    patterns = {name: name_pattern(name) for name in names}
    plan = {}
    for path, (old, new) in contents.items():
        chunks = []
        for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, old, new, autojunk=False).get_opcodes():
            if tag == "equal":
                chunks.append((None, old[i1:i2], old[i1:i2]))
                continue
            removed = [(line_owner(old, i, patterns), old[i]) for i in range(i1, i2)]
            added = [(line_owner(new, j, patterns), new[j]) for j in range(j1, j2)]
            if any(owner is None for owner, _ in removed + added):
                return None
            order = list(dict.fromkeys(owner for owner, _ in removed + added))
            for owner in order:
                chunks.append(
                    (
                        owner,
                        [line for who, line in removed if who == owner],
                        [line for who, line in added if who == owner],
                    )
                )
        plan[path] = chunks
    return plan


def rebuild(chunks, applied):
    return "".join(line for owner, old, new in chunks for line in (new if owner in applied else old))


def commit(message, key, amend=False):
    git(
        "commit",
        "--quiet",
        f"--gpg-sign={key}",
        "--file=-",
        *(["--amend", "--reset-author"] if amend else []),
        data=f"{message}\n".encode(),
    )
    git("verify-commit", "HEAD")


def main():
    paths = git("diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "-z", "HEAD")
    paths = [path.decode() for path in paths.split(b"\0") if path]
    if not paths:
        raise SystemExit("Refusing to rewrite an empty dependency commit")
    kind = ecosystem(paths)
    message = git("log", "-1", "--format=%B").decode()
    details, announced = metadata(message), updates(message)
    key = git("config", "--get", "user.signingkey").decode().strip()
    prefixes = {
        name: prefix(kind, [details.get(name, {}).get("dependency-type", "")])
        if details.get(name)
        else prefix(kind, [])
        for name, _, _ in announced
    }

    if len(announced) > 1:
        original = git("rev-parse", "HEAD").decode().strip()
        contents = {
            path: (
                git("show", f"HEAD^:{path}").decode().splitlines(keepends=True),
                git("show", f"HEAD:{path}").decode().splitlines(keepends=True),
            )
            for path in paths
        }
        plan = split_plan(contents, [name for name, _, _ in announced])
        if plan is not None:
            git("reset", "--quiet", "--hard", "HEAD^")
            applied = set()
            for name, old, new in announced:
                applied.add(name)
                for path, chunks in plan.items():
                    with open(path, "w") as stream:
                        stream.write(rebuild(chunks, applied))
                git("add", "--", *paths)
                if git("diff", "--cached", "--name-only"):
                    commit(bump_subject(prefixes[name], name, old, new), key)
            if not git("diff", "--name-only", original, "HEAD"):
                return
            # Never leave a tree that differs from Dependabot's: fall back.
            git("reset", "--quiet", "--hard", original)

    if len(announced) == 1:
        name, old, new = announced[0]
        text = bump_subject(prefixes[name], name, old, new)
    else:
        types = [value.get("dependency-type", "") for value in details.values()]
        stripped = re.sub(r"^[a-z-]+(?:\([^)]*\))?!?:\s*", "", message.splitlines()[0] if message else "")
        text = subject(f"{prefix(kind, types)}: {re.sub(r'^Bump\b', 'bump', stripped)}")
    commit(text, key, amend=True)


if __name__ == "__main__":
    main()
