# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Checks must exist, pass and still correspond to the authorized PR head."""

import pytest
import os
import json
from pathlib import Path
import sys
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import ci_automation as ci


def test_explicit_api_token_is_passed_only_to_the_subprocess_environment():
    with patch.dict(os.environ, GH_TOKEN="merge-token"), patch.object(ci, "run", return_value="{}") as command:
        assert ci.api("repos/owner/cloud/commits/head/status", token="checks-token") == {}
        assert command.call_args.kwargs["env"]["GH_TOKEN"] == "checks-token"
        assert "checks-token" not in str(command.call_args.args)
        assert os.environ["GH_TOKEN"] == "merge-token"
    with pytest.raises(ValueError, match="must not be empty"):
        ci.api("unused", token="")


@pytest.mark.parametrize("signed", [False, True])
def test_ci_reads_use_the_workflow_token_before_and_after_signing(signed):
    sha = "a" * 40
    with patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA=sha, CHECKS_TOKEN="checks-token"), \
         patch.object(ci, "pull_request", return_value={}), \
         patch.object(ci, "merge_authorized", return_value=True), \
         patch.object(ci, "git", return_value=sha), \
         patch.object(ci, "required_checks", return_value={"validate"}), \
         patch.object(ci, "api", side_effect=[[{"check_runs": [check()]}], {"statuses": []}]) as client:
        ci.wait_checks(signed)
        assert client.call_count == 2
        for call in client.call_args_list:
            assert call.kwargs["token"] == "checks-token"
            assert sha in call.args[0]


def test_signed_wait_tolerates_the_pull_request_head_lagging_behind_the_push():
    original, signed = "a" * 40, "b" * 40
    stale = {"head": {"sha": original}}
    current = {"head": {"sha": signed}}
    with patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA=original, CHECKS_TOKEN="checks-token"), \
         patch.object(ci, "pull_request", side_effect=[stale, current]), \
         patch.object(ci, "merge_authorized", return_value=True) as authorized, \
         patch.object(ci, "git", return_value=signed), \
         patch.object(ci, "required_checks", return_value={"validate"}), \
         patch.object(ci.time, "sleep") as sleep, \
         patch.object(ci, "api", side_effect=[[{"check_runs": [check()]}], {"statuses": []}]):
        ci.wait_checks(signed=True)
    sleep.assert_called_once()
    authorized.assert_called_once_with(current, signed)


def test_signed_wait_rejects_an_unexpected_pull_request_head():
    with patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA="a" * 40, CHECKS_TOKEN="checks-token"), \
         patch.object(ci, "pull_request", return_value={"head": {"sha": "c" * 40}}), \
         patch.object(ci, "merge_authorized", return_value=False), \
         patch.object(ci, "git", return_value="b" * 40):
        with pytest.raises(ValueError, match="Pull request changed"):
            ci.wait_checks(signed=True)


def test_ci_read_token_is_required_without_a_pat_fallback():
    with patch.dict(os.environ, GH_TOKEN="merge-token", CHECKS_TOKEN=""):
        with pytest.raises(ValueError, match="CHECKS_TOKEN is required"):
            ci.wait_checks()


def test_consecutive_paginated_documents_are_all_decoded():
    with patch.object(
        ci, "run", return_value='[{"number": 1}]\n[{"number": 2}]'
    ) as command:
        assert ci.api("repos/owner/cloud/issues", paginate=True) == [
            [{"number": 1}],
            [{"number": 2}],
        ]
        assert "--slurp" not in command.call_args.args


def test_truncated_page_is_an_error_instead_of_partial_results():
    with patch.object(ci, "run", return_value='[{"number": 1}]\n[{'):
        with pytest.raises(json.JSONDecodeError):
            ci.api("repos/owner/cloud/issues", paginate=True)


def check(
    number=1,
    state="completed",
    conclusion="success",
    name="validate",
):
    return {
        "id": number,
        "status": state,
        "conclusion": conclusion,
        "name": name,
        "app": {"id": 10},
    }


def test_missing_or_skipped_ci_cannot_merge():
    assert not ci.checks_ready([], {"statuses": []})
    assert not ci.checks_ready([check(conclusion="skipped")], {"statuses": []})


def test_required_checks_follow_the_repository_ruleset():
    required = ci.required_checks()
    assert {"audit", "check commit signatures", "detect changed components"} <= required
    assert "validate" not in required


def test_merge_waits_for_every_required_check_and_accepts_skipped_ones():
    required = {"audit", "validate go"}
    assert not ci.checks_ready([check(name="audit")], {"statuses": []}, required)
    assert ci.checks_ready(
        [check(name="audit"), check(2, conclusion="skipped", name="validate go")], {"statuses": []}, required
    )
    assert not ci.checks_ready(
        [check(conclusion="skipped", name="audit"), check(2, conclusion="skipped", name="validate go")],
        {"statuses": []}, required,
    )


def test_latest_success_supersedes_failed_attempt():
    assert ci.checks_ready(
        [check(conclusion="failure"), check(2)], {"statuses": []}, {"validate"}
    )


def test_pending_or_failed_checks_block_merge():
    assert not ci.checks_ready(
        [check(state="in_progress", conclusion=None)], {"statuses": []}
    )
    with pytest.raises(ValueError):
        ci.checks_ready([check(conclusion="failure")], {"statuses": []})
    with pytest.raises(ValueError):
        ci.checks_ready([check()], {"statuses": [{}], "state": "failure"})


def test_removed_label_fork_or_changed_head_revokes_authorization():
    pr = {
        "state": "open",
        "draft": False,
        "head": {
            "repo": {"full_name": "owner/cloud"},
            "sha": "a" * 40,
            "ref": "feature",
        },
        "base": {"ref": "main"},
        "labels": [{"name": "auto-merge"}],
    }
    with patch.dict(
        os.environ,
        GH_REPO="owner/cloud",
        HEAD_REF="feature",
        MERGE_LABEL="auto-merge",
    ):
        assert ci.merge_authorized(pr, "a" * 40)
        assert not ci.merge_authorized(pr, "b" * 40)
        pr["labels"] = []
        assert not ci.merge_authorized(pr, "a" * 40)
        pr["labels"] = [{"name": "auto-merge"}]
        pr["head"]["repo"] = {"full_name": "fork/cloud"}
        assert not ci.merge_authorized(pr, "a" * 40)


def test_merge_comment_uses_the_workflow_token(tmp_path):
    sha = "a" * 40
    (tmp_path / "merge-base-sha").write_text("b" * 40)
    with patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", RUNNER_TEMP=str(tmp_path),
                    PR_WRITE_TOKEN="workflow-token"), \
         patch.object(ci, "git", side_effect=[sha, "", "b" * 40]), \
         patch.object(ci, "pull_request", return_value={}), \
         patch.object(ci, "merge_authorized", return_value=True), \
         patch.object(ci, "check_labeller"), \
         patch.object(ci, "authenticated_git"), \
         patch.object(ci, "api") as client:
        ci.merge()
    assert client.call_args.args[:2] == ("repos/owner/cloud/issues/7/comments", "POST")
    assert client.call_args.kwargs["token"] == "workflow-token"


def test_pull_request_writes_require_the_workflow_token():
    with patch.dict(os.environ, PR_WRITE_TOKEN=""):
        with pytest.raises(ValueError, match="PR_WRITE_TOKEN is required"):
            ci.pr_write_token()


def test_merge_failure_reports_and_unlabels_with_the_workflow_token():
    with patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", MERGE_LABEL="auto-merge",
                    PR_WRITE_TOKEN="workflow-token"), \
         patch.object(sys, "argv", ["ci_automation.py", "merge", "--mode", "merge"]), \
         patch.object(ci, "merge", side_effect=ValueError("Main advanced")), \
         patch.object(ci, "api") as client:
        with pytest.raises(ValueError, match="Main advanced"):
            ci.main()
    calls = [(call.args[0], call.args[1], call.kwargs["token"]) for call in client.call_args_list]
    assert calls == [("repos/owner/cloud/issues/7/comments", "POST", "workflow-token"),
                     ("repos/owner/cloud/issues/7/labels/auto-merge", "DELETE", "workflow-token")]


def labelled(number, at, name="auto-merge"):
    return {"event": "labeled", "created_at": at, "label": {"name": name}, "number": number}


def queue_api(queue):
    """Serve open labelled issues and their label events; ``queue`` maps PR numbers to label times."""
    def respond(endpoint, *args, **kwargs):
        if "/events" in endpoint:
            number = int(endpoint.split("/issues/")[1].split("/")[0])
            return [[labelled(number, at) for at in queue[number]] + [labelled(number, "2026-01-01T00:00:00Z", "other")]]
        return [[{"number": number, "pull_request": {}} for number in queue] + [{"number": 99}]]
    return respond


def test_queue_orders_pull_requests_by_their_latest_auto_merge_label():
    queue = {5: ["2026-10-06T10:00:02Z"], 6: ["2026-10-06T09:00:00Z", "2026-10-06T10:00:03Z"], 7: ["2026-10-06T10:00:01Z"]}
    with patch.dict(os.environ, GH_REPO="owner/cloud", MERGE_LABEL="auto-merge"), \
         patch.object(ci, "api", side_effect=queue_api(queue)):
        assert ci.queue_ahead(7) == []
        assert ci.queue_ahead(5) == [7]
        assert ci.queue_ahead(6) == [7, 5]


def test_queue_waits_for_earlier_pull_requests_then_for_the_pending_release(capsys):
    with patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", HEAD_SHA="a" * 40, QUEUE_DIR="queue"), \
         patch.object(ci, "pull_request", return_value={}), \
         patch.object(ci, "merge_authorized", return_value=True), \
         patch.object(ci, "queue_ahead", side_effect=[[5], [], []]), \
         patch.object(ci, "pending_release", side_effect=[["cloud", "cloud-docs"], []]) as pending, \
         patch.object(ci.time, "sleep") as sleep:
        ci.wait_turn()
    output = capsys.readouterr().out
    assert "Waiting for #5 ahead in the merge queue" in output
    assert "Waiting for the release of cloud, cloud-docs on main" in output
    assert pending.call_count == 2 and sleep.call_count == 2


def test_queue_stops_when_the_label_is_removed():
    with patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", HEAD_SHA="a" * 40, QUEUE_DIR="queue"), \
         patch.object(ci, "pull_request", return_value={}), \
         patch.object(ci, "merge_authorized", return_value=False):
        with pytest.raises(ValueError, match="merge authorization was removed"):
            ci.wait_turn()


def test_queue_times_out_without_holding_the_job_forever():
    with patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", HEAD_SHA="a" * 40, QUEUE_DIR="queue"), \
         patch.object(ci, "pull_request", return_value={}), \
         patch.object(ci, "merge_authorized", return_value=True), \
         patch.object(ci, "queue_ahead", return_value=[5]), \
         patch.object(ci.time, "sleep"):
        with pytest.raises(TimeoutError, match="merge queue"):
            ci.wait_turn(limit=0)


def test_pending_release_reads_the_multicz_plan_of_the_latest_main(tmp_path):
    with patch.object(ci, "authenticated_git") as fetch, \
         patch.object(ci, "git") as checkout, \
         patch.object(ci, "run", return_value='{"bumps": {"cloud-docs": {}, "cloud": {}}}') as plan:
        assert ci.pending_release(tmp_path) == ["cloud", "cloud-docs"]
    fetch.assert_called_once_with("-C", str(tmp_path), "fetch", "--quiet", "--tags", "--force", "origin", "main")
    checkout.assert_called_once_with("-C", str(tmp_path), "checkout", "--quiet", "--detach", "FETCH_HEAD")
    plan.assert_called_once_with("multicz", "plan", "--output", "json", cwd=tmp_path)


def dependabot_pr(number=12, sha="a" * 40, labels=(), user="dependabot[bot]", base="main"):
    return {"number": number, "state": "open", "user": {"login": user}, "labels": [{"name": n} for n in labels],
            "head": {"sha": sha, "ref": "dependabot/go_modules/cobra", "repo": {"full_name": "owner/cloud"}},
            "base": {"ref": base}}


def pr_commit(verified=True, message="fix(deps): bump cobra\n\nupdate-type: version-update:semver-minor"):
    return {"commit": {"message": message, "verification": {"verified": verified}}}


def dependabot_api(pr, commits, comments=()):
    """Route the GitHub API calls of label_dependabot; record the writes."""
    writes = []

    def respond(endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            writes.append((endpoint, method, payload, token))
            return {}
        if endpoint.endswith("/commits?per_page=100"):
            return [list(commits)]
        if "/comments" in endpoint:
            return [list(comments)]
        return [[pr]]
    return respond, writes


@pytest.fixture
def dependabot_env():
    with patch.dict(os.environ, GH_REPO="owner/cloud", MERGE_LABEL="auto-merge", PR_WRITE_TOKEN="workflow-token",
                    RUN_HEAD_SHA="a" * 40, RUN_HEAD_BRANCH="dependabot/go_modules/cobra"):
        yield


def test_dependabot_minor_update_is_labelled_with_the_merge_pat(dependabot_env):
    respond, writes = dependabot_api(dependabot_pr(), [pr_commit(), pr_commit()])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    assert writes == [("repos/owner/cloud/issues/12/labels", "POST", {"labels": ["auto-merge"]}, None)]


@pytest.mark.parametrize("pr,commits", [
    (dependabot_pr(sha="b" * 40), [pr_commit()]),
    (dependabot_pr(labels=["auto-merge"]), [pr_commit()]),
    (dependabot_pr(), [pr_commit(), pr_commit(verified=False)]),
    (dependabot_pr(), []),
    (dependabot_pr(user="someone"), [pr_commit()]),
    (dependabot_pr(base="develop"), [pr_commit()]),
])
def test_dependabot_pull_requests_not_ready_are_left_alone(dependabot_env, pr, commits):
    respond, writes = dependabot_api(pr, commits)
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    assert writes == []


def test_dependabot_major_update_waits_for_approval_with_a_single_notice(dependabot_env):
    major = pr_commit(message="ci: bump setup-go\n\nupdate-type: version-update:semver-major")
    respond, writes = dependabot_api(dependabot_pr(), [pr_commit(), major])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    [(endpoint, method, payload, token)] = writes
    assert (endpoint, method, token) == ("repos/owner/cloud/issues/12/comments", "POST", "workflow-token")
    assert ci.MAJOR_NOTICE in payload["body"] and "add the `auto-merge` label to merge it" in payload["body"]

    respond, writes = dependabot_api(dependabot_pr(), [major], comments=[{"body": payload["body"]}])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    assert writes == []


def test_error_lines_keep_failures_without_timestamps_or_repeats():
    log = "\n".join([
        "2026-10-06T10:00:00.1234567Z Run go test ./...",
        "2026-10-06T10:00:01.0000000Z --- FAIL: TestRun (0.00s)",
        "2026-10-06T10:00:01.0000000Z FAILED scripts/tests/test_x.py::test_y - AssertionError",
        "2026-10-06T10:00:01.0000000Z E   assert 1 == 2",
        "2026-10-06T10:00:02.0000000Z ##[error]Process completed with exit code 1.",
        "2026-10-06T10:00:02.0000000Z ##[error]Process completed with exit code 1.",
        "2026-10-06T10:00:03.0000000Z ok  example.test/pkg",
    ])
    assert ci.error_lines(log) == [
        "--- FAIL: TestRun (0.00s)",
        "FAILED scripts/tests/test_x.py::test_y - AssertionError",
        "E   assert 1 == 2",
        "Process completed with exit code 1.",
    ]
    assert ci.error_lines(log, limit=1) == ["Process completed with exit code 1."]


def ci_job(name, conclusion, number=1):
    return {"id": number, "name": name, "status": "completed", "conclusion": conclusion,
            "html_url": f"https://example.test/job/{number}",
            "steps": [{"name": "Checkout", "conclusion": "success"}, {"name": "Run tests", "conclusion": conclusion}]}


def job_comment(name, text, number=50, user="github-actions[bot]"):
    return {"id": number, "user": {"login": user}, "body": f"<!-- ci-job: {name} -->\n{text}"}


def report_api(jobs, comments):
    writes = []

    def respond(endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            writes.append((endpoint, method, payload["body"]))
            return {}
        if "/jobs?" in endpoint:
            return [{"jobs": jobs}]
        return [comments]
    return respond, writes


@pytest.fixture
def report_env():
    with patch.dict(os.environ, GH_REPO="owner/cloud", PR_NUMBER="7", HEAD_SHA="abcdef1234" + "0" * 30,
                    RUN_ID="99", RUN_ATTEMPT="1", REPORT_JOB="report job failures"):
        yield


def test_failed_job_gets_its_own_comment_with_step_errors_and_link(report_env):
    jobs = [ci_job("validate go", "failure", 3), ci_job("audit", "success", 4),
            ci_job("report job failures", "failure", 5), ci_job("validate scripts", "skipped", 6)]
    respond, writes = report_api(jobs, [])
    log = "2026-10-06T10:00:01.0000000Z --- FAIL: TestRun (0.00s)\n"
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "run", return_value=log) as logs:
        ci.report_jobs()
    logs.assert_called_once_with("gh", "api", "repos/owner/cloud/actions/jobs/3/logs")
    [(endpoint, method, body)] = writes
    assert (endpoint, method) == ("repos/owner/cloud/issues/7/comments", "POST")
    assert body.startswith("<!-- ci-job: validate go -->\n**CI job `validate go` failed** on `abcdef1`")
    assert "in step `Run tests`" in body and "--- FAIL: TestRun" in body
    assert "[Job logs](https://example.test/job/3)" in body


def test_a_new_failure_edits_the_existing_comment_instead_of_adding_one(report_env):
    respond, writes = report_api([ci_job("validate go", "failure", 3)],
                                 [job_comment("validate go", "**CI job `validate go` failed** on `0000000`.")])
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "run", return_value=""):
        ci.report_jobs()
    [(endpoint, method, body)] = writes
    assert (endpoint, method) == ("repos/owner/cloud/issues/comments/50", "PATCH")
    assert "`abcdef1`" in body and "```" not in body


def test_a_job_passing_again_resolves_its_comment_once(report_env):
    respond, writes = report_api([ci_job("validate go", "success", 3)],
                                 [job_comment("validate go", "**CI job `validate go` failed** on `0000000`.")])
    with patch.object(ci, "api", side_effect=respond):
        ci.report_jobs()
    [(endpoint, method, body)] = writes
    assert (endpoint, method) == ("repos/owner/cloud/issues/comments/50", "PATCH")
    assert "**CI job `validate go` passes again** on `abcdef1`" in body

    respond, writes = report_api([ci_job("validate go", "success", 3)], [job_comment("validate go", body.split("\n", 1)[1])])
    with patch.object(ci, "api", side_effect=respond):
        ci.report_jobs()
    assert writes == []


def test_passing_jobs_without_a_failure_and_foreign_comments_stay_untouched(report_env):
    respond, writes = report_api([ci_job("validate go", "success", 3), ci_job("audit", "cancelled", 4)],
                                 [job_comment("validate go", "spoofed failure", user="someone")])
    with patch.object(ci, "api", side_effect=respond):
        ci.report_jobs()
    assert writes == []
