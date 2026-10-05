# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""The pipeline boxes of a pull request description follow each CI run."""

import os
import re
from pathlib import Path
from unittest.mock import patch

import pytest

import ci_automation as ci

ROOT = Path(__file__).resolve().parents[2]
HEAD = "b" * 40
BODY = """## Pipeline

- [ ] audit <!-- ci-box: audit -->
  - [ ] zizmor <!-- ci-box: audit / zizmor -->
- [ ] detect changed components <!-- ci-box: detect changed components -->
- [x] validate go <!-- ci-box: validate go --> passed on `aaaaaaa`
  - [ ] govulncheck <!-- ci-box: validate go / govulncheck -->
  - [ ] osv-scanner <!-- ci-box: validate go / osv-scanner -->
- [ ] validate documentation <!-- ci-box: validate documentation -->

## Checklist

- [ ] `make check` passes.
"""
CONCLUSIONS = {
    "audit": "success",
    "detect changed components": "success",
    "validate go": "failure",
    "validate documentation": "skipped",
}
TOOLS = {("audit", "zizmor"): "passed", ("validate go", "govulncheck"): "failed"}


class Api:
    def __init__(self, body, head=HEAD):
        self.body, self.head, self.writes = body, head, []

    def __call__(self, endpoint, method="GET", payload=None, paginate=False, token=None):
        if method != "GET":
            self.writes.append((endpoint, method, payload))
            return {}
        return {"head": {"sha": self.head}, "body": self.body}


def update(api, conclusions=CONCLUSIONS, tools=TOOLS):
    with patch.dict(os.environ, GH_REPO="owner/cloud", HEAD_SHA=HEAD), patch.object(ci, "api", api):
        ci.update_pipeline_checklist(7, conclusions, tools)


def test_boxes_follow_the_jobs_and_tools_of_this_run():
    api = Api(BODY)
    update(api)
    [(endpoint, method, payload)] = api.writes
    assert (endpoint, method) == ("repos/owner/cloud/pulls/7", "PATCH")
    lines = payload["body"].splitlines()
    assert "- [x] audit <!-- ci-box: audit --> passed on `bbbbbbb`" in lines
    assert "  - [x] zizmor <!-- ci-box: audit / zizmor --> passed on `bbbbbbb`" in lines
    assert "- [ ] validate go <!-- ci-box: validate go --> failed on `bbbbbbb`" in lines
    assert "  - [ ] govulncheck <!-- ci-box: validate go / govulncheck --> failed on `bbbbbbb`" in lines
    # The job stopped before this tool reported.
    assert "  - [ ] osv-scanner <!-- ci-box: validate go / osv-scanner --> not run on `bbbbbbb`" in lines
    assert "- [x] validate documentation <!-- ci-box: validate documentation --> not needed on `bbbbbbb`" in lines
    # Human checklist items are left alone.
    assert "- [ ] `make check` passes." in lines


def test_jobs_skipped_after_a_detection_failure_did_not_run():
    api = Api(BODY)
    update(api, {"detect changed components": "failure", "validate documentation": "skipped"}, {})
    body = api.writes[0][2]["body"]
    assert "- [ ] validate documentation <!-- ci-box: validate documentation --> not run on `bbbbbbb`" in body
    # A job missing from the run keeps its box as it was.
    assert "- [ ] audit <!-- ci-box: audit -->\n" in body


def test_a_second_identical_run_writes_nothing():
    api = Api(BODY)
    update(api)
    again = Api(api.writes[0][2]["body"])
    update(again)
    assert again.writes == []


@pytest.mark.parametrize("body", [None, "", "Created without the template.\n\n- [ ] a box without a marker"])
def test_descriptions_without_markers_are_left_untouched(body):
    api = Api(body)
    update(api)
    assert api.writes == []


def test_a_run_for_an_older_commit_does_not_touch_the_description():
    api = Api(BODY, head="c" * 40)
    update(api)
    assert api.writes == []


def test_every_template_box_names_a_ci_job():
    template = (ROOT / ".github/pull_request_template.md").read_text()
    workflow = (ROOT / ".github/workflows/ci.yml").read_text()
    jobs = set(re.findall(r"^    name: (.+)$", workflow, re.MULTILINE))
    keys = re.findall(r"<!-- ci-box: (.+?) -->", template)
    assert keys and all(key.partition(" / ")[0] in jobs for key in keys)
    assert all(ci.CHECKLIST_BOX.search(line) for line in template.splitlines() if "ci-box:" in line)
