#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Trusted GitHub automation; checked-out PR files are data, never executed."""

import argparse
import base64
import datetime
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import tomllib
import urllib.error
from urllib.parse import quote
import urllib.request

import scanners


def run(*args, **kwargs):
    return subprocess.check_output(args, **kwargs).decode().strip()


def git(*args):
    return run("git", *args)


def api(endpoint, method="GET", payload=None, paginate=False, token=None):
    command = ["gh", "api", endpoint, "--method", method]
    if paginate:
        command += ["--paginate"]
    options = {}
    if token is not None:
        if not token:
            raise ValueError("An explicit GitHub token must not be empty")
        options["env"] = {**os.environ, "GH_TOKEN": token}
    if payload is not None:
        command += ["--input", "-"]
        options["input"] = json.dumps(payload).encode()
    response = run(*command, **options)
    if paginate:
        # gh --paginate prints consecutive JSON documents. Decode each
        # complete page without depending on newer gh's --slurp flag.
        decoder = json.JSONDecoder()
        pages = []
        while response.strip():
            response = response.lstrip()
            page, offset = decoder.raw_decode(response)
            pages.append(page)
            response = response[offset:]
        return pages
    return json.loads(response) if response else None


def repository():
    value = os.environ["GH_REPO"]
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", value):
        raise ValueError("Invalid repository")
    return value


def pr_number():
    return int(os.environ.get("PR_NUMBER") or os.environ["PR"])


def pull_request():
    return api(f"repos/{repository()}/pulls/{pr_number()}")


def validate_head(sha, branch):
    if not re.fullmatch(r"[a-f0-9]{40}", sha):
        raise ValueError("Invalid commit SHA")
    subprocess.run(["git", "check-ref-format", f"refs/heads/{branch}"], check=True)


def output(name, value):
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as stream:
        print(f"{name}={value}", file=stream)


def validate_dependabot():
    sha, branch = os.environ["SIGNAL_SHA"], os.environ["SIGNAL_BRANCH"]
    validate_head(sha, branch)
    if not branch.startswith("dependabot/"):
        raise ValueError("Expected a Dependabot branch")
    owner = repository().split("/")[0]
    pages = api(
        f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}&per_page=100", paginate=True
    )
    matches = [
        p
        for page in pages
        for p in page
        if p["user"]["login"] == "dependabot[bot]"
        and (p["head"]["repo"] or {}).get("full_name") == repository()
        and p["head"]["sha"] == sha
        and p["head"]["ref"] == branch
        and p["base"]["ref"] == os.environ["BASE_BRANCH"]
    ]
    if len(matches) != 1:
        raise ValueError("Expected one current same-repository Dependabot PR")
    output("number", matches[0]["number"])


MAJOR_UPDATE = "update-type: version-update:semver-major"
ANNOUNCED_UPDATE = re.compile(
    r"^(?:Updates `([^`\s]+)`|Bumps \[([^\]\s]+)\]\([^)]*\)) from v?(\S+?) to v?(\S+?)\.?$", re.M
)


def major_updates(text):
    """Dependencies whose leading version number changes in a Dependabot announcement."""
    majors = []
    for current, single, old, new in ANNOUNCED_UPDATE.findall(text):
        before, after = (re.match(r"\d+", version) for version in (old, new))
        if before and after and before.group() != after.group():
            majors.append(current or single)
    return majors


MAJOR_NOTICE = "<!-- dependabot-major-update -->"


def dependabot_pull(pr):
    """A Dependabot pull request from this repository, opened against main."""
    return (
        pr["state"] == "open"
        and pr["user"]["login"] == "dependabot[bot]"
        and (pr["head"]["repo"] or {}).get("full_name") == repository()
        and pr["head"]["ref"].startswith("dependabot/")
        and pr["base"]["ref"] == "main"
    )


def add_merge_label(number):
    """Label with the merge PAT: a label added by the workflow token would not
    start auto-merge-signed, because GitHub does not chain its events."""
    api(f"repos/{repository()}/issues/{number}/labels", "POST", {"labels": [os.environ["MERGE_LABEL"]]})


def label_dependabot():
    """Queue a Dependabot pull request for merging once CI passed on its signed head.

    Major updates are left for a maintainer, who approves one by adding the
    merge label; the pull request gets a notice saying so.
    """
    sha, branch = os.environ["RUN_HEAD_SHA"], os.environ["RUN_HEAD_BRANCH"]
    validate_head(sha, branch)
    owner = repository().split("/")[0]
    pages = api(
        f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}&per_page=100", paginate=True
    )
    pulls = [p for page in pages for p in page if dependabot_pull(p) and p["head"]["ref"] == branch]
    if len(pulls) != 1 or pulls[0]["head"]["sha"] != sha:
        print("CI ran on a commit that is no longer the head of an open Dependabot pull request")
        return
    pr = pulls[0]
    if any(label["name"] == os.environ["MERGE_LABEL"] for label in pr["labels"]):
        print(f"#{pr['number']} is already queued for merging")
        return
    pages = api(f"repos/{repository()}/pulls/{pr['number']}/commits?per_page=100", paginate=True)
    commits = [commit for page in pages for commit in page]
    if not commits or not all((commit["commit"].get("verification") or {}).get("verified") for commit in commits):
        print(f"#{pr['number']} still has commits that are not rewritten and signed")
        return
    # The rewritten commits carry no body, so the pull request description,
    # which Dependabot writes and the rewrite leaves alone, tells majors apart.
    if major_updates(pr.get("body") or "") or any(MAJOR_UPDATE in commit["commit"]["message"] for commit in commits):
        comments = api(f"repos/{repository()}/issues/{pr['number']}/comments?per_page=100", paginate=True)
        if not any(MAJOR_NOTICE in comment["body"] for page in comments for comment in page):
            api(
                f"repos/{repository()}/issues/{pr['number']}/comments",
                "POST",
                {
                    "body": f"{MAJOR_NOTICE}\nThis pull request contains a major update, so it is not merged "
                    f"automatically. Review it, then add the `{os.environ['MERGE_LABEL']}` label to merge "
                    "it through the signed merge queue."
                },
                token=pr_write_token(),
            )
            # The maintainer has to act: the pull request and its tracking issue are theirs.
            maintainer = token_owner()
            tracking = re.search(r"Closes #(\d+)", pr.get("body") or "")
            for number in (pr["number"], *([tracking.group(1)] if tracking else [])):
                assign(number, maintainer)
        print(f"#{pr['number']} is a major update and waits for a maintainer's label")
        return
    add_merge_label(pr["number"])
    print(f"#{pr['number']} queued for merging")


def pr_write_token():
    """The token pull request comments and label removals are written with."""
    token = os.environ.get("PR_WRITE_TOKEN")
    if not token:
        raise ValueError("PR_WRITE_TOKEN is required to comment on or relabel the pull request")
    return token


RELEASE_BRANCH = "main"
INTEGRATION_BRANCH = "develop"
MERGE_JOB_PREFIX = "merge into "


def base_branch():
    """The branch the queue merges into: main, or develop once it exists."""
    base = os.environ.get("BASE_REF") or RELEASE_BRANCH
    if base not in {RELEASE_BRANCH, INTEGRATION_BRANCH}:
        raise ValueError(f"The merge queue does not merge into {base}")
    return base


def is_promotion(pr):
    """A pull request proposing develop, as it is, for release on main."""
    return pr["head"]["ref"] == INTEGRATION_BRANCH and pr["base"]["ref"] == RELEASE_BRANCH


def merge_authorized(pr, sha):
    base, head = base_branch(), pr["head"]["ref"]
    return (
        pr["state"] == "open"
        and not pr["draft"]
        and (pr["head"]["repo"] or {}).get("full_name") == repository()
        and pr["head"]["sha"] == sha
        and head == os.environ["HEAD_REF"]
        and pr["base"]["ref"] == base
        # main only ever moves forward, and develop only reaches main as a promotion.
        and head != RELEASE_BRANCH
        and (head != INTEGRATION_BRANCH or base == RELEASE_BRANCH)
        and any(label["name"] == os.environ["MERGE_LABEL"] for label in pr["labels"])
    )


def check_labeller():
    permission = api(f"repos/{repository()}/collaborators/{quote(os.environ['LABELLER'], safe='')}/permission")[
        "permission"
    ]
    if permission not in {"admin", "maintain", "write"}:
        raise ValueError("The labeller no longer has write access")


def validate_merge():
    validate_head(os.environ["HEAD_SHA"], os.environ["HEAD_REF"])
    if not os.environ.get("GH_TOKEN") or not os.environ.get("GPG_PRIVATE_KEY"):
        raise ValueError("Merge token and signing key are required")
    pull = pull_request()
    if not merge_authorized(pull, os.environ["HEAD_SHA"]):
        raise ValueError("Pull request changed or merge authorization was removed")
    check_labeller()
    output("promotion", "true" if is_promotion(pull) else "false")


def configure_signing():
    for name in ("GPG_PRIVATE_KEY", "PUSH_TOKEN", "GIT_USER_NAME", "GIT_USER_EMAIL", "GIT_SIGNING_KEY"):
        if not os.environ.get(name):
            raise ValueError(f"Missing {name}")
    home = Path(tempfile.mkdtemp(prefix="cloud-signing-", dir=os.environ["RUNNER_TEMP"]))
    home.chmod(0o700)
    os.environ["GNUPGHOME"] = str(home)
    # Register cleanup before any import or key validation can fail.
    with Path(os.environ["GITHUB_ENV"]).open("a") as stream:
        print(f"GNUPGHOME={home}", file=stream)
    subprocess.run(
        ["gpg", "--batch", "--import"],
        input=os.environ["GPG_PRIVATE_KEY"].encode(),
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    subprocess.run(
        ["gpg", "--batch", "--list-secret-keys", os.environ["GIT_SIGNING_KEY"].rstrip("!")],
        check=True,
        stdout=subprocess.DEVNULL,
    )
    passphrase = home / "passphrase"
    passphrase.write_text(os.environ.get("GPG_PASSPHRASE", ""))
    passphrase.chmod(0o600)
    wrapper = home / "git-gpg"
    wrapper.write_text(
        "#!/usr/bin/env python3\nimport os, sys\n"
        "os.execvp('gpg', ['gpg', '--batch', '--pinentry-mode', 'loopback', "
        "'--passphrase-file', os.path.join(os.environ['GNUPGHOME'], 'passphrase'), *sys.argv[1:]])\n"
    )
    wrapper.chmod(0o700)
    for key, value in (
        ("user.name", os.environ["GIT_USER_NAME"]),
        ("user.email", os.environ["GIT_USER_EMAIL"]),
        ("user.signingkey", os.environ["GIT_SIGNING_KEY"]),
        ("gpg.program", str(wrapper)),
        ("commit.gpgsign", "true"),
        ("core.hooksPath", "/dev/null"),
    ):
        git("config", key, value)


def cleanup():
    home = Path(os.environ.get("GNUPGHOME", "/nonexistent"))
    root = Path(os.environ["RUNNER_TEMP"]).resolve()
    if home.parent.resolve() == root and home.name.startswith("cloud-signing-") and not home.is_symlink():
        subprocess.run(["gpgconf", "--kill", "gpg-agent"], check=False)
        shutil.rmtree(home, ignore_errors=True)


def rebase(mode):
    git("config", "core.hooksPath", "/dev/null")
    if mode == "merge":
        git("fetch", "origin", base_branch())
        base = git("rev-parse", f"origin/{base_branch()}")
        Path(os.environ["RUNNER_TEMP"], "merge-base-sha").write_text(base)
        script = Path(__file__).with_name("sign_commit.py")
    elif mode == "resign":
        # Same base, same order: only the signatures and identity change.
        base = git("merge-base", f"origin/{os.environ['BASE_BRANCH']}", "HEAD")
        script = Path(__file__).with_name("sign_commit.py")
    else:
        base = git("merge-base", f"origin/{os.environ['BASE_BRANCH']}", "HEAD")
        script = Path(__file__).with_name("rewrite_dependabot_commit.py")
    if git("rev-list", "--merges", f"{base}..HEAD"):
        raise ValueError("Merge commits cannot be automatically replayed")
    subprocess.run(
        ["git", "rebase", "--force-rebase", "--empty=keep", base, "--exec", shlex.join(["python3", str(script)])],
        check=True,
    )


def push_signed(mode):
    sha = os.environ.get("EXPECTED_SHA") or os.environ["HEAD_SHA"]
    pr = pull_request()
    branch = os.environ["HEAD_REF"]
    validate_head(sha, branch)
    if mode == "merge":
        if not merge_authorized(pr, sha):
            raise ValueError("Pull request changed or merge authorization was removed")
        check_labeller()
    elif mode == "resign":
        if not (
            pr["state"] == "open"
            and (pr["head"]["repo"] or {}).get("full_name") == repository()
            and pr["head"]["sha"] == sha
            and pr["head"]["ref"] == branch
            and pr["base"]["ref"] == os.environ["BASE_BRANCH"]
        ):
            raise ValueError("Pull request changed while its commits were re-signed")
        check_labeller()
    elif not (
        pr["state"] == "open"
        and pr["user"]["login"] == "dependabot[bot]"
        and (pr["head"]["repo"] or {}).get("full_name") == repository()
        and pr["head"]["sha"] == sha
        and pr["head"]["ref"] == branch
    ):
        raise ValueError("Dependabot PR is stale")
    authenticated_git("push", f"--force-with-lease=refs/heads/{branch}:{sha}", "origin", f"HEAD:refs/heads/{branch}")
    if mode == "resign":
        api(
            f"repos/{repository()}/issues/{pr['number']}/comments",
            "POST",
            {
                "body": f"Re-signed the commits of `{branch}` with the maintainer key after an unsigned push "
                f"by `{os.environ['LABELLER']}`; checks run again on `{git('rev-parse', '--short', 'HEAD')}`."
            },
            token=pr_write_token(),
        )


def validate_resign():
    """Decide whether a push to a pull request branch needs its commits re-signed.

    Only an open pull request from this repository, still at the pushed head,
    qualifies, and only when one of its commits is not verified: the signed
    push this workflow makes then triggers it again with nothing left to do.
    The pusher must have write access, the trust the merge label also asks for.
    """
    sha, branch = os.environ["SIGNAL_SHA"], os.environ["SIGNAL_BRANCH"]
    validate_head(sha, branch)
    owner = repository().split("/")[0]
    pages = api(
        f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}&per_page=100",
        paginate=True,
    )
    pulls = [
        pull
        for page in pages
        for pull in page
        if (pull["head"]["repo"] or {}).get("full_name") == repository()
        and pull["head"]["ref"] == branch
        and branch not in {RELEASE_BRANCH, INTEGRATION_BRANCH}
        and pull["base"]["ref"] in {RELEASE_BRANCH, INTEGRATION_BRANCH}
    ]
    if len(pulls) != 1 or pulls[0]["head"]["sha"] != sha:
        print("The pushed commit is no longer the head of an open pull request")
        output("needed", "false")
        return
    number = pulls[0]["number"]
    output("number", number)
    output("base", pulls[0]["base"]["ref"])
    pages = api(f"repos/{repository()}/pulls/{number}/commits?per_page=100", paginate=True)
    unsigned = [
        commit["sha"][:7]
        for page in pages
        for commit in page
        if not (commit["commit"].get("verification") or {}).get("verified")
    ]
    if not unsigned:
        print(f"Every commit of #{number} is signed")
        output("needed", "false")
        return
    check_labeller()
    print(f"#{number}: re-signing after unsigned commits " + ", ".join(unsigned))
    output("needed", "true")


def authenticated_git(*args):
    subprocess.run(
        ["git", "-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential", *args], check=True
    )


REPOSITORY_CONFIG = Path(__file__).resolve().parent.parent / ".github/repository.toml"
PR_TEMPLATE = Path(__file__).resolve().parent.parent / ".github/pull_request_template.md"


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


def required_checks(config=REPOSITORY_CONFIG):
    """Return the status checks the branch rulesets require before merging."""
    rulesets = tomllib.loads(Path(config).read_text()).get("rulesets", [])
    return {
        check["context"]
        for ruleset in rulesets
        for rule in ruleset.get("rules", [])
        if rule["type"] == "required_status_checks"
        for check in rule["parameters"]["required_status_checks"]
    }


def checks_ready(runs, statuses, required=None):
    required = required_checks() if required is None else set(required)
    # A rerun supersedes older attempts with the same app and check name.
    latest = {}
    for check in runs:
        key = (check.get("app", {}).get("id"), check["name"])
        if key not in latest or check["id"] > latest[key]["id"]:
            latest[key] = check
    checks = [c for c in latest.values() if not c["name"].startswith(MERGE_JOB_PREFIX)]
    if any(c["status"] == "completed" and c["conclusion"] not in {"success", "neutral", "skipped"} for c in checks):
        raise ValueError("A check failed")
    if statuses.get("statuses") and statuses["state"] in {"failure", "error"}:
        raise ValueError("A commit status failed")
    # Every required check must be reported, as the ruleset demands, and at
    # least one must have succeeded so a fully skipped run cannot merge.
    names = {c["name"]: c for c in checks}
    return (
        bool(required)
        and required <= names.keys()
        and any(names[name]["conclusion"] == "success" for name in required)
        and all(c["status"] == "completed" for c in checks)
        and (not statuses.get("statuses") or statuses["state"] == "success")
    )


def wait_checks(signed=False):
    token = os.environ.get("CHECKS_TOKEN")
    if not token:
        raise ValueError("CHECKS_TOKEN is required to read CI with the workflow token")
    sha = git("rev-parse", "HEAD") if signed else os.environ["HEAD_SHA"]
    deadline = time.monotonic() + 25 * 60
    while time.monotonic() < deadline:
        pr = pull_request()
        if signed and sha != os.environ["HEAD_SHA"] and pr["head"]["sha"] == os.environ["HEAD_SHA"]:
            # GitHub updates the pull request head shortly after the signed push.
            time.sleep(5)
            continue
        if not merge_authorized(pr, sha):
            raise ValueError("Pull request changed while waiting for checks")
        pages = api(
            f"repos/{repository()}/commits/{sha}/check-runs?per_page=100&filter=latest", paginate=True, token=token
        )
        runs = [c for page in pages for c in page["check_runs"]]
        statuses = api(f"repos/{repository()}/commits/{sha}/status", token=token)
        if checks_ready(runs, statuses):
            return
        time.sleep(30)
    raise TimeoutError("Timed out waiting for CI; re-apply the auto-merge label")


def queue_ahead(number):
    """Return the labelled pull requests queued before ``number``, oldest first.

    The queue order is the time of each pull request's latest auto-merge label,
    so removing and re-applying the label moves a pull request to the back.
    """
    label = os.environ["MERGE_LABEL"]
    pages = api(f"repos/{repository()}/issues?state=open&labels={quote(label, safe='')}&per_page=100", paginate=True)
    queued = []
    for issue in (issue for page in pages for issue in page if "pull_request" in issue):
        events = api(f"repos/{repository()}/issues/{issue['number']}/events?per_page=100", paginate=True)
        labelled = [
            event["created_at"]
            for page in events
            for event in page
            if event["event"] == "labeled" and (event.get("label") or {}).get("name") == label
        ]
        if labelled:
            queued.append((max(labelled), issue["number"]))
    order = [entry for _, entry in sorted(queued)]
    return order[: order.index(number)] if number in order else order


def pending_release(directory):
    """Return the components ``multicz plan`` would still bump on the latest main."""
    authenticated_git("-C", str(directory), "fetch", "--quiet", "--tags", "--force", "origin", "main")
    git("-C", str(directory), "checkout", "--quiet", "--detach", "FETCH_HEAD")
    return sorted(json.loads(run("multicz", "plan", "--output", "json", cwd=directory))["bumps"])


def develop_behind(directory):
    """True while develop lacks a commit of main: the sync after a release has
    not run yet, and merging into develop now would make the two diverge."""
    for branch in (RELEASE_BRANCH, INTEGRATION_BRANCH):
        authenticated_git(
            "-C", str(directory), "fetch", "--quiet", "origin", f"+refs/heads/{branch}:refs/remotes/origin/{branch}"
        )
    return not is_ancestor(f"origin/{RELEASE_BRANCH}", f"origin/{INTEGRATION_BRANCH}", directory)


def is_ancestor(older, newer, directory=None):
    command = ["git", *(["-C", str(directory)] if directory else []), "merge-base", "--is-ancestor", older, newer]
    return subprocess.run(command, check=False).returncode == 0


def wait_turn(poll=30, limit=5 * 60 * 60):
    """Wait until this pull request heads the queue and main has no pending
    release; a merge into develop also waits until develop contains main."""
    number, directory = pr_number(), Path(os.environ["QUEUE_DIR"])
    deadline = time.monotonic() + limit
    while time.monotonic() < deadline:
        if not merge_authorized(pull_request(), os.environ["HEAD_SHA"]):
            raise ValueError("Pull request changed or merge authorization was removed")
        ahead = queue_ahead(number)
        if ahead:
            print("Waiting for " + ", ".join(f"#{entry}" for entry in ahead) + " ahead in the merge queue")
        else:
            pending = pending_release(directory)
            if pending:
                print(f"Waiting for the release of {', '.join(pending)} on main")
            elif base_branch() == INTEGRATION_BRANCH and develop_behind(directory):
                print("Waiting for develop to be synced with main")
            else:
                print(f"First in the merge queue and {base_branch()} can move")
                return
        time.sleep(poll)
    raise TimeoutError("Timed out waiting in the merge queue")


def check_promotion():
    """A promotion moves main to develop as it is, without rewriting a commit:
    main must be an ancestor of the validated head and every commit in
    between verified."""
    sha = os.environ["HEAD_SHA"]
    if git("rev-parse", "HEAD") != sha:
        raise ValueError("The checkout is not the validated promotion head")
    git("fetch", "origin", RELEASE_BRANCH)
    base = git("rev-parse", f"origin/{RELEASE_BRANCH}")
    if not is_ancestor(base, sha):
        raise ValueError("develop does not contain main; wait for develop to be synced")
    pages = api(f"repos/{repository()}/compare/{base}...{sha}?per_page=100", paginate=True)
    unsigned = [
        commit["sha"][:7]
        for page in pages
        for commit in page["commits"]
        if not (commit["commit"].get("verification") or {}).get("verified")
    ]
    if unsigned:
        raise ValueError("Unverified commits cannot be promoted: " + ", ".join(unsigned))
    Path(os.environ["RUNNER_TEMP"], "merge-base-sha").write_text(base)


def merge():
    sha, base = git("rev-parse", "HEAD"), base_branch()
    pull = pull_request()
    if not merge_authorized(pull, sha):
        raise ValueError("Pull request changed or merge authorization was removed")
    check_labeller()
    git("fetch", "origin", base)
    if git("rev-parse", f"origin/{base}") != Path(os.environ["RUNNER_TEMP"], "merge-base-sha").read_text():
        raise ValueError(f"{base} advanced; re-apply the auto-merge label")
    authenticated_git("push", "origin", f"HEAD:refs/heads/{base}")
    how = "as validated" if is_promotion(pull) else "GPG-signed with the maintainer key"
    api(
        f"repos/{repository()}/issues/{pr_number()}/comments",
        "POST",
        {"body": f"Merged into `{base}` as `{sha}`, {how}. History stays linear."},
        token=pr_write_token(),
    )


PROMOTION_LABEL = "release"
PROMOTION_TITLE = "Promote develop to main"
PROMOTION_MARKER = "<!-- release-promotion -->"
PROMOTION_VALIDATION = (
    "Promotion of `{branch}` at `{sha}` into `{base}`. Once merged, the release jobs on `{base}` bump the "
    "components above and `{branch}` is synced with the release commit."
)
PROMOTION_MERGE = "It is never merged automatically: review it, then add the `{label}` label to fast-forward `{base}`."


def promotion_plan(directory):
    """What promoting develop would release: ``multicz plan`` on develop, and
    the commits develop has that main lacks, oldest first."""
    bumps = json.loads(run("multicz", "plan", "--output", "json", cwd=directory))["bumps"]
    log = run("git", "log", "--reverse", "--format=%H%x09%s", f"origin/{RELEASE_BRANCH}..HEAD", cwd=directory)
    commits = [line.split("\t", 1) for line in log.splitlines() if line]
    return bumps, commits


def promotion_content(bumps, commits):
    """The release plan and the commits, as Markdown."""
    lines = ["### Release plan", ""]
    if bumps:
        lines += ["| Component | Current | Next | Bump |", "|---|---|---|---|"]
        lines += [
            f"| `{name}` | {bump['current_version']} | {bump['next_version']} | {bump['kind']} |"
            for name, bump in sorted(bumps.items())
        ]
    else:
        lines.append("No component would be released.")
    lines += ["", "### Commits", ""]
    lines += [f"- `{sha[:7]}` {subject}" for sha, subject in commits]
    return "\n".join(lines)


def promotion_issue_body(body, content):
    """The issue as its author wrote it, followed by the generated content."""
    head = (body or "").split(PROMOTION_MARKER, 1)[0].rstrip()
    return f"{head}\n\n{PROMOTION_MARKER}\n{content}\n".lstrip()


def open_promotion():
    owner = repository().split("/")[0]
    pages = api(
        f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + INTEGRATION_BRANCH, safe='')}"
        f"&base={RELEASE_BRANCH}&per_page=100",
        paginate=True,
    )
    pulls = [pull for page in pages for pull in page if is_promotion(pull)]
    return pulls[0] if pulls else None


def close_issue(number, message, reason="not_planned"):
    api(f"repos/{repository()}/issues/{number}/comments", "POST", {"body": message})
    api(f"repos/{repository()}/issues/{number}", "PATCH", {"state": "closed", "state_reason": reason})


def supersede_promotions(pull, issue_number):
    """Close the open promotion pull request and every other open promotion
    issue: a new promotion issue replaces them."""
    message = f"Superseded by the promotion proposed in #{issue_number}."
    if pull is not None:
        api(f"repos/{repository()}/issues/{pull['number']}/comments", "POST", {"body": message})
        api(f"repos/{repository()}/pulls/{pull['number']}", "PATCH", {"state": "closed"})
    pages = api(
        f"repos/{repository()}/issues?state=open&labels={quote(PROMOTION_LABEL, safe='')}&per_page=100", paginate=True
    )
    for issue in (issue for page in pages for issue in page if "pull_request" not in issue):
        if str(issue["number"]) != str(issue_number):
            close_issue(issue["number"], message)


def drop_section(body, heading):
    """``body`` without its ``## heading`` section."""
    return re.sub(rf"^## {re.escape(heading)}\n.*?(?=^## |\Z)", "", body, count=1, flags=re.M | re.S)


def assign(number, login):
    if login:
        api(f"repos/{repository()}/issues/{number}/assignees", "POST", {"assignees": [login]})


def promote():
    """Propose develop for release on main: fill the promotion issue and open,
    or refresh, the promotion pull request. Never labels it for merging.

    Triggered by a promotion issue (``ISSUE_NUMBER``), which supersedes the open
    promotion, by hand, or by a push to develop, which only refreshes a promotion
    already open. The issue and the pull request are labelled ``release`` and
    assigned to whoever proposed the release.
    """
    event, issue_number = os.environ["EVENT"], os.environ.get("ISSUE_NUMBER") or None
    directory = Path(os.environ["PROMOTION_DIR"])
    pull = open_promotion()
    linked = re.search(r"Closes #(\d+)", (pull or {}).get("body") or "")
    if event == "issues":
        author = os.environ["ISSUE_AUTHOR"]
        permission = api(f"repos/{repository()}/collaborators/{quote(author, safe='')}/permission")["permission"]
        if permission not in {"admin", "maintain", "write"}:
            close_issue(issue_number, f"Only maintainers can propose a release; `{author}` needs write access.")
            return
        if not linked or linked.group(1) != str(issue_number):
            supersede_promotions(pull, issue_number)
            pull, linked = None, None
    elif event == "push" and pull is None:
        print("No promotion to refresh")
        return
    issue_number = issue_number or (linked.group(1) if linked else None)
    bumps, commits = promotion_plan(directory)
    sha = git("-C", str(directory), "rev-parse", "HEAD")
    if not commits:
        if event == "issues":
            close_issue(issue_number, f"`{INTEGRATION_BRANCH}` has nothing that `{RELEASE_BRANCH}` lacks.")
        print("Nothing to promote")
        return
    content = promotion_content(bumps, commits)
    # A refresh on push keeps the assignees; a new proposal goes to its author.
    proposer = os.environ.get("PROPOSER") if event != "push" else None
    if issue_number is None:
        issue = api(
            f"repos/{repository()}/issues",
            "POST",
            {
                "title": PROMOTION_TITLE,
                "labels": [PROMOTION_LABEL],
                "assignees": [proposer] if proposer else [],
                "body": promotion_issue_body("### Notes\n\n_No response_", content),
            },
        )
        issue_number = issue["number"]
    else:
        assign(issue_number, proposer)
        issue = api(f"repos/{repository()}/issues/{issue_number}")
        body = promotion_issue_body(issue.get("body"), content)
        if body != issue.get("body"):
            api(f"repos/{repository()}/issues/{issue_number}", "PATCH", {"body": body})
    sections = {
        "Description": content,
        "Validation": PROMOTION_VALIDATION.format(branch=INTEGRATION_BRANCH, sha=sha[:7], base=RELEASE_BRANCH)
        + "\n\n"
        + PROMOTION_MERGE.format(label=os.environ["MERGE_LABEL"], base=RELEASE_BRANCH),
        "Related issues": f"Closes #{issue_number}",
    }
    if pull is None:
        template = (
            PR_TEMPLATE.read_text()
            if PR_TEMPLATE.exists()
            else "## Description\n\n## Validation\n\n## Related issues\n"
        )
        pull = api(
            f"repos/{repository()}/pulls",
            "POST",
            {
                "title": PROMOTION_TITLE,
                "head": INTEGRATION_BRANCH,
                "base": RELEASE_BRANCH,
                # Nothing for a contributor to tick on a promotion: no checklist.
                "body": drop_section(fill_sections(template, sections), "Checklist").rstrip() + "\n",
            },
        )
        api(f"repos/{repository()}/issues/{pull['number']}/labels", "POST", {"labels": [PROMOTION_LABEL]})
        assign(pull["number"], proposer)
        api(
            f"repos/{repository()}/issues/{issue_number}/comments",
            "POST",
            {
                "body": f"Promotion proposed in #{pull['number']}. "
                + PROMOTION_MERGE.format(label=os.environ["MERGE_LABEL"], base=RELEASE_BRANCH)
            },
        )
    else:
        # Only the generated sections change, so the pipeline boxes CI ticks stay.
        body = drop_section(fill_sections(pull.get("body") or "", sections), "Checklist")
        if body != pull.get("body"):
            api(f"repos/{repository()}/pulls/{pull['number']}", "PATCH", {"body": body})
    print(f"Promotion #{pull['number']} for issue #{issue_number} at {sha[:7]}")


def sync_develop():
    """Bring develop up to main after a push to main.

    develop normally contains every commit of main but the release commit, so it
    moves forward to main. When it also holds commits of its own (a fix merged
    straight into main), those are replayed on main, re-signed, and develop is
    pushed with a lease. Without a develop branch there is nothing to do.
    """
    try:
        api(f"repos/{repository()}/branches/{INTEGRATION_BRANCH}")
    except subprocess.CalledProcessError:
        print(f"No {INTEGRATION_BRANCH} branch to sync")
        return
    for branch in (RELEASE_BRANCH, INTEGRATION_BRANCH):
        authenticated_git("fetch", "--quiet", "origin", f"+refs/heads/{branch}:refs/remotes/origin/{branch}")
    main, develop = (git("rev-parse", f"origin/{branch}") for branch in (RELEASE_BRANCH, INTEGRATION_BRANCH))
    lease = f"--force-with-lease=refs/heads/{INTEGRATION_BRANCH}:{develop}"
    if is_ancestor(main, develop):
        print(f"{INTEGRATION_BRANCH} already contains {RELEASE_BRANCH}")
        return
    if is_ancestor(develop, main):
        authenticated_git("push", lease, "origin", f"{main}:refs/heads/{INTEGRATION_BRANCH}")
        print(f"{INTEGRATION_BRANCH} moved forward to {RELEASE_BRANCH} at {main[:7]}")
        return
    try:
        fork = git("merge-base", main, develop)
    except subprocess.CalledProcessError:
        raise ValueError(
            f"{INTEGRATION_BRANCH} and {RELEASE_BRANCH} share no history; recreate {INTEGRATION_BRANCH} from "
            f"{RELEASE_BRANCH}"
        ) from None
    if git("rev-list", "--merges", f"{fork}..{develop}"):
        raise ValueError(f"{INTEGRATION_BRANCH} has merge commits; sync it with {RELEASE_BRANCH} by hand")
    git("config", "core.hooksPath", "/dev/null")
    git("checkout", "--quiet", "--detach", develop)
    script = Path(__file__).with_name("sign_commit.py")
    subprocess.run(
        [
            "git",
            "rebase",
            "--force-rebase",
            "--empty=drop",
            "--onto",
            main,
            fork,
            "--exec",
            shlex.join(["python3", str(script)]),
        ],
        check=True,
    )
    authenticated_git("push", lease, "origin", f"HEAD:refs/heads/{INTEGRATION_BRANCH}")
    print(f"{INTEGRATION_BRANCH} replayed on {RELEASE_BRANCH} at {git('rev-parse', '--short', 'HEAD')}")


SECURITY_MARKER = "security-report: "
# Verdicts that win over a tool's later, wrapped or footer lines.
PREFERRED_CONCLUSION = re.compile(
    r"^=+ .*\b\d+ (passed|failed|errors?)\b.* =+$|"
    r"^No script tests affected|"
    r"^Your code is affected by|^No vulnerabilities found|^\d+ vulnerabilit(y|ies) can be fixed"
)
CONCLUSION = re.compile(
    r"no (issues|findings|vulnerabilities|leaks)|issues? (identified|found)|leaks? found|^>> issue:|"
    r"vulnerabilit|findings|all modules verified|checksum mismatch|security error|"
    r"\b\d+ issues?\b",
    re.I,
)
ANSI = re.compile(r"\x1b\[[0-9;]*m")


def headline(output):
    """The tool's own conclusion: its last meaningful output line."""
    lines = [
        line.strip()
        for line in output.splitlines()
        if line.strip() and not re.match(r"(make(\[\d+\])?:|exit status \d+)", line.strip())
    ]
    # go test prints one line per package: count them.
    packages = [line.split()[0] for line in lines if re.match(r"^(ok|FAIL)\s+\S+\s", line)]
    if packages:
        failed = packages.count("FAIL")
        verdict = f"{len(packages) - failed} packages passed"
        return verdict + (f", {failed} failed" if failed else "")
    # A tool may print a footer after its verdict (bandit lists skipped files):
    # prefer the last line that reads as one.
    preferred = [line for line in lines if PREFERRED_CONCLUSION.search(line)]
    verdicts = [line for line in lines if CONCLUSION.search(line)]
    line = (preferred or verdicts or lines or ["no output"])[-1]
    # Drop pytest's ==== framing and a logger prefix such as gitleaks' "2:14PM INF ".
    line = re.sub(r"^=+\s*|\s*=+$", "", line)
    return re.sub(r"^\d{1,2}:\d{2}(?:[AP]M)? [A-Z]{3} ", "", line)[:160]


def write_report(tool, status, conclusion, details, report):
    """The job summary section and the log marker report-jobs reads."""
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as stream:
            stream.write(
                f"### {tool}\n\n**Result:** {status}: {conclusion}\n\n"
                f"<details><summary>Details</summary>\n\n```\n{details}\n```\n\n</details>\n\n"
            )
    # Base64 keeps the report intact: a problem matcher (setup-go's turns any
    # "file.go:1:2:" into an annotation) would otherwise rewrite the line.
    print(SECURITY_MARKER + base64.b64encode(json.dumps(report).encode()).decode())


def scan(tool, command):
    """Run a scanner with machine-readable output and judge it ourselves.

    ``{json}`` in the command, or the ``SCAN_JSON`` variable, names the file the
    scanner writes; otherwise its standard output is the report. Findings are
    normalized, then the exception registry accepts some of them: the step fails
    on any other finding, or when the scanner's output cannot be read.
    """
    with tempfile.TemporaryDirectory() as directory:
        target = Path(directory, "report.json")
        args = [arg.replace("{json}", str(target)) for arg in command]
        result = subprocess.run(
            args,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            env={**os.environ, "SCAN_JSON": str(target)},
        )
        raw = target.read_text() if target.exists() and target.stat().st_size else result.stdout
    if result.stderr.strip():
        print(ANSI.sub("", result.stderr).rstrip())
    error = None
    try:
        findings = scanners.PARSERS[tool](raw)
        entries = scanners.load_registry()
    except (ValueError, KeyError, IndexError, TypeError) as exc:
        findings, entries, error = [], [], f"cannot read the {tool} report: {exc}"
    if error is None and result.returncode and not findings and tool != "govulncheck":
        error = f"{tool} exited with status {result.returncode} without reporting a finding"
    blocking, accepted, expired = scanners.apply_registry(findings, entries)
    for entry in expired:
        annotation("warning", f"{tool} exception {entry['rule']} expired on {entry['expires']}: {entry['reason']}")
    status = "failed" if error or blocking else "passed"
    if error:
        conclusion = error
    elif blocking:
        conclusion = f"{len(blocking)} finding{'s' if len(blocking) > 1 else ''}"
    else:
        conclusion = "no findings"
    if accepted:
        conclusion += f", {len(accepted)} accepted by exception"
    if expired:
        conclusion += f", {len(expired)} expired exception{'s' if len(expired) > 1 else ''}"
    lines = [finding.describe() for finding in blocking] + [
        f"{finding.describe()} (accepted until {finding.exception['expires']})" for finding in accepted
    ]
    print("\n".join(lines) or conclusion)
    report = {"tool": tool, "status": status, "summary": conclusion}
    if blocking or error:
        report["findings"] = ([error] if error else []) + [finding.describe() for finding in blocking][:20]
    report["items"] = [
        {
            "rule": finding.rule,
            "path": finding.path,
            "line": finding.line,
            "message": finding.message,
            "state": "accepted" if finding.exception else "blocking",
        }
        for finding in blocking + accepted
    ][:50]
    write_report(tool, status, conclusion, "\n".join(lines) or conclusion, report)
    return 1 if status == "failed" else 0


def security(tool, command, tail=60):
    """Run one check and report it in the job summary and the job log.

    Security scanners go through ``scan``, which applies the exception
    registry. Other checks (pytest, go test, ruff, go mod verify) keep their own
    exit status; the summary gets the end of their output and the log gets one
    ``security-report:`` line that report-jobs copies to the pull request.
    """
    if tool in scanners.PARSERS:
        return scan(tool, command)
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    output = ANSI.sub("", result.stdout)
    print(output, end="" if output.endswith("\n") else "\n")
    status = "passed" if result.returncode == 0 else "failed"
    conclusion = headline(output)
    excerpt = "\n".join(output.rstrip().splitlines()[-tail:])
    report = {"tool": tool, "status": status, "summary": conclusion}
    if result.returncode:
        # What the tool found, so its own pull request comment can quote it.
        report["findings"] = error_lines(output, limit=20)
    write_report(tool, status, conclusion, excerpt, report)
    return result.returncode


def security_reports(log):
    """The security tool results a job recorded in its log, in order."""
    reports = []
    for line in log.splitlines():
        # The runner may prefix the line (##[error] from a problem matcher).
        start = line.find(SECURITY_MARKER)
        if start < 0:
            continue
        try:
            encoded = line[start + len(SECURITY_MARKER) :].strip()
            reports.append(json.loads(base64.b64decode(encoded, validate=True)))
        except (ValueError, json.JSONDecodeError):
            continue
    return reports


def job_log(job_id):
    """A job's log, read through the REST API rather than gh, which refuses logs
    holding terminal escape sequences. The token goes to GitHub only: the log
    itself is served from a storage host the API redirects to."""
    base = os.environ.get("GITHUB_API_URL", "https://api.github.com")
    request = urllib.request.Request(
        f"{base}/repos/{repository()}/actions/jobs/{int(job_id)}/logs",
        headers={"Accept": "application/vnd.github+json"},
    )
    token = os.environ.get("LOGS_TOKEN") or os.environ["GH_TOKEN"]
    request.add_unredirected_header("Authorization", f"Bearer {token}")
    # The URL is built from the trusted API base and a numeric job id.
    with urllib.request.urlopen(request, timeout=60) as response:  # nosec B310
        return response.read().decode("utf-8", "replace")


def annotation(kind, message):
    """A GitHub Actions annotation; newlines are encoded so it stays one entry."""
    text = str(message).replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    print(f"::{kind}::{text}")


JOB_MARKER = "<!-- ci-job: {} -->"
CHECK_MARKER = "<!-- ci-check: {} / {} -->"
ERROR_LINE = re.compile(
    r"##\[error\]|^FAILED |^E\s|^--- FAIL|^FAIL\b|\b(?:Error|error|ERROR):"
    # What the security tools found: zizmor, govulncheck, gitleaks, osv-scanner,
    # bandit, golangci-lint, and a go.sum checksum the module download rejects.
    r"|^(?:error|warning)\[[\w-]+\]|^\s*-->|^Vulnerability #\d+|^\s*(?:Found in|Fixed in|#\d+):|"
    r"^\s*(?:RuleID|File|Line|Finding):|^\| https://osv\.dev/|^>> Issue:|^\s*Location:|"
    r"^\S+\.go:\d+:\d+: |checksum mismatch|SECURITY ERROR"
)
TIMESTAMP = re.compile(r"^\d{4}-\d\d-\d\dT[\d:.]+Z ")


def error_lines(log, limit=30):
    """The lines of a job log that explain its failure, without timestamps or repeats."""
    lines = []
    for raw in log.splitlines():
        line = TIMESTAMP.sub("", raw).replace("##[error]", "").rstrip()
        if ERROR_LINE.search(TIMESTAMP.sub("", raw)) and line.strip() and line not in lines:
            lines.append(line[:200])
    return lines[-limit:]


def token_owner():
    """The account behind GH_TOKEN, the maintainer's PAT, or ``None`` when it
    cannot tell (the workflow token cannot read /user)."""
    try:
        return api("user")["login"]
    except (subprocess.CalledProcessError, KeyError, TypeError):
        return None


def comment_author():
    """The account report comments are posted as: the PAT's owner, or the
    Actions bot when the workflow token is used (it cannot read /user)."""
    try:
        return api("user")["login"]
    except (subprocess.CalledProcessError, KeyError, TypeError):
        return "github-actions[bot]"


def report_jobs():
    """Keep one pull request comment per CI check, updated in place.

    Each security tool a job ran gets its own comment: passed or failed, its
    verdict and, on failure, what it found. A job gets a comment of its own when
    it runs no security tool, or when it fails outside one (a test, a build);
    such a comment is later marked passed rather than left behind. Later runs
    edit the same comments, only when they change. Skipped or cancelled jobs
    are left alone.
    """
    number, sha = pr_number(), os.environ["HEAD_SHA"][:7]
    run_jobs = api(
        f"repos/{repository()}/actions/runs/{os.environ['RUN_ID']}/attempts/"
        f"{os.environ['RUN_ATTEMPT']}/jobs?per_page=100",
        paginate=True,
        token=os.environ.get("LOGS_TOKEN") or None,
    )
    authors = {"github-actions[bot]", comment_author()}
    jobs = [
        job
        for page in run_jobs
        for job in page["jobs"]
        if job["name"] != os.environ["REPORT_JOB"] and job["status"] == "completed"
    ]
    pages = api(f"repos/{repository()}/issues/{number}/comments?per_page=100", paginate=True)
    existing = {
        comment["body"].splitlines()[0]: comment
        for page in pages
        for comment in page
        if comment["user"]["login"] in authors and comment["body"].startswith("<!-- ci-")
    }
    findings, tools = [], {}
    for job in jobs:
        if job["conclusion"] not in {"success", "failure", "timed_out"}:
            continue
        try:
            log, unavailable = job_log(job["id"]), False
        except (urllib.error.URLError, OSError, ValueError) as error:
            annotation("warning", f"Cannot read the log of {job['name']}: {error}")
            log, unavailable = "", True
        link = f"[Job logs]({job['html_url']})"
        failed = job["conclusion"] != "success"
        reports = security_reports(log)
        tools.update({(job["name"], report["tool"]): report["status"] for report in reports})
        findings += [
            {**item, "tool": report["tool"]}
            for report in reports
            for item in report.get("items", [])
            if item.get("state") == "blocking"
        ]
        posts = []
        for report in reports:
            passed = report["status"] == "passed"
            body = (
                f"{CHECK_MARKER.format(job['name'], report['tool'])}\n{'✅' if passed else '🟥'} "
                f"**`{report['tool']}`** {report['status']} on `{sha}` (job `{job['name']}`): "
                f"{report['summary']}\n\n"
            )
            if not passed and report.get("findings"):
                body += "```\n" + "\n".join(report["findings"]) + "\n```\n\n"
            posts.append(body + link)
        marker = JOB_MARKER.format(job["name"])
        tool_failed = any(report["status"] == "failed" for report in reports)
        if not reports or (failed and not tool_failed) or unavailable or marker in existing:
            body = (
                f"{marker}\n{'🟥' if failed else '✅'} "
                f"**CI job `{job['name']}` {'failed' if failed else 'passed'}** on `{sha}`"
            )
            steps = [step["name"] for step in job.get("steps", []) if step["conclusion"] == "failure"]
            body += (f", in step `{steps[0]}`." if failed and steps else ".") + "\n\n"
            lines = error_lines(log) if failed and not tool_failed else []
            if lines:
                body += "```\n" + "\n".join(lines) + "\n```\n\n"
            if unavailable:
                body += "The job log could not be read, so its details are missing here.\n\n"
            posts.append(body + link)
        for body in posts:
            comment = existing.get(body.splitlines()[0])
            if comment and comment["body"] == body:
                continue
            # One comment that cannot be written never stops the other reports.
            try:
                if comment:
                    api(f"repos/{repository()}/issues/comments/{comment['id']}", "PATCH", {"body": body})
                else:
                    api(f"repos/{repository()}/issues/{number}/comments", "POST", {"body": body})
            except subprocess.CalledProcessError:
                annotation("warning", f"Cannot write a report comment for {job['name']}")
    try:
        review_findings(number, sha, findings)
    except subprocess.CalledProcessError:
        annotation("warning", "Cannot write the review of the scanner findings")
    try:
        update_pipeline_checklist(number, {job["name"]: job["conclusion"] for job in jobs}, tools)
    except (subprocess.CalledProcessError, KeyError, TypeError, ValueError) as error:
        annotation("warning", f"Cannot update the pipeline checklist: {error}")


CHECKLIST_BOX = re.compile(r"^(\s*[-*] \[)[ xX](\] .*?<!-- ci-box: (.+?) -->)(.*)$", re.MULTILINE)
DETECTION_JOB = "detect changed components"


def checklist_state(key, conclusions, tools):
    """``(checked, note)`` for one checklist box, or ``None`` to leave it alone.

    A box names a job, or a ``job / tool`` the job reports on. A job skipped
    because detection found nothing for it is not needed; one skipped after an
    upstream failure did not run.
    """
    job, _, tool = key.partition(" / ")
    conclusion = conclusions.get(job)
    if conclusion is None or conclusion == "cancelled":
        return None
    if conclusion == "skipped":
        if conclusions.get(DETECTION_JOB) == "success":
            return True, "not needed"
        return False, "not run"
    if tool:
        status = tools.get((job, tool))
        if status is None:
            return None if conclusion == "success" else (False, "not run")
        return status == "passed", status
    return conclusion == "success", "passed" if conclusion == "success" else "failed"


def update_pipeline_checklist(number, conclusions, tools):
    """Tick the pipeline boxes of the pull request description from this run.

    Only lines carrying a ``<!-- ci-box: ... -->`` marker change, and only
    while this run's commit is still the pull request head; a description
    without markers is left untouched.
    """
    pull = api(f"repos/{repository()}/pulls/{number}")
    head = os.environ["HEAD_SHA"]
    if pull["head"]["sha"] != head:
        return
    body = pull.get("body") or ""

    def box(match):
        state = checklist_state(match.group(3).strip(), conclusions, tools)
        if state is None:
            return match.group(0)
        checked, note = state
        return f"{match.group(1)}{'x' if checked else ' '}{match.group(2)} {note} on `{head[:7]}`"

    updated = CHECKLIST_BOX.sub(box, body.replace("\r\n", "\n"))
    if updated != body.replace("\r\n", "\n"):
        api(f"repos/{repository()}/pulls/{number}", "PATCH", {"body": updated})


FINDING_MARKER = "<!-- ci-finding: {} -->"
RESOLVED_MARKER = "<!-- ci-finding-resolved -->"


def finding_key(finding):
    return "|".join(str(finding[field]) for field in ("tool", "rule", "path", "line"))


def commentable_lines(patch):
    """The new-side line numbers a review comment may target in one file's diff."""
    lines, current = set(), 0
    for line in (patch or "").splitlines():
        hunk = re.match(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@", line)
        if hunk:
            current = int(hunk.group(1))
        elif line.startswith("-") or line.startswith("\\"):
            continue
        elif current:
            lines.add(current)
            current += 1
    return lines


def finding_comment(finding):
    return (
        f"{FINDING_MARKER.format(finding_key(finding))}\n"
        f"🟥 **`{finding['tool']}`** `{finding['rule']}`: {finding['message']}\n\n"
        f"To accept this finding, reply `/exception <days>d <reason>` (at most {scanners.MAX_DAYS} days); "
        "otherwise fix it in this pull request."
    )


def review_findings(number, sha, findings):
    """Open one review thread per scanner finding, on its line when the diff has it.

    Only findings without a thread yet are posted; a finding that no longer
    shows up gets one reply saying it is resolved. A finding in a file this pull
    request does not change cannot have a thread: the check's report lists it.
    """
    pages = api(f"repos/{repository()}/pulls/{number}/comments?per_page=100", paginate=True)
    comments = [comment for page in pages for comment in page]
    authors = {"github-actions[bot]", comment_author()}
    ours = {}
    for comment in comments:
        marker = re.match(r"<!-- ci-finding: (.+?) -->", comment["body"])
        if marker and comment["user"]["login"] in authors:
            ours[marker.group(1)] = comment
    resolved = {comment["in_reply_to_id"] for comment in comments if RESOLVED_MARKER in comment["body"]}
    current = {finding_key(finding): finding for finding in findings}
    for key, comment in ours.items():
        if key not in current and comment["id"] not in resolved:
            api(
                f"repos/{repository()}/pulls/{number}/comments/{comment['id']}/replies",
                "POST",
                {"body": f"{RESOLVED_MARKER}\n✅ No longer reported on `{sha}`."},
            )
    new = [finding for key, finding in current.items() if key not in ours]
    if not new:
        return
    files = api(f"repos/{repository()}/pulls/{number}/files?per_page=100", paginate=True)
    lines = {item["filename"]: commentable_lines(item.get("patch")) for page in files for item in page}
    for finding in new:
        if finding["path"] not in lines:
            continue
        thread = {"commit_id": os.environ["HEAD_SHA"], "path": finding["path"], "body": finding_comment(finding)}
        if finding["line"] and finding["line"] in lines[finding["path"]]:
            thread.update(line=finding["line"], side="RIGHT")
        else:
            # No line, or one the diff does not show: the thread goes on the file.
            thread["subject_type"] = "file"
        api(f"repos/{repository()}/pulls/{number}/comments", "POST", thread)


EXCEPTION_COMMAND = re.compile(r"^/exception\s+(\d+)d\s+(\S.*)$")
EXCEPTION_DONE = "<!-- ci-exception: {} -->"


def pull_request_for(branch):
    """The open pull request from this repository for ``branch``, into main or
    develop. main and develop themselves never get commits pushed this way."""
    subprocess.run(["git", "check-ref-format", f"refs/heads/{branch}"], check=True)
    owner = repository().split("/")[0]
    pages = api(
        f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}&per_page=100",
        paginate=True,
    )
    pulls = [
        pull
        for page in pages
        for pull in page
        if (pull["head"]["repo"] or {}).get("full_name") == repository()
        and pull["head"]["ref"] == branch
        and branch not in {RELEASE_BRANCH, INTEGRATION_BRANCH}
        and pull["base"]["ref"] in {RELEASE_BRANCH, INTEGRATION_BRANCH}
    ]
    return pulls[0] if len(pulls) == 1 else None


def pending_exceptions(number):
    """``/exception`` replies to finding threads that got no answer yet."""
    pages = api(f"repos/{repository()}/pulls/{number}/comments?per_page=100", paginate=True)
    comments = [comment for page in pages for comment in page]
    authors = {"github-actions[bot]", comment_author()}
    findings = {}
    for comment in comments:
        marker = re.match(r"<!-- ci-finding: (.+?) -->", comment["body"])
        if marker and comment["user"]["login"] in authors:
            findings[comment["id"]] = marker.group(1)
    answered = {
        int(match)
        for comment in comments
        if comment["user"]["login"] in authors
        for match in re.findall(r"<!-- ci-exception: (\d+) -->", comment["body"])
    }
    return [
        (comment, findings[comment["in_reply_to_id"]])
        for comment in comments
        if comment["body"].strip().startswith("/exception")
        and comment.get("in_reply_to_id") in findings
        and comment["id"] not in answered
    ]


def exception_pending():
    """Decide whether a review comment asks for an exception that is still to add."""
    # A review comment run's SHA is not reliably the head: use the branch.
    pull = pull_request_for(os.environ["SIGNAL_BRANCH"])
    if pull is None:
        print("No open pull request from this repository for the commented branch")
        output("needed", "false")
        return
    pending = pending_exceptions(pull["number"])
    output("number", pull["number"])
    output("sha", pull["head"]["sha"])
    output("needed", "true" if pending else "false")
    print(f"#{pull['number']}: {len(pending)} exception request(s) to handle")


def exception_apply(today=None):
    """Add the requested exceptions to the registry in one signed commit.

    Each request is a reply to a finding thread: `/exception <days>d <reason>`
    from someone with write access, for at most MAX_DAYS days. Every request
    gets an answer in its thread, whether it was applied or refused.
    """
    today = today or datetime.date.today()
    number = pr_number()
    pull = pull_request()
    branch, sha = os.environ["HEAD_REF"], os.environ["EXPECTED_SHA"]
    validate_head(sha, branch)
    if pull["state"] != "open" or pull["head"]["sha"] != sha or pull["head"]["ref"] != branch:
        raise ValueError("Pull request changed before its exceptions were added")
    registry = Path(".github/exceptions.toml")
    text = registry.read_text() if registry.exists() else ""
    answers, added = [], []
    for comment, key in pending_exceptions(number):
        tool, rule, path, _line = key.split("|", 3)
        login = comment["user"]["login"]
        command = EXCEPTION_COMMAND.match(comment["body"].strip().splitlines()[0])
        permission = api(f"repos/{repository()}/collaborators/{quote(login, safe='')}/permission")["permission"]
        if permission not in {"admin", "maintain", "write"}:
            answers.append((comment, f"🟥 `{login}` needs write access to add an exception."))
        elif not command or not 1 <= int(command.group(1)) <= scanners.MAX_DAYS:
            answers.append(
                (
                    comment,
                    f"🟥 Use `/exception <days>d <reason>` with 1 to {scanners.MAX_DAYS} days, e.g. `/exception 7d why`.",
                )
            )
        else:
            days, reason = int(command.group(1)), command.group(2).strip()
            entry = {
                "tool": tool,
                "rule": rule,
                "path": path,
                "reason": f"{reason} (requested by {login} in #{number})",
                "added": today,
                "expires": today + datetime.timedelta(days=days),
            }
            text = scanners.add_exception(text, entry)
            added.append((comment, entry))
    if added:
        registry.write_text(text)
        scanners.load_registry(registry)
        git("add", str(registry))
        tools = sorted({entry["tool"] for _, entry in added})
        subject = (
            f"chore: accept {', '.join(tools)} finding{'s' if len(added) > 1 else ''} until {added[0][1]['expires']}"
        )
        key = git("config", "--get", "user.signingkey")
        git("-c", "core.hooksPath=/dev/null", "commit", "--quiet", f"--gpg-sign={key}", "--message", subject[:72])
        git("verify-commit", "HEAD")
        authenticated_git(
            "push", f"--force-with-lease=refs/heads/{branch}:{sha}", "origin", f"HEAD:refs/heads/{branch}"
        )
        head = git("rev-parse", "--short", "HEAD")
        answers += [
            (
                comment,
                f"✅ Exception added for `{entry['tool']}` `{entry['rule']}` in `{entry['path']}` until "
                f"{entry['expires']}, in `{head}`; checks run again.",
            )
            for comment, entry in added
        ]
    for comment, answer in answers:
        api(
            f"repos/{repository()}/pulls/{number}/comments/{comment['in_reply_to_id']}/replies",
            "POST",
            {"body": f"{EXCEPTION_DONE.format(comment['id'])}\n{answer}"},
            token=pr_write_token(),
        )
    resolve_threads(number, {comment["in_reply_to_id"] for comment, _ in added})


THREADS_QUERY = """
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes { id isResolved comments(first: 1) { nodes { databaseId } } }
      }
    }
  }
}
"""
RESOLVE_THREAD = "mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id } } }"


def resolve_threads(number, root_ids):
    """Resolve the review threads started by the comments ``root_ids``."""
    if not root_ids:
        return
    owner, name = repository().split("/")
    data = api(
        "graphql",
        "POST",
        {"query": THREADS_QUERY, "variables": {"owner": owner, "name": name, "number": number}},
        token=pr_write_token(),
    )
    for thread in data["data"]["repository"]["pullRequest"]["reviewThreads"]["nodes"]:
        roots = [comment["databaseId"] for comment in thread["comments"]["nodes"]]
        if not thread["isResolved"] and roots and roots[0] in root_ids:
            api("graphql", "POST", {"query": RESOLVE_THREAD, "variables": {"id": thread["id"]}}, token=pr_write_token())


REVIEW_MARKER = "<!-- exception-review: {} -->"
REVIEW_LABEL = "security-exception"


def exceptions_review(today=None, horizon=7):
    """Open an issue and a pull request for each exception about to expire.

    The pull request removes the exception, so its checks fail again on the
    finding: the maintainer fixes the code in it, or replies `/exception` to the
    finding to renew the exception with a new date and reason.
    """
    today = today or datetime.date.today()
    # Work lands on develop once it exists, so the registry is reviewed there.
    has_develop = subprocess.run(
        ["git", "rev-parse", "--verify", "--quiet", f"refs/remotes/origin/{INTEGRATION_BRANCH}"],
        check=False,
        capture_output=True,
    )
    base = INTEGRATION_BRANCH if has_develop.returncode == 0 else os.environ["BASE_BRANCH"]
    if base == INTEGRATION_BRANCH:
        git("checkout", "--quiet", "--detach", f"origin/{base}")
    due = [
        entry
        for entry in scanners.load_registry(Path(".github/exceptions.toml"))
        if entry["expires"] <= today + datetime.timedelta(days=horizon)
    ]
    if not due:
        print(f"No exception expires within {horizon} days")
        return
    owner = repository().split("/")[0]
    pages = api(f"repos/{repository()}/issues?state=open&labels={REVIEW_LABEL}&per_page=100", paginate=True)
    issues = [issue for page in pages for issue in page if "pull_request" not in issue]
    signing_key = git("config", "--get", "user.signingkey")
    maintainer = token_owner()
    for entry in due:
        tool, rule, path = scanners.entry_key(entry)
        key = "|".join((tool, rule, path))
        branch = "exceptions/review-" + re.sub(r"[^a-z0-9]+", "-", key.lower()).strip("-")[:60]
        state = "expired" if entry["expires"] < today else "expires"
        issue = next((item for item in issues if REVIEW_MARKER.format(key) in (item.get("body") or "")), None)
        if issue is None:
            issue = api(
                f"repos/{repository()}/issues",
                "POST",
                {
                    "title": f"Security exception {tool} {rule} {state} on {entry['expires']}",
                    "labels": [REVIEW_LABEL],
                    "assignees": [maintainer] if maintainer else [],
                    "body": f"{REVIEW_MARKER.format(key)}\nThe `{tool}` exception for `{rule}`"
                    + (f" in `{path}`" if path else "")
                    + f" {state} on {entry['expires']}.\n\nReason given: {entry['reason']}\n\n"
                    "Its review pull request removes it: fix the finding there, or reply "
                    "`/exception <days>d <reason>` to the finding to renew it.",
                },
            )
        open_pulls = api(
            f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}", paginate=True
        )
        if any(page for page in open_pulls):
            print(f"{key}: review pull request already open")
            continue
        git("checkout", "--quiet", "-B", branch, f"origin/{base}")
        registry = Path(".github/exceptions.toml")
        registry.write_text(scanners.remove_exception(registry.read_text(), (tool, rule, path)))
        git("add", str(registry))
        git(
            "-c",
            "core.hooksPath=/dev/null",
            "commit",
            "--quiet",
            f"--gpg-sign={signing_key}",
            "--message",
            f"chore: review the {tool} {rule} exception"[:72],
        )
        authenticated_git("push", "--force", "origin", f"HEAD:refs/heads/{branch}")
        pull = api(
            f"repos/{repository()}/pulls",
            "POST",
            {
                "title": f"Review the {tool} {rule} exception",
                "head": branch,
                "base": base,
                "body": f"Removes the `{tool}` exception for `{rule}`"
                + (f" in `{path}`" if path else "")
                + f", which {state} on {entry['expires']}. The checks report the finding again: fix it here, "
                "or reply `/exception <days>d <reason>` to its review comment to renew the exception."
                f"\n\nCloses #{issue['number']}",
            },
        )
        api(f"repos/{repository()}/issues/{pull['number']}/labels", "POST", {"labels": [REVIEW_LABEL]})
        assign(pull["number"], maintainer)
        print(f"{key}: issue #{issue['number']}, pull request #{pull['number']}")
    git("checkout", "--quiet", "--detach", f"origin/{base}")


def summary(title, body=""):
    text = f"## {title}\n\n**Result:** {os.environ.get('JOB_STATUS', 'unknown')}\n"
    if "PR_NUMBER" in os.environ or "PR" in os.environ:
        number = os.environ.get("PR_NUMBER") or os.environ.get("PR") or "not validated"
        text += f"\nPull request: {number}\n"
    if os.environ.get("GIT_USER_NAME") or os.environ.get("GIT_USER_EMAIL"):
        text += f"\nIdentity: {os.environ.get('GIT_USER_NAME', '')} <{os.environ.get('GIT_USER_EMAIL', '')}>\n"
    if body:
        text += f"\n{body}\n"
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as stream:
        stream.write(text)


def ci_comparison():
    event = os.environ.get("GITHUB_EVENT_NAME")
    reference = os.environ.get("PR_BASE_SHA" if event == "pull_request" else "PUSH_BEFORE_SHA", "")
    if event not in {"pull_request", "push"} or not re.fullmatch(r"[0-9a-f]{40}", reference) or reference == "0" * 40:
        return None
    try:
        git("merge-base", "--is-ancestor", reference, "HEAD")
    except subprocess.CalledProcessError:
        return None
    return reference


# Changes to these files affect every script test, so the whole suite runs.
SHARED_SCRIPT_FILES = {"scripts/ci_automation.py", "scripts/pyproject.toml", "scripts/uv.lock", "pytest.ini"}


def script_tests(since):
    """Test files to run for a change, or None when the whole suite must run."""
    if not since:
        return None
    files = git("diff", "--name-only", since, "HEAD").splitlines()
    selected = set()
    for path in files:
        if not path.startswith("scripts/") or path == "scripts/CHANGELOG.md":
            continue
        if path in SHARED_SCRIPT_FILES or path.startswith("scripts/tests/conftest"):
            return None
        if path.startswith("scripts/tests/test_") and path.endswith(".py"):
            selected.add(path)
        elif path.startswith("scripts/") and path.count("/") == 1 and path.endswith(".py"):
            module = Path(path).stem
            matches = [
                str(test)
                for test in sorted(Path("scripts/tests").glob("test_*.py"))
                if re.search(rf"^\s*import {re.escape(module)}\b", test.read_text(), re.M)
            ]
            if not matches:
                return None
            selected.update(matches)
        else:
            return None
    return sorted(selected)


def changed_components(since=None, ci=False):
    """Components with commits since their latest tag, as multicz computes them.

    The same rule drives every job, so a commit that changes no component
    (a chore, for example) runs no validation and no release.
    """
    if ci and since:
        raise ValueError("Use either --ci or --since")
    command = ["multicz", "changed", "--output", "json"]
    if since:
        command += ["--since", since]
    changed = json.loads(run(*command))["changed"]
    if not isinstance(changed, list) or not all(isinstance(name, str) for name in changed):
        raise ValueError("Invalid multicz component list")
    encoded = json.dumps(changed, separators=(",", ":"))
    print(f"Changed components: {encoded}")
    output("changed", encoded)
    if ci:
        config = tomllib.loads(Path("multicz.toml").read_text())
        registered = config.get("plugins", {}).get("go-deps", {}).get("packages", {})
        output("go", json.dumps([name for name in changed if name in registered], separators=(",", ":")))
        # Base for selecting script test files only; it does not decide which components changed.
        output("base", ci_comparison() or "")
    listed = "\n".join(f"- `{name}`" for name in changed) if changed else "No changed components."
    summary("Changed components", f"Each component's commits since its latest tag.\n\n{listed}")


def pushed_commits():
    """Return the commits a push brought, or its head when there is no usable range."""
    before, head = os.environ.get("PUSH_BEFORE_SHA", ""), os.environ["HEAD_SHA"]
    if re.fullmatch(r"[0-9a-f]{40}", before) and before != "0" * 40:
        try:
            commits = api(f"repos/{repository()}/compare/{before}...{head}")["commits"]
        except subprocess.CalledProcessError:
            # A force push that shares no history with the previous head: every
            # commit reachable from the new head is new.
            pages = api(f"repos/{repository()}/commits?sha={head}&per_page=100", paginate=True)
            return [commit for page in pages for commit in page]
        if commits:
            return commits
    return [api(f"repos/{repository()}/commits/{head}")]


def verify_signatures():
    """Every commit of the pull request or push must carry a signature GitHub verifies."""
    if os.environ.get("GITHUB_EVENT_NAME") == "push":
        commits = pushed_commits()
    else:
        pages = api(f"repos/{repository()}/pulls/{pr_number()}/commits?per_page=100", paginate=True)
        commits = [commit for page in pages for commit in page]
    if not commits:
        raise ValueError("No commits found for this pull request")
    failures = []
    for commit in commits:
        verification = commit["commit"].get("verification") or {}
        subject = commit["commit"]["message"].splitlines()[0]
        if verification.get("verified") is True:
            print(f"ok {commit['sha'][:10]} {subject}")
        else:
            reason = verification.get("reason", "unknown")
            failures.append(f"{commit['sha'][:10]} ({reason}) {subject}")
            print(f"UNSIGNED {commit['sha'][:10]} ({reason}) {subject}")
    summary(
        "Commit signatures",
        f"{len(commits)} commits checked.\n\n"
        + ("\n".join(f"- `{failure}`" for failure in failures) or "All signatures verified."),
    )
    if failures:
        raise ValueError("Unverified commit signatures: " + ", ".join(failures))


def script_checks(since):
    """Byte-compile every script, then run the pytest files the change affects."""
    subprocess.run(["python3", "-m", "compileall", "-q", "scripts", "-x", "/\\.venv/"], check=True)
    tests = script_tests(since)
    if tests is not None and not tests:
        print("No script tests affected by this change.")
        return
    command = ["uv", "run", "--project", "scripts", "--locked", "pytest"]
    print("Script tests: " + ("full suite" if tests is None else ", ".join(tests)))
    subprocess.run(command + (tests or []), check=True)


def command_actions(args):
    """Every command, by name; ``args`` is read only when one runs."""
    return {
        "validate-dependabot": validate_dependabot,
        "validate-resign": validate_resign,
        "exception-pending": exception_pending,
        "exception-apply": exception_apply,
        "exceptions-review": exceptions_review,
        "label-dependabot": label_dependabot,
        "report-jobs": report_jobs,
        "validate-merge": validate_merge,
        "configure-signing": configure_signing,
        "cleanup": cleanup,
        "rebase": lambda: rebase(args.mode),
        "push": lambda: push_signed(args.mode),
        "wait": lambda: wait_checks(args.signed),
        "merge": merge,
        "check-promotion": check_promotion,
        "sync-develop": sync_develop,
        "promote": promote,
        "queue": wait_turn,
        "summary": lambda: summary(args.title, args.body),
        "changed-components": lambda: changed_components(args.since, args.ci),
        "script-checks": lambda: script_checks(args.since),
        "verify-signatures": verify_signatures,
    }


def main():
    if sys.argv[1:2] == ["security"]:
        args = sys.argv[2:]
        if len(args) < 3 or args[1] != "--":
            raise SystemExit("usage: ci_automation.py security <tool> -- <command...>")
        raise SystemExit(security(args[0], args[2:]))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=sorted(command_actions(None)))
    parser.add_argument("--mode", choices=["merge", "dependabot", "resign"], default="dependabot")
    parser.add_argument("--signed", action="store_true")
    parser.add_argument("--title", default="GitHub automation")
    parser.add_argument("--body", default="", help="Additional job summary text")
    parser.add_argument("--since", help="Git reference for multicz component comparison")
    parser.add_argument(
        "--ci", action="store_true", help="Compare the current CI event; validate all when no safe base exists"
    )
    args = parser.parse_args()
    actions = command_actions(args)
    try:
        actions[args.command]()
    except (ValueError, TimeoutError, subprocess.CalledProcessError) as error:
        if args.mode == "merge" and args.command in {
            "validate-merge",
            "queue",
            "wait",
            "rebase",
            "push",
            "check-promotion",
            "merge",
        }:
            # Retrying is explicit: fix the problem, then re-apply the label.
            # No PR code runs here, including when a rebase fails.
            try:
                api(
                    f"repos/{repository()}/issues/{pr_number()}/comments",
                    "POST",
                    {
                        "body": f"auto-merge-signed: {error}. Fix the problem and re-apply the `{os.environ['MERGE_LABEL']}` label."
                    },
                    token=pr_write_token(),
                )
                api(
                    f"repos/{repository()}/issues/{pr_number()}/labels/{quote(os.environ['MERGE_LABEL'], safe='')}",
                    "DELETE",
                    token=pr_write_token(),
                )
            except subprocess.CalledProcessError:
                pass
        # An expected failure is a clear annotation, never a traceback, and
        # never the output of a command, which may hold a token.
        if isinstance(error, subprocess.CalledProcessError):
            error = f"`{error.cmd[0]}` failed with exit status {error.returncode}"
        annotation("error", error)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
