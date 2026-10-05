#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Normalize security scanner findings and apply the dated exception registry.

Every scanner runs with machine-readable output; one parser per tool turns it
into findings with the same fields. The registry, ``.github/exceptions.toml``,
is the only place where a finding may be accepted: each exception names the
tool, the rule and optionally a path, says why, and expires. An expired
exception still applies but is reported as a warning, so a lapsed review never
blocks unrelated work; the weekly review takes care of it.
"""

from dataclasses import dataclass, field
import datetime
import fnmatch
import json
import os
from pathlib import Path
import re
import tomllib

REGISTRY = Path(__file__).resolve().parent.parent / ".github/exceptions.toml"
MAX_DAYS = 90
TOOLS = ("zizmor", "gitleaks", "bandit", "osv-scanner", "govulncheck", "golangci-lint", "gosec")


@dataclass
class Finding:
    tool: str
    rule: str
    path: str
    line: int
    message: str
    aliases: tuple = ()
    exception: dict | None = field(default=None, compare=False)

    @property
    def rules(self):
        return (self.rule, *self.aliases)

    def describe(self):
        place = f"{self.path}:{self.line}" if self.line else self.path
        return f"{place}: {self.rule} {self.message}".strip()


def relative(path):
    """A path relative to the repository root (the working directory)."""
    path = Path(path)
    if path.is_absolute():
        try:
            return path.resolve().relative_to(Path.cwd().resolve()).as_posix()
        except ValueError:
            return path.as_posix()
    return path.as_posix()


def json_documents(text):
    """Every JSON document in ``text``: some tools stream several, or add text."""
    decoder, index, documents = json.JSONDecoder(), 0, []
    while index < len(text):
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text) or text[index] not in "[{":
            break
        document, index = decoder.raw_decode(text, index)
        documents.append(document)
    return documents


def parse_zizmor(text):
    findings = []
    for item in json_documents(text)[0] if text.strip() else []:
        if item.get("ignored"):
            continue
        location = item["locations"][0]
        findings.append(
            Finding(
                "zizmor",
                item["ident"],
                relative(location["symbolic"]["key"]["Local"]["verbatim_path"]),
                location["concrete"]["location"]["start_point"]["row"] + 1,
                item["desc"],
            )
        )
    return findings


def parse_gitleaks(text):
    return [
        Finding("gitleaks", item["RuleID"], relative(item["File"]), item["StartLine"], item["Description"])
        for item in (json_documents(text)[0] if text.strip() else [])
    ]


def parse_bandit(text):
    return [
        Finding("bandit", item["test_id"], relative(item["filename"]), item["line_number"], item["issue_text"])
        for item in json_documents(text)[0]["results"]
    ]


def parse_osv(text):
    findings = []
    for result in json_documents(text)[0].get("results", []):
        source = relative(result["source"]["path"])
        for package in result.get("packages", []):
            name = f"{package['package']['name']} {package['package']['version']}"
            for group in package.get("groups", []):
                ids = sorted(set(group["ids"]), key=lambda value: (not value.startswith(("GO-", "PYSEC-")), value))
                aliases = tuple(sorted(set(group.get("aliases", [])) | set(ids[1:])))
                findings.append(Finding("osv-scanner", ids[0], source, 0, name, aliases))
    return findings


def parse_govulncheck(text):
    """Reachable vulnerabilities only: a trace that reaches a function. The
    finding points at the closest caller outside the vulnerable module."""
    findings, seen = [], set()
    for document in json_documents(text):
        finding = document.get("finding")
        trace = (finding or {}).get("trace", [])
        if not trace or not any(frame.get("function") for frame in trace):
            continue
        vulnerable = trace[0]
        callers = [frame for frame in trace[1:] if frame.get("module") != vulnerable.get("module")]
        frame = callers[0] if callers else vulnerable
        position = frame.get("position") or {}
        key = (finding["osv"], position.get("filename"), position.get("line"))
        if key in seen:
            continue
        seen.add(key)
        findings.append(
            Finding(
                "govulncheck",
                finding["osv"],
                relative(position.get("filename", "go.mod")),
                position.get("line", 0),
                f"{frame.get('function', '')} calls {vulnerable.get('module', '')} {vulnerable.get('function', '')}".strip(),
            )
        )
    return findings


def parse_golangci(text):
    return [
        Finding(
            "golangci-lint",
            issue["FromLinter"],
            relative(issue["Pos"]["Filename"]),
            issue["Pos"]["Line"],
            issue["Text"],
        )
        for issue in (json_documents(text)[0].get("Issues") or [])
    ]


def parse_gosec(text):
    return [
        Finding("gosec", issue["rule_id"], relative(issue["file"]), int(issue["line"]), issue["details"])
        for issue in (json_documents(text)[0].get("Issues") or [])
    ]


PARSERS = {
    "zizmor": parse_zizmor,
    "gitleaks": parse_gitleaks,
    "bandit": parse_bandit,
    "osv-scanner": parse_osv,
    "govulncheck": parse_govulncheck,
    "golangci-lint": parse_golangci,
    "gosec": parse_gosec,
}


def load_registry(path=REGISTRY):
    """The registry's exceptions, validated: a broken entry is an error."""
    path = Path(path)
    if not path.exists():
        return []
    entries = tomllib.loads(path.read_text()).get("exception", [])
    for number, entry in enumerate(entries, 1):
        problems = []
        if entry.get("tool") not in TOOLS:
            problems.append(f"unknown tool {entry.get('tool')!r}")
        if not str(entry.get("rule", "")).strip():
            problems.append("missing rule")
        if not str(entry.get("reason", "")).strip():
            problems.append("missing reason")
        added, expires = entry.get("added"), entry.get("expires")
        if not isinstance(added, datetime.date) or not isinstance(expires, datetime.date):
            problems.append("added and expires must be dates")
        elif not 0 <= (expires - added).days <= MAX_DAYS:
            problems.append(f"expires must fall within {MAX_DAYS} days of added")
        if problems:
            raise ValueError(f"{path.name} exception #{number}: " + "; ".join(problems))
    return entries


def matches(entry, finding):
    return (
        entry["tool"] == finding.tool
        and entry["rule"] in finding.rules
        and (not entry.get("path") or fnmatch.fnmatch(finding.path, entry["path"]))
    )


def apply_registry(findings, entries, today=None):
    """Split findings into those that block and those an exception accepts.

    An accepted finding keeps a reference to its exception; ``expired`` lists
    the exceptions past their date that still accepted something.
    """
    today = today or datetime.date.today()
    blocking, accepted, expired = [], [], []
    for finding in findings:
        entry = next((entry for entry in entries if matches(entry, finding)), None)
        if entry is None:
            blocking.append(finding)
            continue
        finding.exception = entry
        accepted.append(finding)
        if entry["expires"] < today and entry not in expired:
            expired.append(entry)
    return blocking, accepted, expired


def entry_key(entry):
    return (entry["tool"], str(entry["rule"]), entry.get("path", ""))


def registry_blocks(text):
    """The header and each ``[[exception]]`` block of the registry, as text."""
    parts = re.split(r"(?m)^(?=\[\[exception\]\]\s*$)", text)
    return parts[0], parts[1:]


def remove_exception(text, key):
    """The registry text without the exception matching ``key``, comments kept."""
    header, blocks = registry_blocks(text)
    kept = [block for block in blocks if entry_key(tomllib.loads(block)["exception"][0]) != key]
    return header + "".join(kept)


def add_exception(text, entry):
    """The registry text with ``entry``, replacing one for the same finding."""
    text = remove_exception(text, entry_key(entry)).rstrip("\n") + "\n\n"
    lines = ["[[exception]]"]
    for name in ("tool", "rule", "path", "reason"):
        if entry.get(name):
            # JSON strings are valid TOML basic strings.
            lines.append(f"{name} = {json.dumps(str(entry[name]), ensure_ascii=False)}")
    lines += [f"added = {entry['added'].isoformat()}", f"expires = {entry['expires'].isoformat()}"]
    return text + "\n".join(lines) + "\n"


def check(path=REGISTRY, today=None):
    """Validate the registry; an expired exception is a warning, not an error."""
    try:
        entries = load_registry(path)
    except ValueError as error:
        return [str(error)]
    today = today or datetime.date.today()
    return [
        f"warning: {entry['tool']} exception {entry['rule']} expired on {entry['expires']}"
        for entry in entries
        if entry["expires"] < today
    ]


if __name__ == "__main__":
    os.chdir(Path(__file__).resolve().parent.parent)
    issues = check()
    for issue in issues:
        print(issue)
    raise SystemExit(any(not issue.startswith("warning:") for issue in issues))
