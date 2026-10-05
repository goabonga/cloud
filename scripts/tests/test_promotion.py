# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""develop is promoted to main as it is, never automatically, and synced back."""

import json
import os
from pathlib import Path
import re
import subprocess
from unittest.mock import patch

import pytest

import ci_automation as ci

ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40


def pull(head="feature/x", base="main", sha=SHA, labels=("auto-merge",)):
    return {
        "state": "open",
        "draft": False,
        "head": {"ref": head, "sha": sha, "repo": {"full_name": "owner/cloud"}},
        "base": {"ref": base},
        "labels": [{"name": name} for name in labels],
    }


@pytest.mark.parametrize(
    "head,base,allowed",
    [
        ("feature/x", "main", True),
        ("feature/x", "develop", True),
        ("develop", "main", True),
        ("main", "develop", False),
        ("develop", "develop", False),
    ],
)
def test_the_queue_merges_into_main_or_develop_and_develop_only_as_a_promotion(head, base, allowed):
    env = {"GH_REPO": "owner/cloud", "HEAD_REF": head, "BASE_REF": base, "MERGE_LABEL": "auto-merge"}
    with patch.dict(os.environ, env):
        assert ci.merge_authorized(pull(head, base), SHA) is allowed


def test_other_bases_are_refused():
    with patch.dict(os.environ, BASE_REF="release/1"), pytest.raises(ValueError, match="does not merge into"):
        ci.base_branch()


def git_repo(path):
    def run(*args):
        return subprocess.run(["git", *args], cwd=path, check=True, capture_output=True, text=True).stdout.strip()

    run("init", "-q", "-b", "main")
    run("config", "user.name", "Test")
    run("config", "user.email", "test@example.test")
    run("config", "commit.gpgsign", "false")
    return run


def commit(run, path, name):
    (path / name).write_text(name)
    run("add", name)
    run("commit", "-q", "-m", f"feat: add {name}")
    return run("rev-parse", "HEAD")


@pytest.fixture
def promotion(tmp_path, monkeypatch):
    """main at one commit, develop one signed commit ahead, as origin."""
    origin = tmp_path / "origin"
    origin.mkdir()
    run = git_repo(origin)
    base = commit(run, origin, "a")
    run("checkout", "-q", "-b", "develop")
    head = commit(run, origin, "b")
    work = tmp_path / "work"
    subprocess.run(["git", "clone", "-q", str(origin), str(work)], check=True)
    subprocess.run(["git", "checkout", "-q", "--detach", head], cwd=work, check=True)
    monkeypatch.chdir(work)
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    monkeypatch.setenv("GH_REPO", "owner/cloud")
    monkeypatch.setenv("HEAD_SHA", head)
    return {"origin": origin, "run": run, "base": base, "head": head, "tmp": tmp_path}


def compare(*verified):
    return [{"commits": [{"sha": "c" * 40, "commit": {"verification": {"verified": state}}} for state in verified]}]


def test_a_promotion_records_main_and_keeps_every_commit(promotion):
    with patch.object(ci, "api", return_value=compare(True)):
        ci.check_promotion()
    assert (promotion["tmp"] / "merge-base-sha").read_text() == promotion["base"]


def test_a_promotion_refuses_unverified_commits(promotion):
    with patch.object(ci, "api", return_value=compare(True, False)), pytest.raises(ValueError, match="Unverified"):
        ci.check_promotion()


def test_a_promotion_needs_develop_to_contain_main(promotion):
    run = promotion["run"]
    run("checkout", "-q", "main")
    commit(run, promotion["origin"], "hotfix")
    with patch.object(ci, "api", return_value=compare(True)), pytest.raises(ValueError, match="does not contain main"):
        ci.check_promotion()


def sync(promotion):
    with (
        patch.object(ci, "api", return_value={"name": "develop"}),
        patch.object(ci, "authenticated_git", side_effect=lambda *args: subprocess.run(["git", *args], check=True)),
    ):
        ci.sync_develop()
    return promotion["run"]("rev-parse", "develop")


def test_sync_moves_develop_forward_to_the_release_commit(promotion):
    run = promotion["run"]
    run("checkout", "-q", "main")
    run("merge", "-q", "--ff-only", "develop")
    release = commit(run, promotion["origin"], "release")
    run("checkout", "-q", "--detach")
    assert sync(promotion) == release


def test_sync_leaves_a_develop_that_contains_main(promotion):
    promotion["run"]("checkout", "-q", "--detach")
    assert sync(promotion) == promotion["head"]


def test_sync_replays_develop_on_a_main_that_moved_on_its_own(promotion, monkeypatch):
    run = promotion["run"]
    run("checkout", "-q", "main")
    hotfix = commit(run, promotion["origin"], "hotfix")
    run("checkout", "-q", "--detach")
    # The replayed commits are re-signed by sign_commit.py; here it only amends.
    signer = promotion["tmp"] / "sign_commit.py"
    signer.write_text(
        "import subprocess\nsubprocess.run(['git', 'commit', '--amend', '--no-edit', '-q'], check=True)\n"
    )
    monkeypatch.setattr(ci, "__file__", str(signer))
    synced = sync(promotion)
    assert run("rev-parse", f"{synced}~1") == hotfix
    assert run("show", "-s", "--format=%s", synced) == "feat: add b"


def test_without_develop_there_is_nothing_to_sync(capsys):
    error = subprocess.CalledProcessError(1, "gh")
    with patch.dict(os.environ, GH_REPO="owner/cloud"), patch.object(ci, "api", side_effect=error):
        ci.sync_develop()
    assert "No develop branch" in capsys.readouterr().out


class Api:
    """Issues, pull requests and permissions for promote()."""

    def __init__(self, open_pull=None, permission="admin", issue_body="### Notes\n\nShip it"):
        self.open_pull, self.permission, self.issue_body = open_pull, permission, issue_body
        self.writes = []

    def __call__(self, endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            self.writes.append((endpoint, method, payload))
            if endpoint.endswith("/pulls"):
                return {"number": 50}
            if endpoint.endswith("/issues"):
                return {"number": 60}
            return {}
        if "/collaborators/" in endpoint:
            return {"permission": self.permission}
        if "/pulls?" in endpoint:
            return [[self.open_pull] if self.open_pull else []]
        return {"number": 9, "body": self.issue_body}


# As `multicz plan --output json` reports a bump.
PLAN = {
    "cloud": {
        "current_version": "0.0.3",
        "next_version": "0.1.0",
        "kind": "minor",
        "reasons": [{"kind": "commit", "sha": "b" * 40, "type": "feat", "scope": "cli", "subject": "add a command"}],
        "artifacts": [],
    }
}
COMMITS = [["b" * 40, "feat(cli): add a command"]]


def promote(api, event="issues", issue="9", plan=(PLAN, COMMITS)):
    env = {
        "GH_REPO": "owner/cloud",
        "EVENT": event,
        "ISSUE_NUMBER": issue or "",
        "ISSUE_AUTHOR": "goabonga",
        "PROMOTION_DIR": "develop",
        "MERGE_LABEL": "auto-merge",
    }
    with (
        patch.dict(os.environ, env),
        patch.object(ci, "api", api),
        patch.object(ci, "promotion_plan", return_value=plan),
        patch.object(ci, "git", return_value="d" * 40),
    ):
        ci.promote()
    return api.writes


def test_a_promotion_issue_gets_the_plan_and_a_pull_request_from_the_template():
    writes = promote(Api())
    issue = next(payload for endpoint, method, payload in writes if endpoint.endswith("/issues/9"))
    assert issue["body"].startswith("### Notes\n\nShip it\n\n" + ci.PROMOTION_MARKER)
    assert (
        "| `cloud` | 0.0.3 | 0.1.0 | minor |" in issue["body"]
        and "- `bbbbbbb` feat(cli): add a command" in issue["body"]
    )
    pr = next(payload for endpoint, method, payload in writes if endpoint.endswith("/pulls"))
    assert (pr["head"], pr["base"], pr["title"]) == ("develop", "main", ci.PROMOTION_TITLE)
    assert ci.section(pr["body"], "Related issues") == "Closes #9"
    assert "| `cloud` | 0.0.3 | 0.1.0 | minor |" in ci.section(pr["body"], "Description")
    assert "<!-- ci-box: audit -->" in ci.section(pr["body"], "Pipeline")
    # Never queued for merging by the workflow.
    assert not any("/labels" in endpoint for endpoint, _, _ in writes)
    comment = next(payload for endpoint, method, payload in writes if endpoint.endswith("/9/comments"))
    assert "#50" in comment["body"] and "add the `auto-merge` label" in comment["body"]


def test_a_rerun_refreshes_the_generated_parts_and_keeps_the_ticked_boxes():
    template = ci.PR_TEMPLATE.read_text()
    body = ci.fill_sections(template, {"Description": "old", "Related issues": "Closes #9"}).replace(
        "- [ ] audit <!-- ci-box: audit -->", "- [x] audit <!-- ci-box: audit --> passed on `abc1234`"
    )
    writes = promote(Api(open_pull={**pull("develop", "main"), "number": 50, "body": body}), event="push", issue=None)
    [(endpoint, method, payload)] = [write for write in writes if write[0].endswith("/pulls/50")]
    assert "passed on `abc1234`" in payload["body"] and "minor" in ci.section(payload["body"], "Description")


def test_a_second_promotion_issue_is_closed_as_a_duplicate():
    open_pull = {**pull("develop", "main"), "number": 50, "body": "## Related issues\n\nCloses #8\n"}
    writes = promote(Api(open_pull=open_pull))
    assert writes[-1] == ("repos/owner/cloud/issues/9", "PATCH", {"state": "closed", "state_reason": "duplicate"})
    assert "already proposed in #50, tracked by #8" in writes[0][2]["body"]


def test_only_maintainers_can_propose_a_release():
    writes = promote(Api(permission="read"))
    assert "needs write access" in writes[0][2]["body"] and writes[-1][2]["state"] == "closed"


def test_nothing_to_promote_closes_the_issue():
    writes = promote(Api(), plan=({}, []))
    assert "nothing that `main` lacks" in writes[0][2]["body"] and writes[-1][2]["state_reason"] == "not_planned"


def test_a_manual_run_creates_the_issue_and_a_push_without_promotion_does_nothing():
    writes = promote(Api(), event="workflow_dispatch", issue=None)
    created = next(payload for endpoint, method, payload in writes if endpoint.endswith("/issues"))
    assert created["labels"] == ["release"] and created["title"] == ci.PROMOTION_TITLE
    assert ci.section(next(p for e, m, p in writes if e.endswith("/pulls"))["body"], "Related issues") == "Closes #60"
    assert promote(Api(), event="push", issue=None) == []


def test_the_promotion_content_lists_the_plan_and_the_commits():
    content = ci.promotion_content({}, COMMITS)
    assert "No component would be released." in content and "- `bbbbbbb` feat(cli): add a command" in content


def test_promotion_workflows_are_wired_safely():
    workflows = ROOT / ".github/workflows"
    promotion = (workflows / "promotion.yml").read_text()
    assert "issues:\n    types: [opened]" in promotion and "workflow_dispatch:" in promotion
    assert "contains(github.event.issue.labels.*.name, 'release')" in promotion
    assert "${{ github.event.issue.title" not in promotion and "${{ github.event.issue.body" not in promotion
    merge = (workflows / "auto-merge-signed.yml").read_text()
    assert "branches: [main, develop]" in merge and "check-promotion" in merge
    assert merge.count("if: steps.validate.outputs.promotion != 'true'") == 3
    ci_workflow = (workflows / "ci.yml").read_text()
    sync_job = ci_workflow.split("\n  sync-develop:\n")[1]
    assert "github.ref == 'refs/heads/main'" in sync_job and "sync-develop" in sync_job
    form = (ROOT / ".github/ISSUE_TEMPLATE/release_promotion.yml").read_text()
    assert "labels: [release]" in form and re.search(r"label: Notes", form)
    labels = (ROOT / ".github/repository.toml").read_text()
    assert "[labels.release]" in labels


def test_promotion_plan_reads_multicz_and_the_commits_main_lacks(tmp_path):
    outputs = {"multicz": json.dumps({"bumps": PLAN}), "git": "b" * 40 + "\tfeat(cli): add a command\n"}
    with patch.object(ci, "run", side_effect=lambda *args, **kwargs: outputs[args[0]]):
        assert ci.promotion_plan(tmp_path) == (PLAN, COMMITS)
