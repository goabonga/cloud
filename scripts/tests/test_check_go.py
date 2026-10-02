# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Go validation includes imported internal packages and excludes stdlib."""

from unittest.mock import Mock

import pytest

import check_go


def test_import_graph_filters_standard_library_and_duplicates(monkeypatch):
    runner = Mock(side_effect=["github.com/goabonga/cloud", "net/http\ngithub.com/goabonga/cloud/cmd/api\ngithub.com/goabonga/cloud/internal/transport\ngithub.com/goabonga/cloud/internal/transport"])
    monkeypatch.setattr(check_go, "run", runner)
    entry, packages = check_go.packages("cloud-api")
    assert entry == ["./cmd/api"]
    assert packages == ["github.com/goabonga/cloud/cmd/api", "github.com/goabonga/cloud/internal/transport"]
    assert runner.call_args.args[-1] == "./cmd/api"


def test_unknown_component_does_not_run_tools(monkeypatch):
    runner = Mock()
    monkeypatch.setattr(check_go, "run", runner)
    with pytest.raises(ValueError, match="Unknown Go component"):
        check_go.packages("../unregistered")
    runner.assert_not_called()


def test_empty_import_graph_blocks_validation(monkeypatch):
    monkeypatch.setattr(check_go, "run", Mock(side_effect=["github.com/goabonga/cloud", "net/http"]))
    with pytest.raises(ValueError, match="No Go packages"):
        check_go.packages("cloud-api")


@pytest.fixture
def graph(monkeypatch):
    import json

    module = "github.com/goabonga/cloud"
    packages = {
        module + "/cmd/api": [module + "/internal/transport"],
        module + "/cmd/cli": [module + "/internal/cli"],
        module + "/internal/transport": [],
        module + "/internal/cli": [],
    }
    documents = "\n".join(json.dumps({"ImportPath": name, "Deps": deps}) for name, deps in packages.items())
    monkeypatch.setattr(check_go, "run", Mock(side_effect=[module, documents]))
    return list(packages)


def test_shared_package_change_selects_consumers_without_unrelated_packages(graph):
    assert check_go.affected_packages(graph, ["internal/transport/server.go"], ["./cmd/api", "./cmd/cli"]) == [
        "github.com/goabonga/cloud/cmd/api", "github.com/goabonga/cloud/internal/transport"]


def test_command_change_does_not_retest_unchanged_dependencies(graph):
    assert check_go.affected_packages(graph, ["cmd/api/main_test.go"], ["./cmd/api"]) == [
        "github.com/goabonga/cloud/cmd/api"]


@pytest.mark.parametrize("files", [None, ["go.mod"], ["go.sum"]])
def test_unknown_base_or_module_change_checks_all_owned_packages(files, monkeypatch):
    runner = Mock()
    monkeypatch.setattr(check_go, "run", runner)
    assert check_go.affected_packages(["a", "b"], files, ["./cmd/api"]) == ["a", "b"]
    runner.assert_not_called()


def test_browser_change_exercises_server_packages(graph, monkeypatch):
    monkeypatch.setattr(check_go, "packages", Mock(return_value=(["./cmd/ssr"], ["server", "shared"])))
    assert check_go.affected_packages(graph, ["www/src/App.tsx"], ["./cmd/ssr"]) == ["server", "shared"]


def test_selected_components_check_shared_packages_once(monkeypatch):
    module = "github.com/goabonga/cloud/"
    monkeypatch.setattr(check_go, "packages", Mock(side_effect=[
        (["./cmd/api"], [module + "cmd/api", module + "internal/transport"]),
        (["./cmd/idp"], [module + "cmd/idp", module + "internal/transport"]),
    ]))
    monkeypatch.setattr(check_go, "run", Mock(side_effect=[module.rstrip("/"), ""]))
    commands = Mock()
    monkeypatch.setattr(check_go.subprocess, "run", commands)
    check_go.check_components(["cloud-api", "cloud-identity-platform"])
    expected = [module + "cmd/api", module + "cmd/idp", module + "internal/transport"]
    assert commands.call_args_list[0].args[0] == ["go", "vet", *expected]
    assert commands.call_args_list[1].args[0] == ["go", "test", "-race", *expected]
    assert commands.call_args_list[-1].args[0] == ["go", "build", "-o", "/dev/null", "./cmd/api", "./cmd/idp"]


def test_ci_rejects_invalid_changed_files_before_running_checks(monkeypatch):
    monkeypatch.setattr("sys.argv", ["check_go.py", "--ci"])
    monkeypatch.setenv("GO_COMPONENTS", '["cloud-api"]')
    monkeypatch.setenv("CHANGED_FILES", '[42]')
    runner = Mock()
    monkeypatch.setattr(check_go, "check_components", runner)
    with pytest.raises(ValueError, match="Invalid changed file list"):
        check_go.main()
    runner.assert_not_called()
