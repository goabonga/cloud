# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Checks must exist, pass and still correspond to the authorized PR head."""

import base64
import pytest
import os
import json
from pathlib import Path
import re
import subprocess
import sys
from unittest.mock import Mock, patch

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
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA=sha, CHECKS_TOKEN="checks-token"),
        patch.object(ci, "pull_request", return_value={}),
        patch.object(ci, "merge_authorized", return_value=True),
        patch.object(ci, "git", return_value=sha),
        patch.object(ci, "required_checks", return_value={"validate"}),
        patch.object(ci, "api", side_effect=[[{"check_runs": [check()]}], {"statuses": []}]) as client,
    ):
        ci.wait_checks(signed)
        assert client.call_count == 2
        for call in client.call_args_list:
            assert call.kwargs["token"] == "checks-token"
            assert sha in call.args[0]


def test_signed_wait_tolerates_the_pull_request_head_lagging_behind_the_push():
    original, signed = "a" * 40, "b" * 40
    stale = {"head": {"sha": original}}
    current = {"head": {"sha": signed}}
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA=original, CHECKS_TOKEN="checks-token"),
        patch.object(ci, "pull_request", side_effect=[stale, current]),
        patch.object(ci, "merge_authorized", return_value=True) as authorized,
        patch.object(ci, "git", return_value=signed),
        patch.object(ci, "required_checks", return_value={"validate"}),
        patch.object(ci.time, "sleep") as sleep,
        patch.object(ci, "api", side_effect=[[{"check_runs": [check()]}], {"statuses": []}]),
    ):
        ci.wait_checks(signed=True)
    sleep.assert_called_once()
    authorized.assert_called_once_with(current, signed)


def test_signed_wait_rejects_an_unexpected_pull_request_head():
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA="a" * 40, CHECKS_TOKEN="checks-token"),
        patch.object(ci, "pull_request", return_value={"head": {"sha": "c" * 40}}),
        patch.object(ci, "merge_authorized", return_value=False),
        patch.object(ci, "git", return_value="b" * 40),
    ):
        with pytest.raises(ValueError, match="Pull request changed"):
            ci.wait_checks(signed=True)


def test_ci_read_token_is_required_without_a_pat_fallback():
    with patch.dict(os.environ, GH_TOKEN="merge-token", CHECKS_TOKEN=""):
        with pytest.raises(ValueError, match="CHECKS_TOKEN is required"):
            ci.wait_checks()


def test_consecutive_paginated_documents_are_all_decoded():
    with patch.object(ci, "run", return_value='[{"number": 1}]\n[{"number": 2}]') as command:
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
        {"statuses": []},
        required,
    )


def test_latest_success_supersedes_failed_attempt():
    assert ci.checks_ready([check(conclusion="failure"), check(2)], {"statuses": []}, {"validate"})


def test_pending_or_failed_checks_block_merge():
    assert not ci.checks_ready([check(state="in_progress", conclusion=None)], {"statuses": []})
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
    with (
        patch.dict(
            os.environ, GH_REPO="owner/cloud", PR="7", RUNNER_TEMP=str(tmp_path), PR_WRITE_TOKEN="workflow-token"
        ),
        patch.object(ci, "git", side_effect=[sha, "", "b" * 40]),
        patch.object(ci, "pull_request", return_value={"head": {"ref": "feature"}, "base": {"ref": "main"}}),
        patch.object(ci, "merge_authorized", return_value=True),
        patch.object(ci, "check_labeller"),
        patch.object(ci, "authenticated_git"),
        patch.object(ci, "api") as client,
    ):
        ci.merge()
    assert client.call_args.args[:2] == ("repos/owner/cloud/issues/7/comments", "POST")
    assert client.call_args.kwargs["token"] == "workflow-token"


def test_pull_request_writes_require_the_workflow_token():
    with patch.dict(os.environ, PR_WRITE_TOKEN=""):
        with pytest.raises(ValueError, match="PR_WRITE_TOKEN is required"):
            ci.pr_write_token()


def test_merge_failure_reports_and_unlabels_with_the_workflow_token():
    with (
        patch.dict(
            os.environ, GH_REPO="owner/cloud", PR="7", MERGE_LABEL="auto-merge", PR_WRITE_TOKEN="workflow-token"
        ),
        patch.object(sys, "argv", ["ci_automation.py", "merge", "--mode", "merge"]),
        patch.object(ci, "merge", side_effect=ValueError("Main advanced")),
        patch.object(ci, "api") as client,
    ):
        with pytest.raises(SystemExit) as exit_code:
            ci.main()
    assert exit_code.value.code == 1
    calls = [(call.args[0], call.args[1], call.kwargs["token"]) for call in client.call_args_list]
    assert calls == [
        ("repos/owner/cloud/issues/7/comments", "POST", "workflow-token"),
        ("repos/owner/cloud/issues/7/labels/auto-merge", "DELETE", "workflow-token"),
    ]


def labelled(number, at, name="auto-merge"):
    return {"event": "labeled", "created_at": at, "label": {"name": name}, "number": number}


def queue_api(queue):
    """Serve open labelled issues and their label events; ``queue`` maps PR numbers to label times."""

    def respond(endpoint, *args, **kwargs):
        if "/events" in endpoint:
            number = int(endpoint.split("/issues/")[1].split("/")[0])
            return [
                [labelled(number, at) for at in queue[number]] + [labelled(number, "2026-01-01T00:00:00Z", "other")]
            ]
        return [[{"number": number, "pull_request": {}} for number in queue] + [{"number": 99}]]

    return respond


def test_queue_orders_pull_requests_by_their_latest_auto_merge_label():
    queue = {
        5: ["2026-10-06T10:00:02Z"],
        6: ["2026-10-06T09:00:00Z", "2026-10-06T10:00:03Z"],
        7: ["2026-10-06T10:00:01Z"],
    }
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", MERGE_LABEL="auto-merge"),
        patch.object(ci, "api", side_effect=queue_api(queue)),
    ):
        assert ci.queue_ahead(7) == []
        assert ci.queue_ahead(5) == [7]
        assert ci.queue_ahead(6) == [7, 5]


def test_queue_waits_for_earlier_pull_requests_then_for_the_pending_release(capsys):
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", HEAD_SHA="a" * 40, QUEUE_DIR="queue"),
        patch.object(ci, "pull_request", return_value={}),
        patch.object(ci, "merge_authorized", return_value=True),
        patch.object(ci, "queue_ahead", side_effect=[[5], [], []]),
        patch.object(ci, "pending_release", side_effect=[["cloud", "cloud-docs"], []]) as pending,
        patch.object(ci.time, "sleep") as sleep,
    ):
        ci.wait_turn()
    output = capsys.readouterr().out
    assert "Waiting for #5 ahead in the merge queue" in output
    assert "Waiting for the release of cloud, cloud-docs on main" in output
    assert pending.call_count == 2 and sleep.call_count == 2


def test_queue_stops_when_the_label_is_removed():
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", HEAD_SHA="a" * 40, QUEUE_DIR="queue"),
        patch.object(ci, "pull_request", return_value={}),
        patch.object(ci, "merge_authorized", return_value=False),
    ):
        with pytest.raises(ValueError, match="merge authorization was removed"):
            ci.wait_turn()


def test_queue_times_out_without_holding_the_job_forever():
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", PR="7", HEAD_SHA="a" * 40, QUEUE_DIR="queue"),
        patch.object(ci, "pull_request", return_value={}),
        patch.object(ci, "merge_authorized", return_value=True),
        patch.object(ci, "queue_ahead", return_value=[5]),
        patch.object(ci.time, "sleep"),
    ):
        with pytest.raises(TimeoutError, match="merge queue"):
            ci.wait_turn(limit=0)


def test_pending_release_reads_the_multicz_plan_of_the_latest_main(tmp_path):
    with (
        patch.object(ci, "authenticated_git") as fetch,
        patch.object(ci, "git") as checkout,
        patch.object(ci, "run", return_value='{"bumps": {"cloud-docs": {}, "cloud": {}}}') as plan,
    ):
        assert ci.pending_release(tmp_path) == ["cloud", "cloud-docs"]
    fetch.assert_called_once_with("-C", str(tmp_path), "fetch", "--quiet", "--tags", "--force", "origin", "main")
    checkout.assert_called_once_with("-C", str(tmp_path), "checkout", "--quiet", "--detach", "FETCH_HEAD")
    plan.assert_called_once_with("multicz", "plan", "--output", "json", cwd=tmp_path)


def dependabot_pr(number=12, sha="a" * 40, labels=(), user="dependabot[bot]", base="main"):
    return {
        "number": number,
        "state": "open",
        "user": {"login": user},
        "labels": [{"name": n} for n in labels],
        "head": {"sha": sha, "ref": "dependabot/go_modules/cobra", "repo": {"full_name": "owner/cloud"}},
        "base": {"ref": base},
    }


def pr_commit(verified=True, message="fix(deps): bump cobra\n\nupdate-type: version-update:semver-minor"):
    return {"commit": {"message": message, "verification": {"verified": verified}}}


def dependabot_api(pr, commits, comments=()):
    """Route the GitHub API calls of label_dependabot; record the writes."""
    writes = []

    def respond(endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            writes.append((endpoint, method, payload, token))
            return {}
        if endpoint == "user":
            return {"login": "goabonga"}
        if endpoint.endswith("/commits?per_page=100"):
            return [list(commits)]
        if "/comments" in endpoint:
            return [list(comments)]
        return [[pr]]

    return respond, writes


@pytest.fixture
def dependabot_env():
    with patch.dict(
        os.environ,
        GH_REPO="owner/cloud",
        MERGE_LABEL="auto-merge",
        PR_WRITE_TOKEN="workflow-token",
        RUN_HEAD_SHA="a" * 40,
        RUN_HEAD_BRANCH="dependabot/go_modules/cobra",
    ):
        yield


def test_dependabot_major_announced_in_the_pull_request_waits_for_a_label(dependabot_env):
    pr = dependabot_pr()
    pr["body"] = "Updates `actions/setup-go` from 6.5.0 to 7.0.0"
    respond, writes = dependabot_api(pr, [pr_commit(message="ci: bump `actions/setup-go` from 6.5.0 to 7.0.0")])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    (endpoint, method, payload, token), *assignments = writes
    assert endpoint.endswith("/issues/12/comments") and ci.MAJOR_NOTICE in payload["body"]
    assert [write[0] for write in assignments] == ["repos/owner/cloud/issues/12/assignees"]


def test_dependabot_minor_update_is_labelled_with_the_merge_pat(dependabot_env):
    respond, writes = dependabot_api(dependabot_pr(), [pr_commit(), pr_commit()])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    assert writes == [("repos/owner/cloud/issues/12/labels", "POST", {"labels": ["auto-merge"]}, None)]


@pytest.mark.parametrize(
    "pr,commits",
    [
        (dependabot_pr(sha="b" * 40), [pr_commit()]),
        (dependabot_pr(labels=["auto-merge"]), [pr_commit()]),
        (dependabot_pr(), [pr_commit(), pr_commit(verified=False)]),
        (dependabot_pr(), []),
        (dependabot_pr(user="someone"), [pr_commit()]),
        (dependabot_pr(base="develop"), [pr_commit()]),
    ],
)
def test_dependabot_pull_requests_not_ready_are_left_alone(dependabot_env, pr, commits):
    respond, writes = dependabot_api(pr, commits)
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    assert writes == []


@pytest.mark.parametrize(
    "body,expected",
    [
        (
            "Updates `actions/setup-go` from 6.5.0 to 7.0.0\nUpdates `getplumber/plumber` from 0.5.15 to 0.5.20",
            ["actions/setup-go"],
        ),
        ("Bumps [actions/checkout](https://github.com/actions/checkout) from v6 to v7.", ["actions/checkout"]),
        ("Updates `github.com/spf13/cobra` from 1.9.1 to 1.10.2", []),
        ("Bumps the group with 1 update.", []),
    ],
)
def test_major_updates_compare_leading_version_numbers(body, expected):
    assert ci.major_updates(body) == expected


def test_dependabot_major_update_waits_for_approval_with_a_single_notice(dependabot_env):
    major = pr_commit(message="ci: bump setup-go\n\nupdate-type: version-update:semver-major")
    pr = {
        **dependabot_pr(),
        "body": "Bumps setup-go.\n\nTracked by https://github.com/owner/cloud/issues/77.\n\nCloses #77",
    }
    respond, writes = dependabot_api(pr, [pr_commit(), major])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    (endpoint, method, payload, token), *assignments = writes
    assert (endpoint, method, token) == ("repos/owner/cloud/issues/12/comments", "POST", "workflow-token")
    assert ci.MAJOR_NOTICE in payload["body"] and "add the `auto-merge` label to merge it" in payload["body"]
    # The maintainer has to act, on the pull request and its tracking issue.
    assert [(write[0], write[2]) for write in assignments] == [
        ("repos/owner/cloud/issues/12/assignees", {"assignees": ["goabonga"]}),
        ("repos/owner/cloud/issues/77/assignees", {"assignees": ["goabonga"]}),
    ]

    respond, writes = dependabot_api(dependabot_pr(), [major], comments=[{"body": payload["body"]}])
    with patch.object(ci, "api", side_effect=respond):
        ci.label_dependabot()
    assert writes == []


def test_error_lines_keep_failures_without_timestamps_or_repeats():
    log = "\n".join(
        [
            "2026-10-06T10:00:00.1234567Z Run go test ./...",
            "2026-10-06T10:00:01.0000000Z --- FAIL: TestRun (0.00s)",
            "2026-10-06T10:00:01.0000000Z FAILED scripts/tests/test_x.py::test_y - AssertionError",
            "2026-10-06T10:00:01.0000000Z E   assert 1 == 2",
            "2026-10-06T10:00:02.0000000Z ##[error]Process completed with exit code 1.",
            "2026-10-06T10:00:02.0000000Z ##[error]Process completed with exit code 1.",
            "2026-10-06T10:00:03.0000000Z ok  example.test/pkg",
        ]
    )
    assert ci.error_lines(log) == [
        "--- FAIL: TestRun (0.00s)",
        "FAILED scripts/tests/test_x.py::test_y - AssertionError",
        "E   assert 1 == 2",
        "Process completed with exit code 1.",
    ]
    assert ci.error_lines(log, limit=1) == ["Process completed with exit code 1."]


def ci_job(name, conclusion, number=1):
    return {
        "id": number,
        "name": name,
        "status": "completed",
        "conclusion": conclusion,
        "html_url": f"https://example.test/job/{number}",
        "steps": [{"name": "Checkout", "conclusion": "success"}, {"name": "Run tests", "conclusion": conclusion}],
    }


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
    with patch.dict(
        os.environ,
        GH_REPO="owner/cloud",
        PR_NUMBER="7",
        HEAD_SHA="abcdef1234" + "0" * 30,
        RUN_ID="99",
        RUN_ATTEMPT="1",
        REPORT_JOB="report job failures",
    ):
        yield


def job_log(*reports, errors=()):
    lines = [f"2026-10-06T10:00:00.0000000Z {line}" for line in errors]
    lines += [
        "2026-10-06T10:00:01.0000000Z security-report: " + base64.b64encode(json.dumps(report).encode()).decode()
        for report in reports
    ]
    return "\n".join(lines) + "\n"


def test_every_security_tool_gets_its_own_comment(report_env):
    jobs = [
        ci_job("validate go", "failure", 3),
        ci_job("audit", "success", 4),
        ci_job("check commit signatures", "success", 7),
        ci_job("report job failures", "failure", 5),
        ci_job("validate scripts", "skipped", 6),
    ]
    respond, writes = report_api(jobs, [])
    logs = {
        3: job_log(
            {"tool": "go-mod-verify", "status": "passed", "summary": "all modules verified"},
            {
                "tool": "govulncheck",
                "status": "failed",
                "summary": "Your code is affected by 1 vulnerability",
                "findings": [
                    "Vulnerability #1: GO-2021-0113",
                    "#1: internal/cli/cli.go:40:23: cli.command calls language.Parse",
                ],
            },
            errors=["##[error]Process completed with exit code 2."],
        ),
        4: job_log(
            {"tool": "zizmor", "status": "passed", "summary": "No findings to report."},
            {"tool": "gitleaks", "status": "passed", "summary": "no leaks found"},
        ),
        7: job_log(),
    }
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "job_log", side_effect=lambda job: logs[job]):
        ci.report_jobs()
    bodies = [body for _, _, body in writes]
    assert [body.splitlines()[0] for body in bodies] == [
        "<!-- ci-check: validate go / go-mod-verify -->",
        "<!-- ci-check: validate go / govulncheck -->",
        "<!-- ci-check: audit / zizmor -->",
        "<!-- ci-check: audit / gitleaks -->",
        "<!-- ci-job: check commit signatures -->",
    ]
    assert (
        bodies[0].splitlines()[1]
        == "✅ **`go-mod-verify`** passed on `abcdef1` (job `validate go`): all modules verified"
    )
    assert bodies[1].splitlines()[1] == (
        "🟥 **`govulncheck`** failed on `abcdef1` (job `validate go`): Your code is affected by 1 vulnerability"
    )
    assert "Vulnerability #1: GO-2021-0113" in bodies[1] and "Process completed" not in bodies[1]
    assert bodies[1].endswith("[Job logs](https://example.test/job/3)")
    assert "```" not in bodies[2] and "✅ **`gitleaks`** passed" in bodies[3]
    assert bodies[4].splitlines()[1] == "✅ **CI job `check commit signatures` passed** on `abcdef1`."


def test_a_job_failing_outside_its_tools_gets_a_job_comment(report_env):
    respond, writes = report_api([ci_job("validate go", "failure", 3)], [])
    log = job_log(errors=["--- FAIL: TestRun (0.00s)"])
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "job_log", return_value=log):
        ci.report_jobs()
    [(_, _, body)] = writes
    assert body.startswith(
        "<!-- ci-job: validate go -->\n🟥 **CI job `validate go` failed** on `abcdef1`, in step `Run tests`."
    )
    assert "--- FAIL: TestRun" in body


def test_a_former_job_comment_is_marked_passed_once_its_tools_run(report_env):
    respond, writes = report_api(
        [ci_job("validate go", "success", 3)],
        [job_comment("validate go", "🟥 **CI job `validate go` failed** on `0000000`.")],
    )
    log = job_log({"tool": "govulncheck", "status": "passed", "summary": "No vulnerabilities found."})
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "job_log", return_value=log):
        ci.report_jobs()
    assert [(endpoint, method) for endpoint, method, _ in writes] == [
        ("repos/owner/cloud/issues/7/comments", "POST"),
        ("repos/owner/cloud/issues/comments/50", "PATCH"),
    ]
    assert "✅ **CI job `validate go` passed**" in writes[1][2]


def test_a_new_run_edits_the_job_comment_only_when_it_changes(report_env):
    respond, writes = report_api(
        [ci_job("validate go", "success", 3)],
        [job_comment("validate go", "**CI job `validate go` failed** on `0000000`.")],
    )
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "job_log", return_value=""):
        ci.report_jobs()
    [(endpoint, method, body)] = writes
    assert (endpoint, method) == ("repos/owner/cloud/issues/comments/50", "PATCH")
    assert "✅ **CI job `validate go` passed** on `abcdef1`." in body

    respond, writes = report_api(
        [ci_job("validate go", "success", 3)], [job_comment("validate go", body.split("\n", 1)[1])]
    )
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "job_log", return_value=""):
        ci.report_jobs()
    assert writes == []


def test_skipped_cancelled_jobs_and_foreign_comments_are_left_alone(report_env):
    respond, writes = report_api(
        [ci_job("validate go", "skipped", 3), ci_job("audit", "cancelled", 4)],
        [job_comment("validate go", "spoofed failure", user="someone")],
    )
    with patch.object(ci, "api", side_effect=respond), patch.object(ci, "job_log") as logs:
        ci.report_jobs()
    assert writes == []
    logs.assert_not_called()


def test_security_wraps_a_tool_into_the_summary_and_the_log(tmp_path, capsys):
    summary = tmp_path / "summary.md"
    with patch.dict(os.environ, GITHUB_STEP_SUMMARY=str(summary)):
        code = ci.security(
            "example-check", ["sh", "-c", "printf '\\033[1mRun started\\033[0m\\nNo issues identified.\\n'"]
        )
    assert code == 0
    text = summary.read_text()
    assert "### example-check" in text and "**Result:** passed: No issues identified." in text
    assert "\x1b" not in text and "Run started" in text
    out = capsys.readouterr().out
    assert ci.security_reports(out) == [
        {"tool": "example-check", "status": "passed", "summary": "No issues identified."}
    ]


def test_security_keeps_the_tool_exit_status_and_skips_make_noise(tmp_path, capsys):
    with patch.dict(os.environ, GITHUB_STEP_SUMMARY=str(tmp_path / "s.md")):
        code = ci.security(
            "example-check", ["sh", "-c", "echo '1 vulnerability found'; echo 'make: *** [osv-go] Error 1'; exit 1"]
        )
    assert code == 1
    assert ci.security_reports(capsys.readouterr().out) == [
        {"tool": "example-check", "status": "failed", "summary": "1 vulnerability found", "findings": []}
    ]


def test_security_command_line_requires_a_separator(monkeypatch):
    monkeypatch.setattr(sys, "argv", ["ci_automation.py", "security", "bandit", "make", "bandit"])
    with pytest.raises(SystemExit, match="usage"):
        ci.main()


def test_an_expected_failure_is_an_annotation_not_a_traceback(monkeypatch, capsys):
    monkeypatch.setattr(sys, "argv", ["ci_automation.py", "verify-signatures"])
    failure = ValueError("Unverified commit signatures: b2af6d25fe (unsigned) ci: bump x\nsecond line")
    with patch.object(ci, "verify_signatures", side_effect=failure):
        with pytest.raises(SystemExit) as exit_code:
            ci.main()
    assert exit_code.value.code == 1
    captured = capsys.readouterr()
    assert captured.out == "::error::Unverified commit signatures: b2af6d25fe (unsigned) ci: bump x%0Asecond line\n"
    assert "Traceback" not in captured.out + captured.err


def test_a_failed_command_is_reported_without_its_output(monkeypatch, capsys):
    monkeypatch.setattr(sys, "argv", ["ci_automation.py", "verify-signatures"])
    failure = subprocess.CalledProcessError(1, ["gh", "api", "x"], output="private-token-example")
    with patch.object(ci, "verify_signatures", side_effect=failure):
        with pytest.raises(SystemExit):
            ci.main()
    out = capsys.readouterr().out
    assert out == "::error::`gh` failed with exit status 1\n"


def test_job_logs_send_the_token_to_github_only():
    response = Mock()
    response.read.return_value = b"log \x1b[1mline\x1b[0m"
    response.__enter__ = Mock(return_value=response)
    response.__exit__ = Mock(return_value=False)
    with (
        patch.dict(os.environ, GH_REPO="owner/cloud", GH_TOKEN="workflow-token"),
        patch.object(ci.urllib.request, "urlopen", return_value=response) as urlopen,
    ):
        assert ci.job_log(42) == "log \x1b[1mline\x1b[0m"
    request = urlopen.call_args.args[0]
    assert request.full_url == "https://api.github.com/repos/owner/cloud/actions/jobs/42/logs"
    assert request.unredirected_hdrs["Authorization"] == "Bearer workflow-token"
    assert "Authorization" not in request.headers


def test_an_unreadable_log_still_reports_the_job(report_env, capsys):
    respond, writes = report_api([ci_job("validate go", "failure", 3), ci_job("audit", "success", 4)], [])
    with (
        patch.object(ci, "api", side_effect=respond),
        patch.object(ci, "job_log", side_effect=ci.urllib.error.URLError("forbidden")),
    ):
        ci.report_jobs()
    assert len(writes) == 2
    assert all("The job log could not be read" in body for _, _, body in writes)
    assert "::warning::Cannot read the log of validate go" in capsys.readouterr().out


@pytest.mark.parametrize(
    "output,expected",
    [
        ("2:14PM INF 627 commits scanned.\n2:14PM INF no leaks found\n", "no leaks found"),
        ("No vulnerabilities found.\nmake[1]: Leaving directory\n", "No vulnerabilities found."),
        ("", "no output"),
        (
            "Test results:\n\tNo issues identified.\n\nCode scanned:\n\tTotal lines of code: 10\nFiles skipped (0):\n",
            "No issues identified.",
        ),
        (
            ">> Issue: [B602:subprocess_popen_with_shell_equals_true] subprocess call with shell=True\n"
            "Run metrics:\nFiles skipped (0):\n",
            ">> Issue: [B602:subprocess_popen_with_shell_equals_true] subprocess call with shell=True",
        ),
        ("verifying github.com/spf13/cobra@v1.10.2: checksum mismatch\nSECURITY ERROR\n", "SECURITY ERROR"),
        ("tests/test_x.py ....\n========== 12 passed in 3.21s ==========\n", "12 passed in 3.21s"),
        (
            "FAILED tests/test_x.py::test_y - AssertionError\n===== 1 failed, 11 passed in 2.00s =====\n",
            "1 failed, 11 passed in 2.00s",
        ),
        (
            "Script tests: full suite\nNo script tests affected by this change.\n",
            "No script tests affected by this change.",
        ),
        (
            "ok  \tgithub.com/goabonga/cloud/cmd/cli\t1.02s\nok  \tgithub.com/goabonga/cloud/internal/cli\t1.01s\n",
            "2 packages passed",
        ),
        (
            "--- FAIL: TestRun (0.00s)\nFAIL\nFAIL\tgithub.com/goabonga/cloud/internal/cli\t0.01s\nok  \tgithub.com/goabonga/cloud/cmd/cli\t1.02s\n",
            "1 packages passed, 1 failed",
        ),
        (
            "=== Symbol Results ===\nYour code is affected by 1 vulnerability from 1 module.\nThis scan also found 2\n"
            "vulnerabilities.\n",
            "Your code is affected by 1 vulnerability from 1 module.",
        ),
    ],
)
def test_headline_keeps_the_tool_conclusion(output, expected):
    assert ci.headline(output) == expected


def test_reports_are_posted_as_the_pat_owner_and_its_comments_are_reused(report_env):
    calls = []

    def respond(endpoint, method="GET", payload=None, paginate=False, token=None):
        calls.append((endpoint, method, token))
        if endpoint == "user":
            return {"login": "goabonga"}
        if "/jobs?" in endpoint:
            return [{"jobs": [ci_job("audit", "success", 4)]}]
        if method == "GET":
            return [
                [
                    job_comment("audit", "old", number=61, user="goabonga"),
                    job_comment("validate go", "spoofed", number=62, user="someone"),
                ]
            ]
        return {}

    with (
        patch.dict(os.environ, LOGS_TOKEN="workflow-token"),
        patch.object(ci, "api", side_effect=respond),
        patch.object(ci, "job_log", return_value=""),
    ):
        ci.report_jobs()
    assert ("repos/owner/cloud/actions/runs/99/attempts/1/jobs?per_page=100", "GET", "workflow-token") in calls
    writes = [(endpoint, method) for endpoint, method, _ in calls if method != "GET"]
    assert writes == [("repos/owner/cloud/issues/comments/61", "PATCH")]


def test_comment_author_falls_back_to_the_actions_bot():
    with patch.object(ci, "api", side_effect=subprocess.CalledProcessError(1, ["gh"])):
        assert ci.comment_author() == "github-actions[bot]"


def test_error_lines_quote_what_the_security_tools_found():
    log = "\n".join(
        "2026-10-06T10:00:00.0000000Z " + line
        for line in [
            "error[unpinned-uses]: unpinned action reference",
            "  --> .github/workflows/zizmor-example.yml:15:15",
            "Vulnerability #1: GO-2021-0113",
            "    Found in: golang.org/x/text@v0.3.5",
            "RuleID:      generic-api-key",
            "File:        config/example.env",
            "| https://osv.dev/PYSEC-2026-2122     | 7.5  | PyPI      | cairosvg | 2.8.2   | 2.9.0         | scripts/uv.lock |",
            "verifying github.com/spf13/cobra@v1.9.1: checksum mismatch",
            "SECURITY ERROR",
            "internal/cli/cli.go:71:6: func unusedHelper is unused (unused)",
            "Starting filesystem walk for root: /",
        ]
    )
    lines = ci.error_lines(log)
    assert "error[unpinned-uses]: unpinned action reference" in lines
    assert "Vulnerability #1: GO-2021-0113" in lines and "RuleID:      generic-api-key" in lines
    assert any("PYSEC-2026-2122" in line for line in lines)
    assert "verifying github.com/spf13/cobra@v1.9.1: checksum mismatch" in lines
    assert "internal/cli/cli.go:71:6: func unusedHelper is unused (unused)" in lines
    assert "Starting filesystem walk for root: /" not in lines


def test_security_records_what_a_failing_tool_found(tmp_path, capsys):
    script = (
        "echo 'Run started'; echo '>> Issue: [B602:subprocess_popen_with_shell_equals_true] shell=True';"
        " echo '   Location: scripts/shell_example.py:9:0'; echo 'Files skipped (0):'; exit 1"
    )
    with patch.dict(os.environ, GITHUB_STEP_SUMMARY=str(tmp_path / "s.md")):
        assert ci.security("example-check", ["sh", "-c", script]) == 1
    [report] = ci.security_reports(capsys.readouterr().out)
    assert report["findings"] == [
        ">> Issue: [B602:subprocess_popen_with_shell_equals_true] shell=True",
        "   Location: scripts/shell_example.py:9:0",
    ]


def test_reports_survive_a_problem_matcher_prefix():
    report = {
        "tool": "govulncheck",
        "status": "failed",
        "summary": "Your code is affected by 1 vulnerability",
        "findings": ["#1: internal/cli/cli.go:40:23: cli.command calls language.Parse"],
    }
    encoded = base64.b64encode(json.dumps(report).encode()).decode()
    log = f"2026-10-06T16:18:31.7450670Z ##[error]security-report: {encoded}\n"
    assert ci.security_reports(log) == [report]
    assert "cli.go:40:23" not in encoded


def test_security_report_lines_hold_no_matchable_text(tmp_path, capsys):
    with patch.dict(os.environ, GITHUB_STEP_SUMMARY=str(tmp_path / "s.md")):
        ci.security(
            "golangci-lint", ["sh", "-c", "echo 'internal/cli/cli.go:71:6: func unusedHelper is unused'; exit 1"]
        )
    marker = [line for line in capsys.readouterr().out.splitlines() if line.startswith(ci.SECURITY_MARKER)]
    assert len(marker) == 1 and ".go:" not in marker[0]


def test_every_command_the_workflows_run_is_accepted():
    root = Path(__file__).resolve().parents[2]
    sources = [*(root / ".github/workflows").glob("*.yml"), root / "Makefile"]
    used = {
        command
        for source in sources
        for command in re.findall(r"ci_automation\.py\"? ([a-z][a-z-]+)", source.read_text())
    } - {"security"}
    assert {"promote", "check-promotion", "sync-develop"} <= used
    assert used <= set(ci.command_actions(None))
