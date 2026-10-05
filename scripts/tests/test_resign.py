# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Unsigned pushes to a pull request branch are re-signed in place, once."""

import os
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

import ci_automation as ci

WORKFLOWS = Path(__file__).resolve().parents[2] / ".github/workflows"


def pull(sha="a" * 40, repo="owner/cloud", base="main", ref="feature/x", number=9):
    return {
        "number": number,
        "state": "open",
        "head": {"sha": sha, "ref": ref, "repo": {"full_name": repo}},
        "base": {"ref": base},
    }


def commit(verified):
    return {"sha": ("b" if verified else "c") * 40, "commit": {"verification": {"verified": verified}}}


@pytest.fixture
def env(tmp_path):
    output = tmp_path / "output"
    with patch.dict(
        os.environ,
        GH_REPO="owner/cloud",
        SIGNAL_SHA="a" * 40,
        SIGNAL_BRANCH="feature/x",
        BASE_BRANCH="main",
        LABELLER="goabonga",
        GITHUB_OUTPUT=str(output),
    ):
        yield output


def run_validation(pulls, commits, permission="admin"):
    def respond(endpoint, *args, **kwargs):
        if "/collaborators/" in endpoint:
            return {"permission": permission}
        if endpoint.endswith("/commits?per_page=100"):
            return [commits]
        return [pulls]

    with patch.object(ci, "api", side_effect=respond) as client:
        ci.validate_resign()
    return client


def outputs(path):
    return dict(line.split("=", 1) for line in path.read_text().splitlines())


def test_unsigned_commits_pushed_by_a_writer_are_re_signed(env):
    client = run_validation([pull()], [commit(True), commit(False)])
    assert outputs(env) == {"number": "9", "needed": "true"}
    assert any("/collaborators/goabonga/permission" in call.args[0] for call in client.call_args_list)


def test_a_fully_signed_branch_needs_nothing_and_cannot_loop(env):
    client = run_validation([pull()], [commit(True), commit(True)])
    assert outputs(env) == {"number": "9", "needed": "false"}
    assert not any("/collaborators/" in call.args[0] for call in client.call_args_list)


@pytest.mark.parametrize(
    "pulls",
    [
        [pull(sha="d" * 40)],
        [pull(repo="fork/cloud")],
        [pull(base="develop")],
        [],
    ],
)
def test_stale_forked_or_other_base_pull_requests_are_left_alone(env, pulls):
    run_validation(pulls, [commit(False)])
    assert outputs(env)["needed"] == "false"


def test_a_pusher_without_write_access_is_refused(env):
    with pytest.raises(ValueError, match="write access"):
        run_validation([pull()], [commit(False)], permission="read")


def test_re_signing_keeps_the_branch_base_and_order():
    calls = []
    with (
        patch.dict(os.environ, BASE_BRANCH="main"),
        patch.object(
            ci, "git", side_effect=lambda *args: calls.append(args) or ("e" * 40 if args[0] == "merge-base" else "")
        ),
        patch.object(ci.subprocess, "run") as run,
    ):
        ci.rebase("resign")
    assert ("merge-base", "origin/main", "HEAD") in calls
    assert not any(args[:1] == ("fetch",) for args in calls)
    command = run.call_args.args[0]
    assert command[:5] == ["git", "rebase", "--force-rebase", "--empty=keep", "e" * 40]
    assert "sign_commit.py" in command[-1]


def test_push_re_checks_the_head_then_comments_as_the_maintainer():
    with (
        patch.dict(
            os.environ,
            GH_REPO="owner/cloud",
            PR_NUMBER="9",
            HEAD_REF="feature/x",
            EXPECTED_SHA="a" * 40,
            BASE_BRANCH="main",
            LABELLER="goabonga",
            PR_WRITE_TOKEN="maintainer-pat",
        ),
        patch.object(ci, "pull_request", return_value=pull()),
        patch.object(ci, "check_labeller") as labeller,
        patch.object(ci, "authenticated_git") as push,
        patch.object(ci, "git", return_value="f00ba12"),
        patch.object(ci, "api") as client,
    ):
        ci.push_signed("resign")
    labeller.assert_called_once_with()
    push.assert_called_once_with(
        "push", f"--force-with-lease=refs/heads/feature/x:{'a' * 40}", "origin", "HEAD:refs/heads/feature/x"
    )
    endpoint, method, payload = client.call_args.args
    assert (endpoint, method) == ("repos/owner/cloud/issues/9/comments", "POST")
    assert "after an unsigned push by `goabonga`" in payload["body"] and "`f00ba12`" in payload["body"]
    assert client.call_args.kwargs["token"] == "maintainer-pat"


def test_push_refuses_a_branch_that_moved_meanwhile():
    with (
        patch.dict(
            os.environ,
            GH_REPO="owner/cloud",
            PR_NUMBER="9",
            HEAD_REF="feature/x",
            EXPECTED_SHA="a" * 40,
            BASE_BRANCH="main",
            LABELLER="goabonga",
        ),
        patch.object(ci, "pull_request", return_value=pull(sha="d" * 40)),
        patch.object(ci, "authenticated_git") as push,
    ):
        with pytest.raises(ValueError, match="changed while its commits were re-signed"):
            ci.push_signed("resign")
    push.assert_not_called()


def test_workflows_leave_dependabot_pushes_to_the_rewrite_and_run_trusted_code():
    signal = (WORKFLOWS / "resign-signal.yml").read_text()
    assert "github.actor != 'dependabot[bot]'" in signal and "secrets." not in signal
    resign = (WORKFLOWS / "resign.yml").read_text()
    assert "workflows: [resign-signal]" in resign
    assert "ref: ${{ github.event.repository.default_branch }}" in resign
    assert "rebase --mode resign" in resign and "push --mode resign" in resign
    assert resign.count("if: steps.pr.outputs.needed == 'true'") == 4
