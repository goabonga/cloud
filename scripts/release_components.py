#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Release changed multicz components atomically after successful main CI."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tomllib

from ci_automation import api, authenticated_git, git, output, repository, run

RELEASE_PREFIX = "chore(release):"


def tag_exists(tag):
    return subprocess.run(["git", "show-ref", "--verify", "--quiet", f"refs/tags/{tag}"]).returncode == 0


def github_release(tag):
    # List rather than interpreting every failed GET as a missing release.
    # Network and permission errors must fail instead of creating duplicates.
    pages = api(f"repos/{repository()}/releases?per_page=100", paginate=True)
    return next((release for page in pages for release in page if release["tag_name"] == tag), None)


def publish_release(tag, sha, existing=None):
    notes = run("multicz", "release-notes", "--tag", tag)
    payload = {"tag_name": tag, "target_commitish": sha, "name": tag,
               "body": notes, "draft": False, "prerelease": False}
    if existing is None:
        return api(f"repos/{repository()}/releases", "POST", payload)
    if existing["draft"]:
        return api(f"repos/{repository()}/releases/{existing['id']}", "PATCH", payload)
    return existing


def versions():
    config = tomllib.loads(Path("multicz.toml").read_text())
    result = {}
    for name, component in config["components"].items():
        entry = component["bump_files"][0]
        path, key = Path(entry["file"]), entry["key"]
        if key.startswith("regex:"):
            match = re.search(key.removeprefix("regex:"), path.read_text())
            if not match:
                raise ValueError(f"Missing version for {name}")
            version = match.group(1)
        else:
            value = json.loads(path.read_text()) if path.suffix == ".json" else tomllib.loads(path.read_text())
            for segment in key.split("."):
                value = value[segment]
            version = value
        if not isinstance(version, str) or not re.fullmatch(r"\d+\.\d+\.\d+", version):
            raise ValueError(f"Expected stable version for {name}")
        result[name] = version
    return config, result


def validate_builds(names, config):
    packages = config.get("plugins", {}).get("go-deps", {}).get("packages", {})
    for name in names:
        if name in packages:
            entry = packages[name]
            entry = [entry] if isinstance(entry, str) else entry
            subprocess.run(["go", "build", "-o", "/dev/null", *entry], check=True)
    if "cloud-www" in names:
        subprocess.run(["npm", "--prefix", "www", "ci"], check=True)
        subprocess.run(["npm", "--prefix", "www", "run", "build"], check=True)
    if "cloud-scripts" in names:
        subprocess.run(["uv", "lock", "--project", "scripts", "--check"], check=True)
    if "cloud-docs" in names:
        subprocess.run(["make", "docs"], check=True)


def bump():
    """Push signed versions/tags and return an immutable publication plan."""
    result = {"tags": [], "sha": "", "publish": "false", "doc_sha": "", "doc_tag": ""}
    if os.environ["GITHUB_REF"] != "refs/heads/main":
        raise ValueError("Component releases are restricted to main")
    expected = os.environ["GITHUB_SHA"]
    if not re.fullmatch(r"[a-f0-9]{40}", expected):
        raise ValueError("Invalid validated commit")
    if git("status", "--porcelain"):
        raise ValueError("Release checkout must be clean")
    git("fetch", "origin", "main", "--tags")
    current = git("rev-parse", "origin/main")
    recovering = current != expected and git("show", "-s", "--format=%P", current) == expected and git("show", "-s", "--format=%s", current).startswith(RELEASE_PREFIX)
    if current != expected and not recovering:
        print("Main advanced; the latest validated workflow will release it.")
        return result
    git("checkout", "--detach", current)
    plan = json.loads(run("multicz", "plan", "--output", "json"))["bumps"]
    if plan:
        command = ["multicz", "bump", "--commit", "--tag", "--sign", "--commit-message",
                   "chore(release): bump changed components", "--output", "json"]
        for name in plan:
            command += ["--component", name]
        run(*command)
    config, component_versions = versions()
    sha = git("rev-parse", "HEAD")
    tags = []
    for name, version in component_versions.items():
        tag = f"{name}-v{version}"
        if name in plan:
            if not tag_exists(tag) or git("rev-parse", tag + "^{commit}") != sha:
                raise ValueError(f"Missing release tag for {name}")
            tags.append(tag)
        elif not tag_exists(tag):
            # Seed a newly introduced component, including a root-only bootstrap.
            if version != "0.0.0" or recovering:
                raise ValueError(f"Missing atomic tag: {tag}")
            git("tag", "-s", "-m", tag, tag, sha)
            tags.append(tag)
    if tags:
        names = [tag.rsplit("-v", 1)[0] for tag in tags]
        validate_builds(names, config)
        if git("status", "--porcelain"):
            raise ValueError("Release build modified tracked files")
        git("verify-commit", sha)
        for tag in tags:
            git("verify-tag", tag)
        authenticated_git("push", "--atomic", "origin", "HEAD:refs/heads/main", *["refs/tags/" + tag for tag in tags])
    result["sha"] = sha
    for name, version in component_versions.items():
        tag = f"{name}-v{version}"
        tag_sha = git("rev-parse", tag + "^{commit}")
        git("merge-base", "--is-ancestor", tag_sha, sha)
        existing = github_release(tag)
        manual_docs = name == "cloud-docs" and os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch"
        if existing and not existing["draft"] and tag not in tags and not manual_docs and not (recovering and tag_sha == sha):
            continue
        git("verify-commit", tag_sha)
        git("verify-tag", tag)
        result["tags"].append(tag)
        if name == "cloud-docs":
            result.update(publish="true", doc_sha=tag_sha, doc_tag=tag)
    return result


def publish_component(tag):
    """Publish one checked-out component tag, without signing or moving Git refs."""
    if os.environ["GITHUB_REF"] != "refs/heads/main":
        raise ValueError("Component publications are restricted to main")
    _, component_versions = versions()
    name, separator, version = tag.rpartition("-v")
    if not separator or component_versions.get(name) != version:
        raise ValueError("Unknown or inconsistent component tag")
    sha = git("rev-parse", "HEAD")
    if git("rev-parse", tag + "^{commit}") != sha:
        raise ValueError("Publication checkout must match the component tag")
    if not git("ls-remote", "origin", "refs/tags/" + tag):
        raise ValueError("Component tag must be pushed before publication")
    return publish_release(tag, sha, github_release(tag))


def release():
    """Run both phases locally; CI runs them in separate jobs."""
    output("publish", "false")
    result = bump()
    for tag in result["tags"]:
        # Recovery can publish an older component tag after main has advanced.
        git("checkout", "--detach", tag)
        published = publish_component(tag)
        if tag == result["doc_tag"]:
            output("publish", "true")
            output("sha", result["doc_sha"])
            output("tag", tag)
            output("url", published["html_url"])
    if result["sha"]:
        git("checkout", "--detach", result["sha"])
    output("released", json.dumps(result["tags"], separators=(",", ":")))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["bump", "publish"])
    parser.add_argument("--tag")
    args = parser.parse_args()
    if args.command == "bump":
        if args.tag:
            parser.error("--tag applies only to publication")
        result = bump()
        for name, value in result.items():
            output(name, json.dumps(value, separators=(",", ":")) if isinstance(value, list) else value)
    elif not args.tag:
        parser.error("publication requires --tag")
    else:
        published = publish_component(args.tag)
        output("url", published["html_url"])


if __name__ == "__main__":
    main()
