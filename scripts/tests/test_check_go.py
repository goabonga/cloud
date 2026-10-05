# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Go validation includes imported internal packages and excludes stdlib."""

from unittest.mock import Mock

import pytest

import check_go


def test_import_graph_filters_standard_library_and_duplicates(monkeypatch):
    runner = Mock(
        side_effect=[
            "github.com/goabonga/cloud",
            "net/http\ngithub.com/goabonga/cloud/cmd/svc\ngithub.com/goabonga/cloud/internal/transport\ngithub.com/goabonga/cloud/internal/transport",
        ]
    )
    monkeypatch.setattr(check_go, "run", runner)
    entry, packages = check_go.packages("cloud-svc")
    assert entry == ["./cmd/svc"]
    assert packages == ["github.com/goabonga/cloud/cmd/svc", "github.com/goabonga/cloud/internal/transport"]
    assert runner.call_args.args[-1] == "./cmd/svc"


def test_unknown_component_does_not_run_tools(monkeypatch):
    runner = Mock()
    monkeypatch.setattr(check_go, "run", runner)
    with pytest.raises(ValueError, match="Unknown Go component"):
        check_go.packages("../unregistered")
    runner.assert_not_called()


def test_empty_import_graph_blocks_validation(monkeypatch):
    monkeypatch.setattr(check_go, "run", Mock(side_effect=["github.com/goabonga/cloud", "net/http"]))
    with pytest.raises(ValueError, match="No Go packages"):
        check_go.packages("cloud-svc")


@pytest.mark.parametrize(
    "phase,command",
    [
        (
            "test",
            [
                "go",
                "test",
                "-race",
                "github.com/goabonga/cloud/cmd/svc",
                "github.com/goabonga/cloud/internal/transport",
                "github.com/goabonga/cloud/cmd/cli",
            ],
        ),
        ("build", ["go", "build", "-o", "/dev/null", "./cmd/svc", "./cmd/cli"]),
        (
            "gosec",
            [
                "go",
                "run",
                "github.com/securego/gosec/v2/cmd/gosec@v2.29.0",
                "./cmd/svc",
                "./internal/transport",
                "./cmd/cli",
            ],
        ),
    ],
)
def test_each_phase_runs_once_for_every_component(monkeypatch, phase, command):
    graphs = {
        "./cmd/svc": ["github.com/goabonga/cloud/cmd/svc", "github.com/goabonga/cloud/internal/transport"],
        "./cmd/cli": ["github.com/goabonga/cloud/cmd/cli", "github.com/goabonga/cloud/internal/transport"],
    }
    monkeypatch.setattr(
        check_go,
        "run",
        lambda *args: "github.com/goabonga/cloud" if args[1:3] == ("list", "-m") else "\n".join(graphs[args[-1]]),
    )
    calls = Mock()
    monkeypatch.setattr(check_go.subprocess, "run", calls)
    check_go.check(["cloud-svc", "cloud"], phase)
    calls.assert_called_once_with(command, check=True)
