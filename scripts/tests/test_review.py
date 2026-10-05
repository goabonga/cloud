# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Each scanner finding gets its own review thread, on its line or on its file."""

import os
from unittest.mock import patch

import pytest

import ci_automation as ci

PATCH = "@@ -8,3 +8,5 @@ import subprocess\n import sys\n-old\n+subprocess.run(cmd, shell=True)\n+x = 1\n context\n"


def finding(tool="bandit", rule="B602", path="scripts/shell_example.py", line=9, message="shell=True"):
    return {"tool": tool, "rule": rule, "path": path, "line": line, "message": message}


def test_commentable_lines_follow_the_new_side_of_each_hunk():
    assert ci.commentable_lines(PATCH) == {8, 9, 10, 11}
    assert ci.commentable_lines(None) == set()


class Api:
    def __init__(self, comments=(), files=None):
        self.comments = list(comments)
        self.files = files if files is not None else [{"filename": "scripts/shell_example.py", "patch": PATCH}]
        self.writes = []

    def __call__(self, endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            self.writes.append((endpoint, method, payload))
            return {}
        if endpoint == "user":
            return {"login": "goabonga"}
        if "/files" in endpoint:
            return [self.files]
        return [self.comments]


@pytest.fixture(autouse=True)
def env():
    with patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA="a" * 40):
        yield


def test_findings_go_on_their_line_or_their_file_without_a_summary():
    api = Api(files=[{"filename": "scripts/shell_example.py", "patch": PATCH}, {"filename": "go.mod", "patch": ""}])
    on_line = finding()
    no_line = finding(tool="osv-scanner", rule="GO-2021-0113", path="go.mod", line=0, message="golang.org/x/text 0.3.5")
    off_lines = finding(rule="B603", line=40, message="subprocess without shell")
    unchanged_file = finding(rule="B310", path="scripts/ci_automation.py", line=40, message="urlopen")
    with patch.object(ci, "api", api):
        ci.review_findings(7, "abcdef1", [on_line, no_line, off_lines, unchanged_file])
    assert all(endpoint.endswith("/pulls/7/comments") for endpoint, _, _ in api.writes)
    line_thread, go_mod, off_diff = (payload for _, _, payload in api.writes)
    assert line_thread["commit_id"] == "a" * 40
    assert (line_thread["path"], line_thread["line"], line_thread["side"]) == ("scripts/shell_example.py", 9, "RIGHT")
    assert "<!-- ci-finding: bandit|B602|scripts/shell_example.py|9 -->" in line_thread["body"]
    assert "reply `/exception <days>d <reason>`" in line_thread["body"]
    assert (go_mod["path"], go_mod["subject_type"]) == ("go.mod", "file") and "line" not in go_mod
    assert (off_diff["path"], off_diff["subject_type"]) == ("scripts/shell_example.py", "file")


def comment(key, number=1, user="goabonga", body=None, in_reply_to=None):
    return {
        "id": number,
        "user": {"login": user},
        "body": body or f"<!-- ci-finding: {key} -->\nfinding",
        "in_reply_to_id": in_reply_to,
    }


def test_findings_already_under_review_are_not_posted_again():
    api = Api(comments=[comment("bandit|B602|scripts/shell_example.py|9")])
    with patch.object(ci, "api", api):
        ci.review_findings(7, "abcdef1", [finding()])
    assert api.writes == []


def test_a_finding_that_disappears_is_marked_resolved_once():
    api = Api(comments=[comment("bandit|B602|scripts/shell_example.py|9", number=5)])
    with patch.object(ci, "api", api):
        ci.review_findings(7, "abcdef1", [])
    [(endpoint, method, payload)] = api.writes
    assert endpoint.endswith("/pulls/7/comments/5/replies") and "No longer reported on `abcdef1`" in payload["body"]
    api = Api(
        comments=[
            comment("bandit|B602|scripts/shell_example.py|9", number=5),
            comment("", number=6, body=f"{ci.RESOLVED_MARKER}\nresolved", in_reply_to=5),
        ]
    )
    with patch.object(ci, "api", api):
        ci.review_findings(7, "abcdef1", [])
    assert api.writes == []


def test_comments_from_other_people_are_ignored():
    api = Api(comments=[comment("bandit|B602|scripts/shell_example.py|9", user="someone")])
    with patch.object(ci, "api", api):
        ci.review_findings(7, "abcdef1", [finding()])
    assert [endpoint for endpoint, _, _ in api.writes] == ["repos/owner/cloud/pulls/7/comments"]
