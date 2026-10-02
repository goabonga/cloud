# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Selective Python checks retain transitive consumers and fail safe on unknowns."""

from pathlib import Path
from unittest.mock import Mock

import pytest

import check_scripts


@pytest.fixture
def scripts(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    Path("scripts/tests").mkdir(parents=True)
    Path("scripts/helper.py").write_text("VALUE = 1\n")
    Path("scripts/worker.py").write_text("from helper import VALUE\n")
    Path("scripts/other.py").write_text("VALUE = 2\n")
    Path("scripts/tests/test_worker.py").write_text("from worker import VALUE\n")
    Path("scripts/tests/test_other.py").write_text("import other\n")


def test_script_selects_only_its_tests(scripts):
    assert check_scripts.select_tests(["scripts/worker.py"]) == ["scripts/tests/test_worker.py"]


def test_transitive_from_import_keeps_consumer_tests(scripts):
    assert check_scripts.select_tests(["scripts/helper.py"]) == ["scripts/tests/test_worker.py"]


def test_changed_test_selects_itself(scripts):
    assert check_scripts.select_tests(["scripts/tests/test_other.py"]) == ["scripts/tests/test_other.py"]


@pytest.mark.parametrize("files", [None, ["pytest.ini"], ["scripts/ci_automation.py"],
                                       ["scripts/uv.lock"], ["scripts/dependency_release.py"], ["scripts/tests/conftest.py"],
                                       ["scripts/tests/fixtures/input.json"], ["scripts/deleted.py"]])
def test_shared_unknown_or_deleted_files_run_full_suite(scripts, files):
    assert check_scripts.select_tests(files) is None


def test_unmapped_subprocess_script_runs_full_suite(scripts):
    Path("scripts/tool.py").write_text("print('tool')\n")
    assert check_scripts.select_tests(["scripts/tool.py"]) is None


def test_changelog_and_unrelated_docs_select_no_tests(scripts):
    assert check_scripts.select_tests(["scripts/CHANGELOG.md", "docs/index.md"]) == []


def test_import_cycle_terminates_and_selects_consumers(scripts):
    Path("scripts/helper.py").write_text("import worker\n")
    assert check_scripts.select_tests(["scripts/helper.py"]) == ["scripts/tests/test_worker.py"]


def test_runner_compiles_then_executes_selected_tests(scripts, monkeypatch):
    runner = Mock()
    monkeypatch.setattr(check_scripts.subprocess, "run", runner)
    check_scripts.check(["scripts/helper.py"])
    assert runner.call_args_list[0].args[0][:3] == ["python3", "-m", "compileall"]
    assert runner.call_args_list[1].args[0] == [
        "uv", "run", "--project", "scripts", "--locked", "pytest", "scripts/tests/test_worker.py"]


def test_empty_selection_does_not_accidentally_run_full_pytest(scripts, monkeypatch):
    runner = Mock()
    monkeypatch.setattr(check_scripts.subprocess, "run", runner)
    check_scripts.check(["scripts/CHANGELOG.md"])
    assert runner.call_count == 1
