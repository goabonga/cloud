#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Keep one durable tracking issue per Dependabot PR, including after a crash.

The issue follows the dependency update issue form and the pull request
description the pull request template."""

import os
from pathlib import Path
import re

from ci_automation import ANNOUNCED_UPDATE, api, output, pr_number, repository

PR_TEMPLATE = Path(__file__).resolve().parents[1] / ".github/pull_request_template.md"
VALIDATION = "Dependency update by Dependabot; the pipeline below validates it."
# The fields of .github/ISSUE_TEMPLATE/dependency_update.yml, rendered the way
# GitHub renders a submitted issue form.
ISSUE_FIELDS = ("Ecosystem", "Updates", "Release notes", "Pull request")
ECOSYSTEMS = {"go": "Go modules", "python": "Python (scripts)", "github-actions": "GitHub Actions"}
NO_RESPONSE = "_No response_"


def marker(repo, number):
    return f"<!-- dependabot-tracking:{repo}#{number} -->"


def find_issue(issues, repo, number, pr_body):
    identity = marker(repo, number)
    # Recognize issues created by the source, even when its PR edit failed.
    legacy = f"Filed by `dependabot-rewrite` for #{number}."
    matches = [
        issue
        for issue in issues
        if "pull_request" not in issue
        and (identity in (issue.get("body") or "") or legacy in (issue.get("body") or ""))
    ]
    if len(matches) > 1:
        print("::warning::Existing duplicate tracking issues found; reusing the oldest without creating another")
    return min(matches, key=lambda item: item["number"]) if matches else None


def pr_marker(repo, number):
    return f"<!-- dependabot-pr:{repo}#{number} -->"


def section(body, heading):
    """The content of one ``## heading`` section of a Markdown description."""
    match = re.search(rf"^## {re.escape(heading)}\n(.*?)(?=^## |\Z)", body, flags=re.M | re.S)
    return match.group(1).strip() if match else ""


def fill_sections(template, contents):
    """``template`` with the content of each named ``## heading`` section replaced."""
    for heading, text in contents.items():
        template = re.sub(
            rf"(^## {re.escape(heading)}\n)(.*?)(?=^## |\Z)",
            lambda match: f"{match.group(1)}\n{text.strip()}\n\n",
            template,
            count=1,
            flags=re.M | re.S,
        )
    return template


def dependabot_text(body, repo, number):
    """Dependabot's own description, out of our template or tracking block."""
    if pr_marker(repo, number) in body:
        return section(body, "Description")
    return re.sub(r"<!-- dependabot-tracking[^>]*-->.*", "", body, flags=re.S).strip()


def update_summary(text):
    """What the update changes: release notes and commits, without Dependabot's
    command help."""
    for separator in ("\n---\n", "<details>\n<summary>Dependabot commands"):
        text = text.split(separator, 1)[0]
    return text.strip()


def issue_body(pr, repo, number):
    """The tracking issue, as the dependency update issue form renders it."""
    text = dependabot_text(pr.get("body") or "", repo, number)
    names = [label["name"] for label in pr.get("labels") or []]
    ecosystem = next((ECOSYSTEMS[name] for name in names if name in ECOSYSTEMS), NO_RESPONSE)
    updates = "\n".join(
        f"- `{current or single}` from {old} to {new}" for current, single, old, new in ANNOUNCED_UPDATE.findall(text)
    )
    values = (ecosystem, updates or issue_title(pr), update_summary(text) or NO_RESPONSE, f"#{number}")
    fields = "".join(f"### {field}\n\n{value}\n\n" for field, value in zip(ISSUE_FIELDS, values))
    return f"{marker(repo, number)}\n{fields}".rstrip() + "\n"


def issue_title(pr):
    """The pull request title without its Conventional Commit type and scope.

    The rewritten commits carry the type; titles read as plain sentences."""
    title = re.sub(r"^[a-z]+(?:\([^)]*\))?!?:\s*", "", pr["title"]).strip() or pr["title"]
    return title[:1].upper() + title[1:]


def issue_labels(pr):
    """The pull request's labels, which name the ecosystem (go, python, ...)."""
    names = [label["name"] for label in pr.get("labels") or []]
    return names if "dependencies" in names else ["dependencies", *names]


def link_block(repo, number, issue):
    url = f"https://github.com/{repo}/issues/{issue['number']}"
    return f"{marker(repo, number)}\nTracked by {url}.\n\nCloses #{issue['number']}"


def templated_body(body, repo, number, issue):
    """Dependabot's description inside the pull request template, or ``None``
    when the repository has no template."""
    if not PR_TEMPLATE.exists():
        return None
    filled = fill_sections(
        PR_TEMPLATE.read_text(),
        {
            "Description": dependabot_text(body, repo, number),
            "Validation": VALIDATION,
            "Related issues": link_block(repo, number, issue),
        },
    )
    return f"{pr_marker(repo, number)}\n{filled.rstrip()}\n"


def pull_request_body(body, repo, number, issue):
    """The pull request description: Dependabot's text in the template, built
    once and then only relinked, so the pipeline boxes CI ticks survive."""
    if pr_marker(repo, number) not in body:
        body = templated_body(body, repo, number, issue) or body
    return linked_body(body, repo, number, issue)


def linked_body(body, repo, number, issue):
    identity = marker(repo, number)
    block = link_block(repo, number, issue)
    # Replace only our own block; preserve Dependabot and maintainer prose.
    pattern = re.escape(identity) + r"\nTracked by [^\n]+\n\nCloses #\d+"
    if re.search(pattern, body):
        return re.sub(pattern, lambda _: block, body)
    legacy = r"<!-- dependabot-tracking -->\nTracked by [^\n]+\n\nCloses #\d+"
    if re.search(legacy, body):
        return re.sub(legacy, lambda _: block, body)
    # A partially saved marker is repaired, not taken as proof of a link.
    body = body.replace(identity, "").rstrip()
    return f"{body}\n\n{block}".lstrip()


def issue_token():
    """The token issue writes use: the maintainer's PAT, so tracking issues appear
    under that account, or the workflow token (GH_TOKEN) when it is unset."""
    return os.environ.get("ISSUE_TOKEN") or None


def track(client=api):
    repo, number = repository(), pr_number()
    endpoint = f"repos/{repo}/pulls/{number}"
    pr = client(endpoint)
    if pr["user"]["login"] != "dependabot[bot]" or (pr["head"]["repo"] or {}).get("full_name") != repo:
        raise ValueError("Tracking requires a same-repository Dependabot PR")
    # No search index: scan every page, open AND closed issues. Missing a
    # page is an error, never a reason to assume the issue does not exist.
    pages = client(f"repos/{repo}/issues?state=all&sort=created&direction=asc&per_page=100", paginate=True)
    issue = find_issue([i for page in pages for i in page], repo, number, pr.get("body") or "")
    if issue is None:
        if pr["state"] != "open":
            return None
        body = issue_body(pr, repo, number)
        # Never retry POST here. If its response is lost, the next serialized
        # run recovers the persisted marker instead of issuing a second POST.
        issue = client(
            f"repos/{repo}/issues",
            "POST",
            {"title": issue_title(pr), "labels": issue_labels(pr), "body": body},
            token=issue_token(),
        )
    elif marker(repo, number) not in (issue.get("body") or ""):
        issue = client(
            f"repos/{repo}/issues/{issue['number']}",
            "PATCH",
            {"body": f"{marker(repo, number)}\n\n{issue.get('body') or ''}"},
            token=issue_token(),
        )
    # Fetch the latest description so a rerun/Dependabot edit does not cause
    # us to overwrite the older body captured before scanning issues.
    current = client(endpoint)
    changes = {}
    body = pull_request_body(current.get("body") or "", repo, number, issue)
    if body != (current.get("body") or ""):
        changes["body"] = body
    if issue_title(current) != current["title"]:
        changes["title"] = issue_title(current)
    if changes:
        client(endpoint, "PATCH", changes)
    # If merging raced with link repair, the closing keyword arrived too
    # late for GitHub auto-close. Reconcile the issue state explicitly.
    current = client(endpoint)
    if current.get("merged") and issue["state"] == "open":
        client(
            f"repos/{repo}/issues/{issue['number']}",
            "PATCH",
            {"state": "closed", "state_reason": "completed"},
            token=issue_token(),
        )
    return issue["number"]


if __name__ == "__main__":
    number = track()
    if number:
        output("issue", number)
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
            stream.write(f"\nTracking issue: https://github.com/{repository()}/issues/{number}\n")
