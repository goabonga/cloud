# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Exercise atomic component releases and retries against a signed Git remote."""

import os
from pathlib import Path

import pytest

import release_components as components
import release_components as docs
from test_release_docs import repo, signing_key, outputs


@pytest.fixture
def component_repo(repo, monkeypatch):
    Path("tools").mkdir()
    Path("tools/version.toml").write_text('version = "0.0.0"\n')
    config = Path("multicz.toml").read_text().replace('[components.cloud-docs]\n', '[components.cloud-docs]\ndepends_on = ["cloud-tool"]\n')
    config += '\n[components.cloud-tool]\npaths = ["tools/**"]\nbump_files = [{file = "tools/version.toml", key = "version"}]\nchangelog = "tools/CHANGELOG.md"\n'
    Path("multicz.toml").write_text(config)
    docs.git("add", "tools/version.toml", "multicz.toml")
    docs.git("commit", "-m", "chore: initialize tool")
    docs.git("push", "origin", "main")
    monkeypatch.setenv("GITHUB_SHA", docs.git("rev-parse", "HEAD"))
    pushes = []

    def push(*args):
        pushes.append(args)
        docs.git(*args)

    monkeypatch.setattr(components, "authenticated_git", push)
    return repo, pushes


def tool_change():
    Path("tools/version.toml").write_text('version = "0.0.0"\nfeature = true\n')
    docs.git("add", "tools/version.toml")
    docs.git("commit", "-m", "fix: update tool")
    docs.git("push", "origin", "HEAD:refs/heads/main")
    os.environ["GITHUB_SHA"] = docs.git("rev-parse", "HEAD")


def test_seed_new_component_creates_signed_zero_tag_without_version_bump(component_repo):
    before = os.environ["GITHUB_SHA"]
    components.release()
    assert docs.git("rev-parse", "HEAD") == before
    docs.git("verify-tag", "cloud-tool-v0.0.0")
    assert outputs()["publish"] == "false"
    assert component_repo[1] == [("push", "--atomic", "origin", "HEAD:refs/heads/main", "refs/tags/cloud-tool-v0.0.0")]


def test_component_and_dependent_docs_bump_and_publish_in_one_atomic_push(component_repo):
    components.release()
    component_repo[1].clear()
    tool_change()
    components.release()
    assert outputs()["tag"] == "cloud-docs-v0.0.1"
    sha = outputs()["sha"]
    docs.git("verify-commit", sha)
    for tag in ("cloud-tool-v0.0.1", "cloud-docs-v0.0.1"):
        docs.git("verify-tag", tag)
        assert docs.git("rev-parse", tag + "^{commit}") == sha
    push = component_repo[1][0]
    assert push[:4] == ("push", "--atomic", "origin", "HEAD:refs/heads/main")
    assert set(push[4:]) == {"refs/tags/cloud-tool-v0.0.1", "refs/tags/cloud-docs-v0.0.1"}
    assert len(component_repo[1]) == 1


def test_no_changes_do_not_push_or_publish(component_repo):
    components.release()
    component_repo[1].clear()
    count = len(component_repo[0][2])
    components.release()
    assert not component_repo[1]
    assert len(component_repo[0][2]) == count
    assert outputs()["publish"] == "false"


def test_retry_after_api_failure_reuses_all_signed_tags(component_repo, monkeypatch):
    components.release()
    tool_change()
    actual_publish = components.publish_release
    monkeypatch.setattr(components, "publish_release", lambda *args: (_ for _ in ()).throw(ConnectionError("API unavailable")))
    with pytest.raises(ConnectionError):
        components.release()
    sha = docs.git("rev-parse", "HEAD")
    push_count = len(component_repo[1])
    monkeypatch.setattr(components, "publish_release", actual_publish)
    components.release()
    assert docs.git("rev-parse", "HEAD") == sha
    assert len(component_repo[1]) == push_count
    assert {release["tag_name"] for release in component_repo[0][2]} == {"cloud-docs-v0.0.0", "cloud-tool-v0.0.0", "cloud-docs-v0.0.1", "cloud-tool-v0.0.1"}


def test_failed_build_does_not_push_any_release_state(component_repo, monkeypatch):
    components.release()
    component_repo[1].clear()
    tool_change()
    before = os.environ["GITHUB_SHA"]
    monkeypatch.setattr(components, "validate_builds", lambda *args: (_ for _ in ()).throw(RuntimeError("Build failed")))
    with pytest.raises(RuntimeError):
        components.release()
    assert docs.git("ls-remote", "origin", "refs/heads/main").split()[0] == before
    assert not docs.git("ls-remote", "origin", "refs/tags/cloud-tool-v0.0.1")
    assert not component_repo[1]


def test_stale_validation_does_not_release_new_main(component_repo):
    before = os.environ["GITHUB_SHA"]
    tool_change()
    os.environ["GITHUB_SHA"] = before
    components.release()
    assert not component_repo[1]
    assert outputs()["publish"] == "false"


def test_bump_pushes_tags_without_creating_github_releases(component_repo):
    components.release()
    tool_change()
    count = len(component_repo[0][2])
    result = components.bump()
    assert set(result["tags"]) == {"cloud-docs-v0.0.1", "cloud-tool-v0.0.1"}
    assert result["publish"] == "true"
    assert result["doc_sha"] == result["sha"]
    assert len(component_repo[0][2]) == count
    assert docs.git("ls-remote", "origin", "refs/heads/main").split()[0] == result["sha"]


def test_publication_can_retry_without_bumping_or_pushing(component_repo):
    components.release()
    tool_change()
    result = components.bump()
    count = len(component_repo[1])
    tag = "cloud-tool-v0.0.1"
    docs.git("checkout", "--detach", tag)
    first = components.publish_component(tag)
    second = components.publish_component(tag)
    assert first["id"] == second["id"]
    assert len(component_repo[1]) == count
    assert len([release for release in component_repo[0][2] if release["tag_name"] == tag]) == 1


def test_publication_rejects_wrong_checkout_and_unknown_component(component_repo):
    components.release()
    tool_change()
    result = components.bump()
    docs.git("checkout", "--detach", "cloud-tool-v0.0.0")
    with pytest.raises(ValueError, match="inconsistent component tag"):
        components.publish_component("cloud-tool-v0.0.1")
    docs.git("checkout", "--detach", result["sha"])
    with pytest.raises(ValueError, match="inconsistent component tag"):
        components.publish_component("unknown-v0.0.1")


def test_workflow_separates_bump_publication_and_docs_with_summaries():
    text = (Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml").read_text()
    bump = text.split("\n  release-bump:\n")[1].split("\n  release:\n")[0]
    publication = text.split("\n  release:\n")[1].split("\n  pages:\n")[0]
    pages = text.split("\n  pages:\n")[1]
    assert "name: release bump" in bump
    assert "scripts/release_components.py bump" in bump
    assert "name: release components" in publication
    assert "scripts/release_components.py publish --tag" in publication
    assert "matrix:" in publication and "fail-fast: false" in publication
    assert "GPG_PRIVATE_KEY" not in publication
    assert "name: publish documentation" in pages
    assert "needs.release.result == 'success'" in pages
    for job in (bump, publication, pages):
        assert "name: Job summary" in job and "if: always()" in job
