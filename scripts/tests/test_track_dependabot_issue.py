# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Recover durable issue identity after partial writes and Dependabot edits."""

import pytest

import copy
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import track_dependabot_issue as tracking


@pytest.fixture
def state(monkeypatch):
    monkeypatch.setenv("GH_REPO", "owner/cloud")
    monkeypatch.setenv("PR_NUMBER", "42")
    state = {
        "pr": {
            "number": 42,
            "title": "chore(deps): bump example",
            "body": "Original description",
            "state": "open",
            "merged": False,
            "user": {"login": "dependabot[bot]"},
            "head": {"repo": {"full_name": "owner/cloud"}},
        },
        "issues": [],
        "creates": 0,
        "fail_link": False,
        "lose_create_response": False,
        "fail_list": False,
        "merge_after_create": False,
    }

    def api(endpoint, method="GET", payload=None, paginate=False, token=None):
        state.setdefault("tokens", []).append((endpoint, method, token))
        if method == "GET" and "/pulls/42" in endpoint:
            return copy.deepcopy(state["pr"])
        if method == "GET" and "/issues?" in endpoint:
            assert paginate
            assert "state=all" in endpoint
            if state["fail_list"]:
                raise RuntimeError("Second page unavailable")
            return [[], copy.deepcopy(state["issues"])]
        if method == "POST":
            state["creates"] += 1
            issue = {"number": 100 + state["creates"], "state": "open", **payload}
            state["issues"].append(issue)
            if state["merge_after_create"]:
                state["pr"].update(state="closed", merged=True)
            if state["lose_create_response"]:
                state["lose_create_response"] = False
                raise RuntimeError("POST succeeded but its response was lost")
            return copy.deepcopy(issue)
        if method == "PATCH" and "/pulls/42" in endpoint:
            if state["fail_link"]:
                state["fail_link"] = False
                raise RuntimeError("PR body update failed")
            state["pr"].update(payload)
            return copy.deepcopy(state["pr"])
        if method == "PATCH":
            number = int(endpoint.rsplit("/", 1)[1])
            issue = next((i for i in state["issues"] if i["number"] == number))
            issue.update(payload)
            return copy.deepcopy(issue)
        raise AssertionError((endpoint, method))

    state["api"] = api
    return state


def test_reruns_and_replaced_pr_body_reuse_issue(state):
    assert tracking.track(state["api"]) == 101
    tracking.track(state["api"])
    state["pr"].update(title="chore(deps): bump a new version", body="Updated by Dependabot")
    assert tracking.track(state["api"]) == 101
    assert state["creates"] == 1
    assert "Updated by Dependabot" in state["pr"]["body"]
    assert state["pr"]["body"].count("Closes #101") == 1


def test_failed_pr_edit_recovers_without_creating_again(state):
    state["fail_link"] = True
    with pytest.raises(RuntimeError):
        tracking.track(state["api"])
    tracking.track(state["api"])
    assert state["creates"] == 1
    assert "Closes #101" in state["pr"]["body"]


def test_lost_create_response_recovers_persisted_marker(state):
    state["lose_create_response"] = True
    with pytest.raises(RuntimeError):
        tracking.track(state["api"])
    tracking.track(state["api"])
    assert state["creates"] == 1


def test_closed_legacy_issue_is_adopted_even_without_pr_marker(state):
    state["issues"].append(
        {
            "number": 88,
            "state": "closed",
            "title": "Old title",
            "body": "Filed by `dependabot-rewrite` for #42. The PR commits were signed.",
        }
    )
    assert tracking.track(state["api"]) == 88
    assert state["creates"] == 0
    assert state["issues"][0]["state"] == "closed"
    assert tracking.marker("owner/cloud", 42) in state["issues"][0]["body"]


def test_failed_listing_never_creates_issue(state):
    state["fail_list"] = True
    with pytest.raises(RuntimeError):
        tracking.track(state["api"])
    assert state["creates"] == 0


def test_merge_race_closes_issue(state):
    state["merge_after_create"] = True
    tracking.track(state["api"])
    assert state["issues"][0]["state"] == "closed"
    assert state["issues"][0]["state_reason"] == "completed"


def test_other_prs_and_spoofed_links_cannot_adopt_unrelated_issue(state):
    state["issues"].append({"number": 88, "state": "open", "body": tracking.marker("owner/cloud", 43)})
    state["pr"]["body"] = (
        "<!-- dependabot-tracking -->\nTracked by https://github.com/owner/cloud/issues/88.\n\nCloses #88"
    )
    assert tracking.track(state["api"]) == 101
    assert state["issues"][0]["body"] == tracking.marker("owner/cloud", 43)


def test_existing_duplicates_choose_oldest_and_never_create(state):
    state["issues"] = [{"number": n, "state": "open", "body": tracking.marker("owner/cloud", 42)} for n in [90, 80]]
    assert tracking.track(state["api"]) == 80
    assert state["creates"] == 0


def test_closed_pr_without_issue_does_not_create(state):
    state["pr"]["state"] = "closed"
    assert tracking.track(state["api"]) is None
    assert state["creates"] == 0


def test_new_issue_describes_the_update_and_names_its_ecosystem(state):
    state["pr"]["labels"] = [{"name": "dependencies"}, {"name": "github-actions"}]
    state["pr"]["body"] = (
        "Bumps the github-actions group with 2 updates: [actions/setup-go](https://github.com/actions/setup-go)"
        " and [getplumber/plumber](https://github.com/getplumber/plumber).\n\n"
        "Updates `actions/setup-go` from 6.5.0 to 7.0.0\n<details>\n<summary>Release notes</summary>\n"
        "notes\n</details>\n\n---\n<details>\n<summary>Dependabot commands and options</summary>\n"
        "@dependabot rebase\n</details>"
    )
    tracking.track(state["api"])
    [issue] = state["issues"]
    assert issue["labels"] == ["dependencies", "github-actions"]
    assert issue["body"].startswith(tracking.marker("owner/cloud", 42) + "\n### Ecosystem\n\nGitHub Actions\n")
    assert "### Updates\n\n- `actions/setup-go` from 6.5.0 to 7.0.0\n" in issue["body"]
    notes = issue["body"].split("### Release notes\n\n", 1)[1]
    assert notes.startswith("Bumps the github-actions group") and "<summary>Release notes</summary>" in notes
    assert "Dependabot commands" not in issue["body"] and "@dependabot" not in issue["body"]
    assert issue["body"].rstrip().endswith("### Pull request\n\n#42")
    for generic in ("Dependabot update", "re-authors and GPG-signs"):
        assert generic not in issue["body"]


def test_issue_labels_always_include_dependencies(state):
    state["pr"]["labels"] = [{"name": "python"}]
    tracking.track(state["api"])
    assert state["issues"][0]["labels"] == ["dependencies", "python"]


@pytest.mark.parametrize(
    "title,expected",
    [
        ("ci: bump the github-actions group with 2 updates", "Bump the github-actions group with 2 updates"),
        (
            "fix(deps): bump github.com/spf13/cobra from 1.9.1 to 1.10.2",
            "Bump github.com/spf13/cobra from 1.9.1 to 1.10.2",
        ),
        ("chore(deps)!: bump pytest in /scripts", "Bump pytest in /scripts"),
        ("Bump something", "Bump something"),
    ],
)
def test_issue_title_drops_the_conventional_commit_type(state, title, expected):
    state["pr"]["title"] = title
    tracking.track(state["api"])
    assert state["issues"][0]["title"] == expected


def test_pull_request_title_drops_the_conventional_commit_type_once(state):
    state["pr"]["title"] = "fix(deps): bump cairosvg from 2.8.2 to 2.9.1 in /scripts in the python-runtime group"
    tracking.track(state["api"])
    assert state["pr"]["title"] == "Bump cairosvg from 2.8.2 to 2.9.1 in /scripts in the python-runtime group"
    assert state["issues"][0]["title"] == state["pr"]["title"]
    state["pr"]["title"] = "ci: bump actions/setup-go"
    tracking.track(state["api"])
    assert state["pr"]["title"] == "Bump actions/setup-go"
    assert len(state["issues"]) == 1


def test_issue_writes_use_the_maintainer_token_and_pr_edits_the_workflow_token(state, monkeypatch):
    monkeypatch.setenv("ISSUE_TOKEN", "maintainer-pat")
    state["pr"]["title"] = "ci: bump actions/setup-go"
    tracking.track(state["api"])
    writes = [
        (endpoint.split("/")[-2] + "/" + endpoint.split("/")[-1] if "/issues/" in endpoint else endpoint, method, token)
        for endpoint, method, token in state["tokens"]
        if method != "GET"
    ]
    assert ("repos/owner/cloud/issues", "POST", "maintainer-pat") in writes
    assert ("repos/owner/cloud/pulls/42", "PATCH", None) in writes


def test_issue_writes_fall_back_to_the_workflow_token(state, monkeypatch):
    monkeypatch.delenv("ISSUE_TOKEN", raising=False)
    tracking.track(state["api"])
    assert ("repos/owner/cloud/issues", "POST", None) in state["tokens"]


def test_issue_fields_follow_the_dependency_update_form():
    form = (Path(__file__).resolve().parents[2] / ".github/ISSUE_TEMPLATE/dependency_update.yml").read_text()
    assert tuple(re.findall(r"^      label: (.+)$", form, re.M)) == tracking.ISSUE_FIELDS
    options = re.findall(r"^        - (.+)$", form, re.M)
    assert set(tracking.ECOSYSTEMS.values()) <= set(options)
    assert "labels: [dependencies]" in form


def test_pull_request_description_follows_the_template(state):
    state["pr"]["labels"] = [{"name": "dependencies"}, {"name": "go"}]
    state["pr"]["body"] = "Bumps [x](https://x) from 1.0.0 to 1.1.0.\n\n---\n<details>\n@dependabot rebase\n</details>"
    tracking.track(state["api"])
    body = state["pr"]["body"]
    assert body.startswith(tracking.pr_marker("owner/cloud", 42) + "\n## Description\n")
    # Dependabot's whole text stays, its commands included.
    assert tracking.section(body, "Description").startswith("Bumps [x](https://x) from 1.0.0 to 1.1.0.")
    assert "@dependabot rebase" in tracking.section(body, "Description")
    assert tracking.section(body, "Validation") == tracking.VALIDATION
    assert tracking.section(body, "Related issues").endswith("Closes #101")
    assert body.count("Closes #101") == 1
    assert "<!-- ci-box: validate go / govulncheck -->" in tracking.section(body, "Pipeline")
    assert "## Checklist" not in body
    assert "### Ecosystem\n\nGo modules" in state["issues"][0]["body"]


def test_a_templated_description_is_only_relinked_so_ticked_boxes_survive(state):
    tracking.track(state["api"])
    ticked = state["pr"]["body"].replace(
        "- [ ] audit <!-- ci-box: audit -->", "- [x] audit <!-- ci-box: audit --> passed on `abc1234`"
    )
    state["pr"]["body"] = ticked
    tracking.track(state["api"])
    assert state["pr"]["body"] == ticked


def test_dependabot_rewriting_its_description_gets_the_template_again(state):
    tracking.track(state["api"])
    state["pr"]["body"] = "Updated by Dependabot"
    tracking.track(state["api"])
    body = state["pr"]["body"]
    assert tracking.section(body, "Description") == "Updated by Dependabot"
    assert body.count("Closes #101") == 1 and state["creates"] == 1


def test_without_a_template_the_link_is_appended(state, monkeypatch):
    monkeypatch.setattr(tracking, "PR_TEMPLATE", Path("/nonexistent/pull_request_template.md"))
    tracking.track(state["api"])
    assert state["pr"]["body"].startswith("Original description\n\n" + tracking.marker("owner/cloud", 42))
    assert "## Pipeline" not in state["pr"]["body"]
