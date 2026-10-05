# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Exercise dependency rewriting in disposable repositories and keyrings."""

import pytest

import os
from pathlib import Path
import subprocess

SCRIPT = Path(__file__).resolve().parents[1] / "rewrite_dependabot_commit.py"


@pytest.fixture(scope="module")
def signing_key(tmp_path_factory):
    keyring = tmp_path_factory.mktemp("rewrite-gpg")
    keyring.chmod(0o700)
    env = {**os.environ, "GNUPGHOME": str(keyring)}
    subprocess.run(
        ["gpg", "--batch", "--passphrase", "", "--quick-generate-key",
         "Rewrite Test <rewrite@example.test>", "ed25519", "sign", "0"],
        env=env, check=True, capture_output=True,
    )
    try:
        yield keyring
    finally:
        subprocess.run(["gpgconf", "--kill", "gpg-agent"], env=env, check=True)


@pytest.fixture
def repo(tmp_path, signing_key, monkeypatch):
    monkeypatch.setenv("GNUPGHOME", str(signing_key))
    repo = tmp_path
    git(repo, "init", "--initial-branch=main")
    git(repo, "config", "user.name", "Chris")
    git(repo, "config", "user.email", "goabonga@pm.me")
    git(repo, "config", "user.signingkey", "rewrite@example.test")
    git(repo, "config", "commit.gpgsign", "false")
    write(repo, "go.mod", "module example.test/cloud\n\ngo 1.26.0\n")
    commit(repo, "chore: initialize test repository", "go.mod")
    return repo


def test_signing_setup_wrapper_and_cleanup(repo, tmp_path):
    private_key = subprocess.check_output(
        [
            "gpg",
            "--batch",
            "--armor",
            "--export-secret-keys",
            "rewrite@example.test",
        ],
    ).decode()
    runtime = tmp_path / "runner"
    runtime.mkdir()
    github_env = tmp_path / "github.env"
    env = {
        **os.environ,
        "RUNNER_TEMP": str(runtime),
        "GITHUB_ENV": str(github_env),
        "GPG_PRIVATE_KEY": private_key,
        "GPG_PASSPHRASE": "",
        "PUSH_TOKEN": "test-token",
        "GIT_USER_NAME": "Chris",
        "GIT_USER_EMAIL": "goabonga@pm.me",
        "GIT_SIGNING_KEY": "rewrite@example.test",
    }
    automation = SCRIPT.with_name("ci_automation.py")
    subprocess.run(
        ["python3", str(automation), "configure-signing"],
        cwd=repo,
        env=env,
        check=True,
        capture_output=True,
    )
    home = Path(github_env.read_text().strip().split("=", 1)[1])
    assert home.is_dir()
    env["GNUPGHOME"] = str(home)
    try:
        write(repo, "feature.py", "print('feature')\n")
        commit(repo, "feat: add feature", "feature.py")
        subprocess.run(
            ["python3", str(SCRIPT.with_name("sign_commit.py"))],
            cwd=repo,
            env=env,
            check=True,
            capture_output=True,
        )
        assert subprocess.check_output(
            ["git", "log", "-1", "--format=%G?"], cwd=repo, env=env
        ).decode().strip() in {"G", "U"}
        subprocess.run(
            ["git", "verify-commit", "HEAD"],
            cwd=repo,
            env=env,
            check=True,
            capture_output=True,
        )
    finally:
        subprocess.run(["python3", str(automation), "cleanup"], env=env, check=True)
    assert not home.exists()


def git(repo, *args):
    return (
        subprocess.check_output(
            ["git", *args],
            cwd=repo,
            stderr=subprocess.DEVNULL,
        )
        .decode()
        .strip()
    )


def write(repo, path, content):
    target = Path(repo, path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content)


def commit(repo, message, path):
    git(repo, "add", path)
    git(
        repo,
        "-c",
        "user.name=dependabot[bot]",
        "-c",
        "user.email=dependabot[bot]@users.noreply.github.com",
        "commit",
        "--no-gpg-sign",
        "-m",
        message,
    )


def rewrite(repo, script=SCRIPT):
    return subprocess.run(
        ["python3", str(script)],
        cwd=repo,
        capture_output=True,
        text=True,
    )


def assert_rewritten(repo, subject, script=SCRIPT):
    result = rewrite(repo, script)
    assert result.returncode == 0, result.stderr
    assert git(repo, "log", "-1", "--format=%s") == subject
    assert git(repo, "log", "-1", "--format=%an <%ae>") == "Chris <goabonga@pm.me>"
    assert git(repo, "log", "-1", "--format=%cn <%ce>") == "Chris <goabonga@pm.me>"
    assert git(repo, "log", "-1", "--format=%G?") == "G"


def test_action_update_is_signed_as_maintainer(repo):
    write(repo, ".github/workflows/check.yml", "name: check\n")
    commit(repo, "chore: Bump checkout", ".github/workflows/check.yml")
    assert_rewritten(repo, "ci: bump checkout")


def test_go_update_is_a_fix(repo):
    write(repo, "go.mod", "module example.test/cloud\n\ngo 1.26.1\n")
    commit(repo, "chore(deps): Bump Go", "go.mod")
    assert_rewritten(repo, "fix(deps): bump Go")


def python_metadata(*types):
    return "".join(f"- dependency-name: dep{n}\n  dependency-type: {kind}\n" for n, kind in enumerate(types))


def python_update(repo, message):
    write(repo, "scripts/pyproject.toml", '[project]\nname = "x"\ndependencies = ["pillow==12.4.0"]\n')
    write(repo, "scripts/uv.lock", "version = 1\n")
    commit(repo, message, "scripts")


def test_python_development_update_remains_chore(repo):
    python_update(repo, "chore(deps): Bump pytest\n\n---\nupdated-dependencies:\n"
                  + python_metadata("direct:development"))
    assert_rewritten(repo, "chore(deps): bump pytest")


@pytest.mark.parametrize("types", [("direct:production",), ("direct:development", "direct:production"),
                                   ("indirect",)])
def test_python_runtime_update_is_a_fix(repo, types):
    python_update(repo, "chore(deps): Bump cairosvg\n\n---\nupdated-dependencies:\n" + python_metadata(*types))
    assert_rewritten(repo, "fix(deps): bump cairosvg")


def test_python_update_without_metadata_is_a_fix(repo):
    python_update(repo, "Bump cairosvg")
    assert_rewritten(repo, "fix(deps): bump cairosvg")


def test_mixed_ecosystem_paths_leave_head_unchanged(repo):
    write(repo, "scripts/uv.lock", "version = 1\n")
    write(repo, "scripts/ci_automation.py", "print('changed')\n")
    commit(repo, "Bump pillow", "scripts")
    before = git(repo, "rev-parse", "HEAD")
    assert rewrite(repo).returncode != 0
    assert git(repo, "rev-parse", "HEAD") == before


def test_dependabot_body_and_trailers_are_dropped(repo):
    write(repo, ".github/workflows/check.yaml", "name: check\n")
    commit(
        repo,
        "fix(deps)!: Bump checkout\n\nupdated-dependencies:\n- dependency-name: actions/checkout\n\nco-authored-by: Bot <bot@example.test>\nSigned-off-by: Bot <bot@example.test>\nBREAKING_CHANGE: update\nGenerated with a tool",
        ".github/workflows/check.yaml",
    )
    assert_rewritten(repo, "ci: bump checkout")
    assert git(repo, "log", "-1", "--format=%b") == ""


GROUPED_ACTIONS = """ci: bump the github-actions group with 2 updates

Bumps the github-actions group with 2 updates: [actions/setup-go](https://github.com/actions/setup-go) and [getplumber/plumber](https://github.com/getplumber/plumber).

Updates `actions/setup-go` from 6.5.0 to 7.0.0
- [Release notes](https://github.com/actions/setup-go/releases)

Updates `getplumber/plumber` from 0.5.15 to 0.5.20
- [Release notes](https://github.com/getplumber/plumber/releases)

---
updated-dependencies:
- dependency-name: actions/setup-go
  dependency-version: 7.0.0
  dependency-type: direct:production
  update-type: version-update:semver-major
  dependency-group: github-actions
- dependency-name: getplumber/plumber
  dependency-version: 0.5.20
  dependency-type: direct:production
  update-type: version-update:semver-patch
  dependency-group: github-actions
...

Signed-off-by: dependabot[bot] <support@github.com>
"""


def workflow(go, plumber):
    return ("jobs:\n  audit:\n    steps:\n"
            f"      - uses: getplumber/plumber@{plumber}\n"
            "      - run: echo audit\n  go:\n    steps:\n"
            f"      - uses: actions/setup-go@{go}\n      - uses: actions/setup-go@{go}\n")


def commits_since(repo, base):
    return git(repo, "log", "--reverse", "--format=%H", f"{base}..HEAD").splitlines()


def test_grouped_update_becomes_one_signed_commit_per_dependency(repo):
    write(repo, ".github/workflows/ci.yml", workflow("v6", "v0.5.15"))
    commit(repo, "ci: add workflow", ".github/workflows/ci.yml")
    base = git(repo, "rev-parse", "HEAD")
    write(repo, ".github/workflows/ci.yml", workflow("v7", "v0.5.20"))
    commit(repo, GROUPED_ACTIONS, ".github/workflows/ci.yml")
    final = git(repo, "rev-parse", "HEAD^{tree}")

    result = rewrite(repo)

    assert result.returncode == 0, result.stderr
    assert git(repo, "rev-parse", "HEAD^{tree}") == final
    rewritten = commits_since(repo, base)
    assert [git(repo, "log", "-1", "--format=%B", sha) for sha in rewritten] == [
        "ci: bump `actions/setup-go` from 6.5.0 to 7.0.0",
        "ci: bump `getplumber/plumber` from 0.5.15 to 0.5.20",
    ]
    assert "actions/setup-go@v7" in git(repo, "show", f"{rewritten[0]}:.github/workflows/ci.yml")
    assert "plumber@v0.5.15" in git(repo, "show", f"{rewritten[0]}:.github/workflows/ci.yml")
    for sha in rewritten:
        assert git(repo, "show", "-s", "--format=%an <%ae> %G?", sha) == "Chris <goabonga@pm.me> G"


PYPROJECT = '[project]\nname = "cloud-scripts"\ndependencies = ["cairosvg=={cairo}"]\n\n[dependency-groups]\ndev = ["pytest=={pytest}"]\n'
LOCK = """version = 1

[[package]]
name = "cairosvg"
version = "{cairo}"
sdist = {{ url = "https://files.example.test/cairosvg-{cairo}.tar.gz" }}

[[package]]
name = "cloud-scripts"
version = "0.0.0"

[package.metadata]
requires-dist = [{{ name = "cairosvg", specifier = "=={cairo}" }}]

[package.metadata.requires-dev]
dev = [{{ name = "pytest", specifier = "=={pytest}" }}]

[[package]]
name = "pytest"
version = "{pytest}"
sdist = {{ url = "https://files.example.test/pytest-{pytest}.tar.gz" }}
"""


def python_versions(repo, cairo, pytest_version):
    write(repo, "scripts/pyproject.toml", PYPROJECT.format(cairo=cairo, pytest=pytest_version))
    write(repo, "scripts/uv.lock", LOCK.format(cairo=cairo, pytest=pytest_version))


def test_python_group_splits_with_a_prefix_per_dependency_type(repo):
    python_versions(repo, "2.8.2", "9.0.3")
    commit(repo, "chore: add scripts", "scripts")
    base = git(repo, "rev-parse", "HEAD")
    python_versions(repo, "2.9.1", "9.1.1")
    commit(repo, "chore(deps): bump the python group with 2 updates\n\n"
           "Updates `cairosvg` from 2.8.2 to 2.9.1\nUpdates `pytest` from 9.0.3 to 9.1.1\n\n---\n"
           "updated-dependencies:\n" + python_metadata("direct:production", "direct:development")
           .replace("dep0", "cairosvg").replace("dep1", "pytest"), "scripts")
    final = git(repo, "rev-parse", "HEAD^{tree}")

    assert rewrite(repo).returncode == 0
    assert git(repo, "rev-parse", "HEAD^{tree}") == final
    rewritten = commits_since(repo, base)
    assert [git(repo, "log", "-1", "--format=%B", sha) for sha in rewritten] == [
        "fix(deps): bump `cairosvg` from 2.8.2 to 2.9.1",
        "chore(deps): bump `pytest` from 9.0.3 to 9.1.1",
    ]
    first_lock = git(repo, "show", f"{rewritten[0]}:scripts/uv.lock")
    assert 'version = "2.9.1"' in first_lock and 'version = "9.0.3"' in first_lock


def test_unattributable_changes_keep_a_single_commit_without_body(repo):
    write(repo, ".github/workflows/ci.yml", workflow("v6", "v0.5.15") + "env:\n  A: 1\n")
    commit(repo, "ci: add workflow", ".github/workflows/ci.yml")
    base = git(repo, "rev-parse", "HEAD")
    write(repo, ".github/workflows/ci.yml", workflow("v7", "v0.5.20") + "env:\n  A: 2\n")
    commit(repo, GROUPED_ACTIONS, ".github/workflows/ci.yml")
    final = git(repo, "rev-parse", "HEAD^{tree}")

    assert rewrite(repo).returncode == 0
    assert git(repo, "rev-parse", "HEAD^{tree}") == final
    [sha] = commits_since(repo, base)
    assert git(repo, "log", "-1", "--format=%B", sha) == "ci: bump the github-actions group with 2 updates"


def test_single_update_names_the_dependency_and_versions(repo):
    write(repo, ".github/workflows/check.yml", "uses: actions/checkout@v7\n")
    commit(repo, "ci: bump actions/checkout from 6 to 7\n\nBumps [actions/checkout](https://github.com/actions/checkout) from 6 to 7.\n", ".github/workflows/check.yml")
    assert_rewritten(repo, "ci: bump `actions/checkout` from 6 to 7")


def test_long_dependency_names_keep_the_subject_bounded(repo):
    name = "example.test/" + "very-long-module-name/" * 3 + "client"
    write(repo, "go.mod", f"module example.test/cloud\n\nrequire {name} v1.2.3\n")
    commit(repo, f"chore(deps): bump {name}\n\nBumps [{name}](https://example.test) from 1.2.2 to 1.2.3.\n", "go.mod")
    result = rewrite(repo)
    assert result.returncode == 0, result.stderr
    message = git(repo, "log", "-1", "--format=%s")
    assert len(message) <= 72 and message.startswith("fix(deps): bump `")


def test_unsupported_paths_leave_head_unchanged(repo):
    write(repo, "untrusted.sh", "exit 99\n")
    commit(repo, "Bump unexpected code", "untrusted.sh")
    before = git(repo, "rev-parse", "HEAD")
    assert rewrite(repo).returncode != 0
    assert git(repo, "rev-parse", "HEAD") == before


def test_subject_is_bounded_and_treated_as_data(repo):
    write(repo, ".github/workflows/check.yml", "name: check\n")
    commit(
        repo,
        "Bump $(touch injected) " + "x" * 100, ".github/workflows/check.yml"
    )
    result = rewrite(repo)
    assert result.returncode == 0, result.stderr
    assert len(git(repo, "log", "-1", "--format=%s")) <= 72
    assert not Path(repo, "injected").exists()


def test_rebase_rewrites_every_dependency_commit(repo):
    base = git(repo, "rev-parse", "HEAD")
    write(repo, ".github/workflows/check.yml", "name: check\n")
    commit(repo, "Bump checkout", ".github/workflows/check.yml")
    write(repo, "go.mod", "module example.test/cloud\n\ngo 1.26.1\n")
    commit(repo, "Bump Go", "go.mod")
    git(repo, "rebase", "--force-rebase", base, "--exec", f"python3 '{SCRIPT}'")
    assert git(repo, "rev-list", "--count", f"{base}..HEAD") == "2"
    for commit_sha in git(repo, "rev-list", f"{base}..HEAD").splitlines():
        assert (
            git(repo, "show", "-s", "--format=%an <%ae>", commit_sha)
            == "Chris <goabonga@pm.me>"
        )
        assert git(repo, "show", "-s", "--format=%G?", commit_sha) == "G"


def test_signed_merge_preserves_type_and_removes_forbidden_trailers(repo):
    script = SCRIPT.with_name("sign_commit.py")
    write(repo, "feature.sh", "touch injected\n")
    commit(
        repo,
        "fix!: repair startup\n\nReason for the fix.\n\nCo-Authored-By: Bot <bot@example.test>\nBREAKING CHANGE: old marker",
        "feature.sh",
    )
    assert_rewritten(repo, "fix: repair startup", script)
    message = git(repo, "log", "-1", "--format=%B")
    assert "Reason for the fix." in message
    assert "Co-Authored-By" not in message
    assert "BREAKING CHANGE" not in message
    assert not Path(repo, "injected").exists()


def test_signed_merge_does_not_execute_git_hooks(repo):
    script = SCRIPT.with_name("sign_commit.py")
    write(repo, "feature.py", "print('feature')\n")
    commit(repo, "feat: add feature", "feature.py")
    hook = Path(repo, ".git/hooks/pre-commit")
    hook.write_text("#!/bin/sh\ntouch injected\nexit 1\n")
    hook.chmod(493)
    assert_rewritten(repo, "feat: add feature", script)
    assert not Path(repo, "injected").exists()


def test_signed_merge_rejects_invalid_subject(repo):
    write(repo, "feature.py", "print('feature')\n")
    commit(repo, "invalid commit message", "feature.py")
    before = git(repo, "rev-parse", "HEAD")
    assert rewrite(repo, SCRIPT.with_name("sign_commit.py")).returncode != 0
    assert git(repo, "rev-parse", "HEAD") == before


GO_GROUP = """fix(deps): bump the go-dependencies group in / with 3 updates

Bumps the go-dependencies group with 3 updates in the / directory: [github.com/a/one](https://github.com/a/one), [github.com/b/two](https://github.com/b/two) and [github.com/c/three](https://github.com/c/three).

Updates `github.com/a/one` from 1.0.0 to 1.1.0
Updates `github.com/b/two` from 2.3.0 to 2.3.1
Updates `github.com/c/three` from 0.9.0 to 1.0.0

---
updated-dependencies:
- dependency-name: github.com/a/one
  dependency-version: 1.1.0
  dependency-type: direct:production
- dependency-name: github.com/b/two
  dependency-version: 2.3.1
  dependency-type: direct:production
- dependency-name: github.com/c/three
  dependency-version: 1.0.0
  dependency-type: direct:production
...
"""


def go_files(repo, one, two, three):
    write(repo, "go.mod", "module example.test/cloud\n\ngo 1.26.0\n\nrequire (\n"
          f"\tgithub.com/a/one v{one}\n\tgithub.com/b/two v{two}\n\tgithub.com/c/three v{three}\n)\n")
    write(repo, "go.sum", "".join(f"{name} v{version} h1:{name[-3:]}{version}=\n{name} v{version}/go.mod h1:x=\n"
                                  for name, version in (("github.com/a/one", one), ("github.com/b/two", two),
                                                        ("github.com/c/three", three))))


def test_any_group_size_splits_go_modules_one_commit_each(repo):
    go_files(repo, "1.0.0", "2.3.0", "0.9.0")
    git(repo, "add", "go.mod", "go.sum")
    git(repo, "commit", "--no-gpg-sign", "-m", "chore: add modules")
    base = git(repo, "rev-parse", "HEAD")
    go_files(repo, "1.1.0", "2.3.1", "1.0.0")
    git(repo, "add", "go.mod", "go.sum")
    commit(repo, GO_GROUP, "go.mod")
    final = git(repo, "rev-parse", "HEAD^{tree}")

    result = rewrite(repo)

    assert result.returncode == 0, result.stderr
    assert git(repo, "rev-parse", "HEAD^{tree}") == final
    assert [git(repo, "log", "-1", "--format=%B", sha) for sha in commits_since(repo, base)] == [
        "fix(deps): bump `github.com/a/one` from 1.0.0 to 1.1.0",
        "fix(deps): bump `github.com/b/two` from 2.3.0 to 2.3.1",
        "fix(deps): bump `github.com/c/three` from 0.9.0 to 1.0.0",
    ]
