#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Check or reconcile repository labels, secrets, variables and policies."""

import argparse
import copy
import json
import os
from pathlib import Path
import re
import subprocess
import tomllib
from urllib.parse import quote

from ci_automation import api, run

DEFAULT_CONFIG = Path(__file__).resolve().parent.parent / ".github/repository.toml"


def includes(actual, expected):
    """Compare managed fields; GitHub adds IDs/defaults and may reorder rules."""
    if isinstance(expected, dict):
        return isinstance(actual, dict) and all(key in actual and includes(actual[key], value)
                                                 for key, value in expected.items())
    if isinstance(expected, list):
        return isinstance(actual, list) and len(actual) == len(expected) and all(
            any(includes(candidate, item) for candidate in actual) for item in expected)
    return actual == expected


def load_config(path):
    with Path(path).open("rb") as stream:
        config = tomllib.load(stream)
    for name in [*config.get("variables", {}), *config.get("secrets", {})]:
        if not re.fullmatch(r"[A-Z_][A-Z0-9_]*", name):
            raise ValueError("Invalid variable/secret name in configuration")
    for label in config.get("labels", {}).values():
        if not re.fullmatch(r"[a-fA-F0-9]{6}", label["color"]):
            raise ValueError("Invalid label color")
    names = [rule["name"] for rule in config.get("rulesets", [])]
    if len(names) != len(set(names)):
        raise ValueError("Duplicate managed ruleset names")
    for name, token in config.get("tokens", {}).items():
        if name not in config.get("secrets", {}):
            raise ValueError(f"Token {name} must refer to a declared secret")
        if not isinstance(token.get("purpose"), str) or not token["purpose"].strip():
            raise ValueError(f"Token {name} must describe its purpose")
        if not token.get("permissions") or any(level not in {"read", "write"} for level in token["permissions"].values()):
            raise ValueError(f"Token {name} permissions must use read or write")
    return config


def token_instructions(repo, config, names):
    tokens = [(name, config.get("tokens", {})[name]) for name in names if name in config.get("tokens", {})]
    if not tokens:
        return ""
    owner, project = repo.split("/")
    lines = ["Fine-grained PAT setup:", "  Create: https://github.com/settings/personal-access-tokens/new",
             f"  Resource owner: {owner}", f"  Repository access: Only select repositories -> {project}",
             "  Create a separate token for each responsibility."]
    for name, token in tokens:
        lines.extend(["", f"  {name}: {token['purpose']}", "  Repository permissions:"])
        for permission, level in token["permissions"].items():
            lines.append(f"    {permission}: {'Read and write' if level == 'write' else 'Read-only'}")
        lines.append("    Metadata: Read-only (automatic)")
        if token.get("account_role"):
            lines.append(f"  Token owner's role: {token['account_role']}")
    return "\n".join(lines)


def describe(error):
    """A failure reason that never echoes command output, which may hold secrets."""
    if isinstance(error, subprocess.CalledProcessError):
        return "GitHub API request failed; check authentication and token permissions"
    return str(error)


class Repository:
    def __init__(self, repo, client=api):
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
            raise ValueError("Invalid repository")
        self.repo, self.client = repo, client
        self.base = f"repos/{repo}"

    def pages(self, path, key=None):
        pages = self.client(f"{self.base}/{path}", paginate=True)
        return [item for page in pages for item in (page[key] if key else page)]

    def snapshot(self):
        """Read every managed section; a section that cannot be read is recorded
        under ``errors`` and set to ``None`` so the others are still checked.
        Inherited organization rulesets are never adopted or overwritten."""
        readers = {
            "settings": lambda: self.client(self.base),
            "workflow_permissions": lambda: self.client(f"{self.base}/actions/permissions/workflow"),
            "labels": lambda: {label["name"]: label for label in self.pages("labels?per_page=100")},
            "variables": lambda: {var["name"]: var for var in self.pages("actions/variables?per_page=100", "variables")},
            "secrets": lambda: {secret["name"] for secret in self.pages("actions/secrets?per_page=100", "secrets")},
            "rulesets": lambda: [self.client(f"{self.base}/rulesets/{rule['id']}")
                                 for rule in self.pages("rulesets?includes_parents=false&per_page=100")],
        }
        state, errors = {}, {}
        for section, read in readers.items():
            try:
                state[section] = read()
            except (subprocess.CalledProcessError, ValueError, KeyError, TypeError) as error:
                state[section], errors[section] = None, describe(error)
        state["errors"] = errors
        return state

    def check(self, config, state):
        errors = state.get("errors", {})
        problems = [f"{section} cannot be read: {reason}" for section, reason in errors.items()]
        for section in ("settings", "workflow_permissions"):
            if section in errors:
                continue
            for key, value in config.get(section, {}).items():
                if not includes(state[section].get(key), value):
                    problems.append(f"{section}.{key} differs")
        if "labels" not in errors:
            for name, label in config.get("labels", {}).items():
                if not includes(state["labels"].get(name), label):
                    problems.append(f"label {name} missing or differs")
        if "variables" not in errors:
            for name, value in config.get("variables", {}).items():
                if state["variables"].get(name, {}).get("value") != value:
                    problems.append(f"variable {name} missing or differs")
        if "secrets" not in errors:
            for name, required in config.get("secrets", {}).items():
                if required and name not in state["secrets"]:
                    problems.append(f"secret {name} missing (value cannot be inspected)")
        if "rulesets" not in errors:
            for expected in config.get("rulesets", []):
                matches = [r for r in state["rulesets"] if r["name"] == expected["name"]]
                if len(matches) != 1 or not includes(matches[0], expected):
                    problems.append(f"ruleset {expected['name']} missing, duplicated or differs")
        return problems

    def init(self, config, state, env=None, secret_runner=subprocess.run):
        """Apply every managed item independently and return what failed.

        A failure never stops the remaining items, so one run reports everything
        left to fix. Unmanaged labels, variables, secrets and rulesets are never
        deleted, and a section whose current state is unknown is not written.
        """
        env = os.environ if env is None else env
        errors = state.get("errors", {})
        failures = [f"{section} not applied: its current state cannot be read" for section in errors
                    if config.get(section)]

        def attempt(item, action):
            try:
                action()
            except (subprocess.CalledProcessError, ValueError) as error:
                failures.append(f"{item}: {describe(error)}")

        for section, endpoint in (("settings", self.base),
                                  ("workflow_permissions", f"{self.base}/actions/permissions/workflow")):
            if section not in errors and config.get(section) and not includes(state[section], config[section]):
                attempt(section, lambda section=section, endpoint=endpoint: self.client(
                    endpoint, "PUT" if section == "workflow_permissions" else "PATCH", config[section]))
        if "labels" not in errors:
            for name, expected in config.get("labels", {}).items():
                if not includes(state["labels"].get(name), expected):
                    exists = name in state["labels"]
                    endpoint = f"{self.base}/labels" + (f"/{quote(name, safe='')}" if exists else "")
                    attempt(f"label {name}", lambda endpoint=endpoint, exists=exists, name=name, expected=expected:
                            self.client(endpoint, "PATCH" if exists else "POST", {"name": name, **expected}))
        if "variables" not in errors:
            for name, value in config.get("variables", {}).items():
                if state["variables"].get(name, {}).get("value") != value:
                    exists = name in state["variables"]
                    endpoint = f"{self.base}/actions/variables" + (f"/{name}" if exists else "")
                    attempt(f"variable {name}", lambda endpoint=endpoint, exists=exists, name=name, value=value:
                            self.client(endpoint, "PATCH" if exists else "POST", {"name": name, "value": value}))
        for name, required in config.get("secrets", {}).items():
            if env.get(name):
                attempt(f"secret {name}", lambda name=name: self.upload_secret(name, env[name], secret_runner))
            elif required and "secrets" not in errors and name not in state["secrets"]:
                failures.append(f"secret {name} missing: set it with `gh secret set {name} --repo {self.repo}`")
        if "rulesets" not in errors:
            for expected in config.get("rulesets", []):
                matches = [r for r in state["rulesets"] if r["name"] == expected["name"]]
                if len(matches) > 1:
                    failures.append(f"ruleset {expected['name']}: ambiguous, several rulesets share the name; "
                                    "refusing to modify policies")
                elif not matches or not includes(matches[0], expected):
                    endpoint = f"{self.base}/rulesets" + (f"/{matches[0]['id']}" if matches else "")
                    attempt(f"ruleset {expected['name']}", lambda endpoint=endpoint, matches=matches, expected=expected:
                            self.client(endpoint, "PUT" if matches else "POST", copy.deepcopy(expected)))
        return failures

    def upload_secret(self, name, value, secret_runner):
        # gh handles public-key encryption. Secret values go through stdin,
        # never argv, stdout, diagnostics or committed files.
        try:
            secret_runner(["gh", "secret", "set", name, "--repo", self.repo], input=value.encode(), check=True,
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError:
            raise ValueError("upload failed; value withheld") from None


def protection_only(config):
    """Scope a configuration to branch protection, which needs no secrets."""
    return {"rulesets": config.get("rulesets", [])}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["check", "init", "protect"])
    parser.add_argument("--repo", help="owner/repository; defaults to gh's current repository")
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG)
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        repo = args.repo or json.loads(run("gh", "repo", "view", "--json", "nameWithOwner"))["nameWithOwner"]
        remote = Repository(repo)
    except ValueError as error:
        parser.exit(1, f"error: {error}\n")
    except subprocess.CalledProcessError:
        parser.exit(1, "error: GitHub CLI command failed; check authentication and repository permissions.\n")
    if args.command == "protect":
        # Branch protection is independent of secrets, so it can be applied
        # on a fresh repository before any token exists.
        config = protection_only(config)
    state, failures = remote.snapshot(), []
    if args.command in ("init", "protect"):
        if args.command == "init":
            # Protection comes first: nothing else may leave main unprotected.
            failures += remote.init(protection_only(config), state)
            state = remote.snapshot()
        failures += remote.init(config, state)
        state = remote.snapshot()
    failures = list(dict.fromkeys(failures))
    problems = remote.check(config, state)
    for failure in failures:
        print(f"ERROR: {failure}")
    for problem in problems:
        print(f"FAIL: {problem}")
    missing = [] if "secrets" in state.get("errors", {}) else [
        name for name, required in config.get("secrets", {}).items() if required and name not in state["secrets"]]
    if missing:
        print("Prepare the secret values described in docs/development/github.md, then set each one:")
        for name in missing:
            print(f"  gh secret set {name} --repo {repo}")
    instructions = token_instructions(repo, config, missing)
    if instructions:
        print(instructions)
    print("Secret values and token scopes cannot be verified through the repository secret API.")
    if not failures and not problems:
        print("GitHub repository configuration matches.")
    raise SystemExit(bool(failures or problems))


if __name__ == "__main__":
    main()
