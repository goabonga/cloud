# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Scanner reports become findings; the dated registry accepts some of them."""

import datetime
import os
from pathlib import Path
from unittest.mock import patch

import pytest

import ci_automation as ci
import scanners

FIXTURES = Path(__file__).parent / "fixtures" / "scanners"
TODAY = datetime.date(2026, 10, 7)


def fixture(name):
    return (FIXTURES / name).read_text()


@pytest.mark.parametrize(
    "tool,name,expected",
    [
        ("zizmor", "zizmor.json", [("unpinned-uses", ".github/workflows/zizmor-example.yml", 16)]),
        ("gitleaks", "gitleaks.json", [("generic-api-key", "config/example.env", 1)]),
        ("bandit", "bandit.json", [("B602", "scripts/shell_example.py", 11)]),
        (
            "osv-scanner",
            "osv-go.json",
            [("GO-2021-0113", "go.mod", 0), ("GO-2022-1059", "go.mod", 0), ("GO-2026-5970", "go.mod", 0)],
        ),
        ("osv-scanner", "osv-py.json", [("PYSEC-2026-2122", "scripts/uv.lock", 0)]),
        ("govulncheck", "govulncheck.json", [("GO-2021-0113", "internal/cli/cli.go", 40)]),
        ("golangci-lint", "golangci.json", [("unused", "internal/cli/cli.go", 71)]),
        ("gosec", "gosec.json", [("G104", "cmd/cli/main.go", 6)]),
    ],
)
def test_each_scanner_report_becomes_normalized_findings(tool, name, expected):
    findings = scanners.PARSERS[tool](fixture(name))
    assert [(finding.rule, finding.path, finding.line) for finding in findings] == expected
    assert all(finding.tool == tool and finding.message for finding in findings)


def test_osv_findings_carry_their_aliases():
    [finding] = scanners.parse_osv(fixture("osv-py.json"))
    assert {"GHSA-f38f-5xpm-9r7c", "CVE-2026-31899"} <= set(finding.aliases)


def test_govulncheck_ignores_vulnerabilities_the_code_does_not_reach():
    findings = scanners.parse_govulncheck(fixture("govulncheck.json"))
    assert [finding.rule for finding in findings] == ["GO-2021-0113"]
    assert findings[0].message == "command calls golang.org/x/text Parse"


def test_empty_reports_have_no_findings():
    assert scanners.parse_zizmor("[]") == [] and scanners.parse_gitleaks("") == []
    assert scanners.parse_golangci('{"Issues": null}') == [] and scanners.parse_govulncheck("") == []


def entry(**fields):
    base = {
        "tool": "bandit",
        "rule": "B602",
        "reason": "constant input",
        "added": TODAY,
        "expires": TODAY + datetime.timedelta(days=7),
    }
    return {**base, **fields}


def test_registry_accepts_by_tool_rule_alias_and_path_glob():
    bandit = scanners.parse_bandit(fixture("bandit.json"))
    osv = scanners.parse_osv(fixture("osv-py.json"))
    entries = [
        entry(path="scripts/*.py"),
        entry(tool="osv-scanner", rule="GHSA-f38f-5xpm-9r7c"),
    ]
    blocking, accepted, expired = scanners.apply_registry(bandit + osv, entries, TODAY)
    assert blocking == [] and len(accepted) == 2 and expired == []
    blocking, _, _ = scanners.apply_registry(bandit, [entry(path="cmd/*")], TODAY)
    assert [finding.rule for finding in blocking] == ["B602"]


def test_an_expired_exception_still_accepts_but_is_reported():
    bandit = scanners.parse_bandit(fixture("bandit.json"))
    old = entry(added=TODAY - datetime.timedelta(days=30), expires=TODAY - datetime.timedelta(days=1))
    blocking, accepted, expired = scanners.apply_registry(bandit, [old], TODAY)
    assert blocking == [] and len(accepted) == 1 and expired == [old]


@pytest.mark.parametrize(
    "body,problem",
    [
        ('tool = "trivy"\nrule = "x"\nreason = "r"\nadded = 2026-10-07\nexpires = 2026-10-08', "unknown tool"),
        ('tool = "bandit"\nrule = "B602"\nreason = ""\nadded = 2026-10-07\nexpires = 2026-10-08', "missing reason"),
        ('tool = "bandit"\nrule = "B602"\nreason = "r"\nadded = 2026-10-07\nexpires = 2027-03-01', "within 90 days"),
        ('tool = "bandit"\nrule = "B602"\nreason = "r"\nadded = 2026-10-07', "must be dates"),
    ],
)
def test_invalid_exceptions_are_refused(tmp_path, body, problem):
    registry = tmp_path / "exceptions.toml"
    registry.write_text("[[exception]]\n" + body + "\n")
    with pytest.raises(ValueError, match=problem):
        scanners.load_registry(registry)
    assert problem in scanners.check(registry)[0]


def test_the_committed_registry_is_valid_and_has_no_expired_entry():
    assert scanners.check(today=datetime.date.today()) == []


def run_scan(tmp_path, tool, report, returncode=1, entries=()):
    def fake_run(args, **kwargs):
        target = next((arg for arg in args if arg.endswith("report.json")), None)
        if target:
            Path(target).write_text(report)
        return type("Result", (), {"returncode": returncode, "stdout": "" if target else report, "stderr": ""})()

    with (
        patch.dict(os.environ, GITHUB_STEP_SUMMARY=str(tmp_path / "summary.md")),
        patch.object(ci.subprocess, "run", side_effect=fake_run),
        patch.object(scanners, "load_registry", return_value=list(entries)),
    ):
        return ci.security(tool, ["tool", "-o", "{json}"] if tool != "zizmor" else ["tool"])


def test_scan_fails_on_findings_and_reports_them(tmp_path, capsys):
    assert run_scan(tmp_path, "bandit", fixture("bandit.json")) == 1
    [report] = ci.security_reports(capsys.readouterr().out)
    assert report["status"] == "failed" and report["summary"] == "1 finding"
    assert report["findings"] == [
        "scripts/shell_example.py:11: B602 subprocess call with shell=True identified, security issue."
    ]
    assert report["items"][0] == {
        "rule": "B602",
        "path": "scripts/shell_example.py",
        "line": 11,
        "message": "subprocess call with shell=True identified, security issue.",
        "state": "blocking",
    }


def test_scan_passes_when_the_registry_accepts_every_finding(tmp_path, capsys):
    assert run_scan(tmp_path, "zizmor", fixture("zizmor.json"), 14, [entry(tool="zizmor", rule="unpinned-uses")]) == 0
    [report] = ci.security_reports(capsys.readouterr().out)
    assert report["status"] == "passed" and report["summary"] == "no findings, 1 accepted by exception"
    assert report["items"][0]["state"] == "accepted"


def test_scan_warns_about_an_expired_exception(tmp_path, capsys):
    old = entry(added=datetime.date(2026, 1, 1), expires=datetime.date(2026, 2, 1))
    assert run_scan(tmp_path, "bandit", fixture("bandit.json"), 1, [old]) == 0
    out = capsys.readouterr().out
    assert "::warning::bandit exception B602 expired on 2026-02-01" in out
    assert ci.security_reports(out)[0]["summary"] == "no findings, 1 accepted by exception, 1 expired exception"


def test_scan_fails_when_the_report_cannot_be_read(tmp_path, capsys):
    assert run_scan(tmp_path, "gosec", "not json", 1) == 1
    assert ci.security_reports(capsys.readouterr().out)[0]["summary"].startswith("cannot read the gosec report")


def test_scan_fails_when_the_scanner_errors_without_findings(tmp_path, capsys):
    assert run_scan(tmp_path, "golangci-lint", '{"Issues": []}', 3) == 1
    assert "exited with status 3" in ci.security_reports(capsys.readouterr().out)[0]["summary"]


def test_govulncheck_is_judged_on_its_findings_not_its_exit_status(tmp_path, capsys):
    assert run_scan(tmp_path, "govulncheck", fixture("govulncheck.json"), 0) == 1
    assert ci.security_reports(capsys.readouterr().out)[0]["summary"] == "1 finding"
