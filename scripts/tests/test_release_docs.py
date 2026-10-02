# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Exercise signed multicz releases against a disposable atomic Git remote."""

import os
from pathlib import Path
import subprocess
from unittest.mock import Mock

import pytest

import release_components as release


@pytest.fixture(scope="module")
def signing_key(tmp_path_factory):
    home = tmp_path_factory.mktemp("release-key")
    home.chmod(0o700)
    env = {**os.environ, "GNUPGHOME": str(home)}
    subprocess.run(["gpg", "--batch", "--passphrase", "", "--quick-generate-key",
                    "Release Test <release@example.test>", "ed25519", "sign", "0"],
                   env=env, check=True, capture_output=True)
    yield home
    subprocess.run(["gpgconf", "--kill", "gpg-agent"], env=env, check=True)


@pytest.fixture
def repo(tmp_path, signing_key, monkeypatch):
    root = Path(__file__).resolve().parents[2]
    bare = tmp_path / "remote.git"
    work = tmp_path / "repo"
    subprocess.run(["git", "init", "--bare", str(bare)], check=True, capture_output=True)
    subprocess.run(["git", "init", "--initial-branch=main", str(work)], check=True, capture_output=True)
    monkeypatch.chdir(work)
    monkeypatch.setenv("GNUPGHOME", str(signing_key))
    monkeypatch.setenv("GH_REPO", "owner/cloud")
    monkeypatch.setenv("GITHUB_REF", "refs/heads/main")
    monkeypatch.setenv("GITHUB_EVENT_NAME", "push")
    monkeypatch.setenv("GITHUB_OUTPUT", str(tmp_path / "outputs"))
    for key, value in [("user.name", "Chris"), ("user.email", "goabonga@pm.me"),
                       ("user.signingkey", "release@example.test"), ("commit.gpgsign", "true")]:
        release.git("config", key, value)
    release.git("remote", "add", "origin", str(bare))
    config = (root / "multicz.toml").read_text()
    start = config.index("[plugins.go-deps.packages]")
    end = config.index("[components.cloud-docs]")
    config = config[:start] + "[plugins.go-deps]\npackages = {}\n\n" + config[end:]
    config = config[:config.index("[components.cloud-scripts]")]
    config = "\n".join(line for line in config.splitlines() if not line.startswith("depends_on"))
    Path("multicz.toml").write_text(config)
    Path("zensical.toml").write_text('[project.extra.versions]\ncloud_docs = "0.0.0"\n')
    Path("scripts").mkdir()
    Path("scripts/pyproject.toml").write_text((root / "scripts/pyproject.toml").read_text())
    Path("docs").mkdir()
    Path("docs/index.md").write_text("Cloud\n")
    # The real site build is checked by make docs in documentation CI. Here a build
    # marker proves the release build runs before any remote state changes.
    Path("Makefile").write_text(".PHONY: docs\ndocs:\n\tpython3 -c \"from pathlib import Path; Path('.git/built').touch()\"\n")
    release.git("add", "multicz.toml", "zensical.toml", "docs/index.md", "Makefile", "scripts/pyproject.toml")
    release.git("commit", "-m", "chore: initialize documentation")
    release.git("tag", "-s", "-m", "Initial version", "cloud-docs-v0.0.0")
    release.git("push", "origin", "main", "--tags")
    monkeypatch.setenv("GITHUB_SHA", release.git("rev-parse", "HEAD"))
    pushes = []

    def push(*args):
        assert Path(".git/built").exists()
        pushes.append(args)
        release.git(*args)

    monkeypatch.setattr(release, "authenticated_git", push)
    initial = {"id": 1, "tag_name": "cloud-docs-v0.0.0", "draft": False,
               "html_url": "https://example.test/releases/0.0.0"}
    releases = [initial]

    def api(endpoint, method="GET", payload=None, paginate=False):
        assert endpoint.startswith("repos/owner/cloud/releases")
        if method == "GET":
            assert paginate
            return [releases]
        if method == "POST":
            assert release.git("rev-parse", payload["tag_name"] + "^{commit}") == payload["target_commitish"]
            assert release.git("ls-remote", "origin", "refs/tags/" + payload["tag_name"])
            value = {"id": len(releases) + 1, **payload,
                     "html_url": f"https://example.test/releases/{payload['tag_name']}"}
            releases.append(value)
            return value
        if method == "PATCH":
            value = next(r for r in releases if r["id"] == int(endpoint.rsplit("/", 1)[1]))
            value.update(payload)
            return value
        raise AssertionError(method)

    monkeypatch.setattr(release, "api", api)
    return work, bare, releases, pushes, api


def change(path="docs/index.md", subject="docs: describe the control plane"):
    Path(path).write_text("Updated cloud content\n")
    release.git("add", path)
    release.git("commit", "-m", subject)
    release.git("push", "origin", "HEAD:refs/heads/main")
    os.environ["GITHUB_SHA"] = release.git("rev-parse", "HEAD")


def outputs():
    return dict(line.split("=", 1) for line in Path(os.environ["GITHUB_OUTPUT"]).read_text().splitlines())


def test_signed_bump_commit_tag_release_and_empty_followup_plan(repo):
    change(subject="ci: document release automation")
    release.release()
    result = outputs()
    assert result["publish"] == "true"
    assert result["tag"] == "cloud-docs-v0.0.1"
    assert release.git("log", "-1", "--format=%s") == "chore(release): bump changed components"
    assert release.git("log", "-1", "--format=%an <%ae>") == "Chris <goabonga@pm.me>"
    release.git("verify-commit", result["sha"])
    release.git("verify-tag", result["tag"])
    assert "0.0.1" in Path("docs/CHANGELOG.md").read_text()
    assert "\"bumps\": {}" in release.run("multicz", "plan", "--output", "json")
    assert repo[3] == [("push", "--atomic", "origin", "HEAD:refs/heads/main", "refs/tags/cloud-docs-v0.0.1")]
    assert len(repo[2]) == 2


def test_unchanged_docs_and_unrelated_commits_do_not_publish(repo):
    change("unrelated.txt", "fix: repair an unrelated component")
    release.release()
    assert outputs() == {"publish": "false", "released": "[]"}
    assert not repo[3]
    assert len(repo[2]) == 1


def test_first_release_starts_at_zero_without_a_seed_tag(repo):
    release.git("tag", "-d", "cloud-docs-v0.0.0")
    release.git("push", "origin", ":refs/tags/cloud-docs-v0.0.0")
    repo[2].clear()
    change()
    release.release()
    assert outputs()["tag"] == "cloud-docs-v0.0.1"
    assert len(repo[2]) == 1
    assert len(repo[3]) == 1


@pytest.mark.parametrize("response_lost", [False, True])
def test_retry_after_release_failure_reuses_signed_version(repo, monkeypatch, response_lost):
    change()
    original_sha = os.environ["GITHUB_SHA"]

    def fail(endpoint, method="GET", payload=None, paginate=False):
        if method == "POST":
            if response_lost:
                repo[4](endpoint, method, payload, paginate)
            raise ConnectionError("Release response failed")
        return repo[4](endpoint, method, payload, paginate)

    monkeypatch.setattr(release, "api", fail)
    with pytest.raises(ConnectionError):
        release.release()
    bumped = release.git("rev-parse", "HEAD")
    assert bumped != original_sha
    monkeypatch.setattr(release, "api", repo[4])
    release.release()
    assert outputs()["sha"] == bumped
    assert len(repo[2]) == 2
    assert len(repo[3]) == 1


def test_stale_main_validation_cannot_release_newer_changes(repo):
    validated = os.environ["GITHUB_SHA"]
    change()
    os.environ["GITHUB_SHA"] = validated
    release.release()
    assert outputs()["publish"] == "false"
    assert not repo[3]


def test_atomic_push_rejects_concurrent_tag_without_pushing_commit(repo, monkeypatch):
    change()
    before = os.environ["GITHUB_SHA"]

    def race(*args):
        subprocess.run(["git", "--git-dir", str(repo[1]), "update-ref",
                        "refs/tags/cloud-docs-v0.0.1", before], check=True)
        release.git(*args)

    monkeypatch.setattr(release, "authenticated_git", race)
    with pytest.raises(subprocess.CalledProcessError):
        release.release()
    assert release.git("ls-remote", "origin", "refs/heads/main").split()[0] == before
    assert len(repo[2]) == 1
    assert outputs()["publish"] == "false"


def test_atomic_push_preserves_concurrent_main_update_without_publishing_tag(repo, monkeypatch):
    change()
    before = os.environ["GITHUB_SHA"]
    concurrent = release.git("commit-tree", "-S", release.git("rev-parse", before + "^{tree}"),
                             "-p", before, "-m", "fix: apply a concurrent change")

    def race(*args):
        release.git("push", "origin", f"{concurrent}:refs/heads/main")
        release.git(*args)

    monkeypatch.setattr(release, "authenticated_git", race)
    with pytest.raises(subprocess.CalledProcessError):
        release.release()
    assert release.git("ls-remote", "origin", "refs/heads/main").split()[0] == concurrent
    assert not release.git("ls-remote", "origin", "refs/tags/cloud-docs-v0.0.1")
    assert len(repo[2]) == 1


def test_failed_site_build_does_not_publish_commit_tag_or_release(repo, monkeypatch):
    change()
    before = os.environ["GITHUB_SHA"]
    actual_run = release.subprocess.run

    def fail_build(command, **kwargs):
        if command == ["make", "docs"]:
            raise subprocess.CalledProcessError(1, command)
        return actual_run(command, **kwargs)

    monkeypatch.setattr(release.subprocess, "run", fail_build)
    with pytest.raises(subprocess.CalledProcessError):
        release.release()
    assert release.git("ls-remote", "origin", "refs/heads/main").split()[0] == before
    assert not release.git("ls-remote", "origin", "refs/tags/cloud-docs-v0.0.1")
    assert not repo[3]
    assert len(repo[2]) == 1


def test_manual_recovery_republishes_existing_version(repo, monkeypatch):
    monkeypatch.setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
    release.release()
    assert outputs()["publish"] == "true"
    assert outputs()["tag"] == "cloud-docs-v0.0.0"
    assert not repo[3]
    assert len(repo[2]) == 1


def test_existing_draft_is_published_without_duplicate(repo, monkeypatch):
    repo[2][0]["draft"] = True
    monkeypatch.setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
    release.release()
    assert not repo[2][0]["draft"]
    assert len(repo[2]) == 1


def test_non_main_and_dirty_checkouts_are_rejected(repo, monkeypatch):
    monkeypatch.setenv("GITHUB_REF", "refs/heads/feature")
    with pytest.raises(ValueError, match="restricted to main"):
        release.release()
    monkeypatch.setenv("GITHUB_REF", "refs/heads/main")
    Path("unexpected.txt").write_text("dirty\n")
    with pytest.raises(ValueError, match="must be clean"):
        release.release()
    assert not repo[3]


def test_missing_release_is_repaired_after_main_advances(repo, monkeypatch):
    change()
    release.release()
    bumped = release.git("rev-parse", "HEAD")
    repo[2].pop()
    change("unrelated.txt", "fix: repair an unrelated component")
    release.release()
    assert outputs()["sha"] == bumped
    assert len(repo[2]) == 2
    assert len(repo[3]) == 1


def test_api_errors_do_not_trigger_release_creation(repo, monkeypatch):
    client = Mock(side_effect=ConnectionError("Read failed"))
    monkeypatch.setattr(release, "api", client)
    with pytest.raises(ConnectionError):
        release.release()
    assert client.call_count == 1
    assert client.call_args.kwargs["paginate"]


def test_publication_uses_released_sha_and_main_only():
    workflow = Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml"
    text = workflow.read_text()
    assert "github.ref == 'refs/heads/main'" in text
    assert "!startsWith(github.event.head_commit.message, 'chore(release):')" in text
    assert "ref: ${{ needs.release.outputs.sha }}" in text
    assert "if: needs.release.outputs.publish == 'true'" in text
    assert "cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}" in text
    assert "pages: write" in text and "id-token: write" in text
