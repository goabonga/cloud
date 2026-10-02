# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Release checks reject failures and accept only legitimate conditional skips."""

import json

import pytest

import ci_automation as ci


@pytest.fixture
def jobs(tmp_path, monkeypatch):
    monkeypatch.setenv("GITHUB_STEP_SUMMARY", str(tmp_path / "summary"))
    return {name: {"result": "success", "outputs": {"checks": '["cloud-docs","cloud-scripts"]'} if name == "components" else {}}
            for name in ("plumber", "components", "licenses", "scripts", "documentation", "go", "www")}


def validate(jobs, monkeypatch):
    monkeypatch.setenv("JOB_RESULTS", json.dumps(jobs))
    ci.require_checks()


def test_all_jobs_success(jobs, monkeypatch):
    validate(jobs, monkeypatch)


def test_unchanged_scripts_can_be_skipped(jobs, monkeypatch):
    jobs["components"]["outputs"]["checks"] = '["cloud-docs"]'
    jobs["scripts"]["result"] = "skipped"
    validate(jobs, monkeypatch)


@pytest.mark.parametrize("job", ["plumber", "components", "licenses", "scripts", "documentation", "go", "www"])
@pytest.mark.parametrize("result", ["failure", "cancelled", "skipped"])
def test_failed_or_required_skipped_jobs_block_validation(jobs, monkeypatch, job, result):
    jobs["components"]["outputs"]["checks"] = '["cloud-docs","cloud-scripts","cloud","cloud-www"]'
    jobs[job]["result"] = result
    with pytest.raises(ValueError, match="Required checks failed"):
        validate(jobs, monkeypatch)


def test_missing_detection_output_blocks_validation(jobs, monkeypatch):
    jobs["components"]["outputs"] = {}
    with pytest.raises(ValueError, match="component output"):
        validate(jobs, monkeypatch)


def test_unchanged_docs_python_go_and_frontend_can_all_be_skipped(jobs, monkeypatch):
    jobs["components"]["outputs"]["checks"] = "[]"
    for name in ("documentation", "scripts", "go", "www"):
        jobs[name]["result"] = "skipped"
    validate(jobs, monkeypatch)
