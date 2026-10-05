# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""`/exception` replies add dated exceptions; the weekly review challenges them."""

import datetime
import os
from pathlib import Path
from unittest.mock import patch

import pytest

import ci_automation as ci
import scanners

TODAY = datetime.date(2026, 10, 7)
FINDING = "<!-- ci-finding: bandit|B602|scripts/shell_example.py|9 -->\nfinding"
HEADER = "# Dated exceptions.\n"


def review_comment(number, body, user="goabonga", reply_to=None):
    return {"id": number, "body": body, "user": {"login": user}, "in_reply_to_id": reply_to}


class Api:
    def __init__(self, comments, permission="admin"):
        self.comments, self.permission, self.writes, self.resolved = comments, permission, [], []

    def __call__(self, endpoint, method="GET", payload=None, paginate=False, token=None):
        if endpoint == "graphql":
            return self.graphql(payload, token)
        if method != "GET":
            self.writes.append((endpoint, method, payload, token))
            return {}
        if endpoint == "user":
            return {"login": "goabonga"}
        if "/collaborators/" in endpoint:
            return {"permission": self.permission}
        if endpoint.endswith("/pulls/9"):
            return {"state": "open", "head": {"sha": "a" * 40, "ref": "feature/x"}}
        return [self.comments]

    def graphql(self, payload, token):
        if "resolveReviewThread" in payload["query"]:
            self.resolved.append((payload["variables"]["id"], token))
            return {}
        threads = [
            {"id": "T1", "isResolved": False, "comments": {"nodes": [{"databaseId": 1}]}},
            {"id": "T99", "isResolved": False, "comments": {"nodes": [{"databaseId": 99}]}},
        ]
        return {"data": {"repository": {"pullRequest": {"reviewThreads": {"nodes": threads}}}}}


def test_pending_requests_are_replies_to_our_findings_not_yet_answered():
    comments = [
        review_comment(1, FINDING),
        review_comment(2, "/exception 7d constant input", reply_to=1),
        review_comment(3, "/exception 7d answered", reply_to=1),
        review_comment(4, "<!-- ci-exception: 3 -->\nok", reply_to=1),
        review_comment(5, "/exception 7d not a finding thread", reply_to=99),
        review_comment(6, FINDING.replace("B602", "B603"), user="someone"),
        review_comment(7, "/exception 7d spoofed finding", reply_to=6),
    ]
    with patch.dict(os.environ, GH_REPO="owner/cloud"), patch.object(ci, "api", Api(comments)):
        pending = ci.pending_exceptions(9)
    assert [(comment["id"], key) for comment, key in pending] == [(2, "bandit|B602|scripts/shell_example.py|9")]


@pytest.fixture
def workspace(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    (tmp_path / ".github").mkdir()
    (tmp_path / ".github/exceptions.toml").write_text(HEADER)
    with patch.dict(
        os.environ,
        GH_REPO="owner/cloud",
        PR_NUMBER="9",
        HEAD_REF="feature/x",
        EXPECTED_SHA="a" * 40,
        PR_WRITE_TOKEN="maintainer-pat",
    ):
        yield tmp_path


def apply(comments, permission="admin"):
    api = Api(comments, permission)
    git_calls = []

    def git(*args):
        git_calls.append(args)
        return "signing-key" if args[:2] == ("config", "--get") else "c0ffee1"

    with (
        patch.object(ci, "api", api),
        patch.object(ci, "git", side_effect=git),
        patch.object(ci, "authenticated_git") as push,
    ):
        ci.exception_apply(today=TODAY)
    return api, git_calls, push


def test_a_valid_request_adds_a_dated_exception_in_a_signed_commit(workspace):
    api, git_calls, push = apply(
        [review_comment(1, FINDING), review_comment(2, "/exception 7d constant input", reply_to=1)]
    )
    [entry] = scanners.load_registry(workspace / ".github/exceptions.toml")
    assert entry == {
        "tool": "bandit",
        "rule": "B602",
        "path": "scripts/shell_example.py",
        "reason": "constant input (requested by goabonga in #9)",
        "added": TODAY,
        "expires": TODAY + datetime.timedelta(days=7),
    }
    assert (workspace / ".github/exceptions.toml").read_text().startswith(HEADER)
    commit = next(args for args in git_calls if "commit" in args)
    assert "--gpg-sign=signing-key" in commit and "chore: accept bandit finding until 2026-10-14" in commit
    push.assert_called_once_with(
        "push", f"--force-with-lease=refs/heads/feature/x:{'a' * 40}", "origin", "HEAD:refs/heads/feature/x"
    )
    [(endpoint, method, payload, token)] = api.writes
    assert endpoint.endswith("/pulls/9/comments/1/replies") and token == "maintainer-pat"
    assert payload["body"].startswith("<!-- ci-exception: 2 -->\n✅ Exception added for `bandit` `B602`")
    assert "until 2026-10-14, in `c0ffee1`" in payload["body"]
    assert api.resolved == [("T1", "maintainer-pat")]


@pytest.mark.parametrize("body", ["/exception 91d too long", "/exception 0d none", "/exception 7d", "/exception soon"])
def test_malformed_or_too_long_requests_are_answered_not_applied(workspace, body):
    api, git_calls, push = apply([review_comment(1, FINDING), review_comment(2, body, reply_to=1)])
    assert scanners.load_registry(workspace / ".github/exceptions.toml") == []
    push.assert_not_called()
    assert api.resolved == []
    [(_, _, payload, _)] = api.writes
    assert "Use `/exception <days>d <reason>` with 1 to 90 days" in payload["body"]


def test_requests_from_people_without_write_access_are_refused(workspace):
    api, _, push = apply([review_comment(1, FINDING), review_comment(2, "/exception 7d why", reply_to=1)], "read")
    push.assert_not_called()
    assert "needs write access" in api.writes[0][2]["body"]


def test_a_renewal_replaces_the_previous_exception(workspace):
    registry = workspace / ".github/exceptions.toml"
    old = {
        "tool": "bandit",
        "rule": "B602",
        "path": "scripts/shell_example.py",
        "reason": "old",
        "added": TODAY - datetime.timedelta(days=30),
        "expires": TODAY,
    }
    registry.write_text(scanners.add_exception(HEADER, old))
    apply([review_comment(1, FINDING), review_comment(2, "/exception 30d still needed", reply_to=1)])
    [entry] = scanners.load_registry(registry)
    assert entry["expires"] == TODAY + datetime.timedelta(days=30) and entry["reason"].startswith("still needed")


class ReviewApi:
    def __init__(self, open_pulls=()):
        self.open_pulls, self.writes = list(open_pulls), []

    def __call__(self, endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            self.writes.append((endpoint, method, payload))
            return {"number": 40 + len(self.writes)}
        if "/pulls?" in endpoint:
            return [self.open_pulls]
        return [[]]


def review(workspace, entries, open_pulls=()):
    registry = workspace / ".github/exceptions.toml"
    text = HEADER
    for entry in entries:
        text = scanners.add_exception(text, entry)
    registry.write_text(text)
    api, git_calls = ReviewApi(open_pulls), []

    def git(*args):
        git_calls.append(args)
        return "signing-key" if args[:2] == ("config", "--get") else ""

    with (
        patch.dict(os.environ, BASE_BRANCH="main"),
        patch.object(ci, "api", api),
        patch.object(ci, "git", side_effect=git),
        patch.object(ci, "authenticated_git") as push,
    ):
        ci.exceptions_review(today=TODAY)
    return api, git_calls, push


def dated(expires, rule="B602"):
    return {
        "tool": "bandit",
        "rule": rule,
        "path": "scripts/x.py",
        "reason": "r",
        "added": expires - datetime.timedelta(days=30),
        "expires": expires,
    }


def test_an_expiring_exception_gets_an_issue_and_a_pull_request_removing_it(workspace):
    api, git_calls, push = review(
        workspace, [dated(TODAY + datetime.timedelta(days=3)), dated(TODAY + datetime.timedelta(days=60), "B603")]
    )
    endpoints = [(endpoint, method) for endpoint, method, _ in api.writes]
    assert endpoints == [
        ("repos/owner/cloud/issues", "POST"),
        ("repos/owner/cloud/pulls", "POST"),
        ("repos/owner/cloud/issues/42/labels", "POST"),
    ]
    issue, pull = api.writes[0][2], api.writes[1][2]
    assert issue["title"] == "Security exception bandit B602 expires on 2026-10-10"
    assert issue["labels"] == ["security-exception"]
    assert pull["head"] == "exceptions/review-bandit-b602-scripts-x-py" and pull["body"].endswith("Closes #41")
    assert ("checkout", "--quiet", "-B", "exceptions/review-bandit-b602-scripts-x-py", "origin/main") in git_calls
    assert any("--gpg-sign=signing-key" in args for args in git_calls)
    push.assert_called_once_with(
        "push", "--force", "origin", "HEAD:refs/heads/exceptions/review-bandit-b602-scripts-x-py"
    )
    [kept] = scanners.load_registry(workspace / ".github/exceptions.toml")
    assert kept["rule"] == "B603"


def test_an_open_review_pull_request_is_not_opened_again(workspace):
    api, _, push = review(workspace, [dated(TODAY - datetime.timedelta(days=1))], open_pulls=[{"number": 5}])
    assert [(endpoint, method) for endpoint, method, _ in api.writes] == [("repos/owner/cloud/issues", "POST")]
    assert "expired on 2026-10-06" in api.writes[0][2]["title"]
    push.assert_not_called()


def test_nothing_to_review_writes_nothing(workspace):
    api, _, push = review(workspace, [dated(TODAY + datetime.timedelta(days=30))])
    assert api.writes == []
    push.assert_not_called()


def test_exception_workflows_are_triggered_safely():
    workflows = Path(__file__).resolve().parents[2] / ".github/workflows"
    signal = (workflows / "exception-signal.yml").read_text()
    assert "pull_request_review_comment:" in signal and "startsWith(github.event.comment.body, '/exception')" in signal
    assert "secrets." not in signal
    review_workflow = (workflows / "exceptions-review.yml").read_text()
    assert 'cron: "0 6 * * 1"' in review_workflow and "workflow_dispatch:" in review_workflow


def test_the_review_works_on_develop_once_it_exists(workspace):
    registry = workspace / ".github/exceptions.toml"
    registry.write_text(scanners.add_exception(HEADER, dated(TODAY + datetime.timedelta(days=3))))
    api, git_calls = ReviewApi(), []

    def git(*args):
        git_calls.append(args)
        return "signing-key" if args[:2] == ("config", "--get") else ""

    found = type("Result", (), {"returncode": 0})()
    with (
        patch.dict(os.environ, BASE_BRANCH="main"),
        patch.object(ci, "api", api),
        patch.object(ci, "git", side_effect=git),
        patch.object(ci, "authenticated_git"),
        patch.object(ci.subprocess, "run", return_value=found),
    ):
        ci.exceptions_review(today=TODAY)
    pull = next(payload for endpoint, method, payload in api.writes if endpoint.endswith("/pulls"))
    assert pull["base"] == "develop"
    assert ("checkout", "--quiet", "-B", "exceptions/review-bandit-b602-scripts-x-py", "origin/develop") in git_calls


def test_promotion_and_long_lived_branches_take_no_exception_commit():
    promotion = {"head": {"ref": "develop", "repo": {"full_name": "owner/cloud"}}, "base": {"ref": "main"}}
    feature = {"head": {"ref": "feature/x", "repo": {"full_name": "owner/cloud"}}, "base": {"ref": "develop"}}
    with patch.dict(os.environ, GH_REPO="owner/cloud"):
        with patch.object(ci, "api", return_value=[[promotion]]):
            assert ci.pull_request_for("develop") is None
        with patch.object(ci, "api", return_value=[[feature]]):
            assert ci.pull_request_for("feature/x") == feature
