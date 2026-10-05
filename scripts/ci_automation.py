#!/usr/bin/env python3

# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Chris <goabonga@pm.me>

"""Trusted GitHub automation; checked-out PR files are data, never executed."""

import argparse
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile
import time
import tomllib
from urllib.parse import quote


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
    pages = api(f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}&per_page=100", paginate=True)
    matches = [p for page in pages for p in page
               if p["user"]["login"] == "dependabot[bot]"
               and (p["head"]["repo"] or {}).get("full_name") == repository()
               and p["head"]["sha"] == sha and p["head"]["ref"] == branch
               and p["base"]["ref"] == os.environ["BASE_BRANCH"]]
    if len(matches) != 1:
        raise ValueError("Expected one current same-repository Dependabot PR")
    output("number", matches[0]["number"])


MAJOR_UPDATE = "update-type: version-update:semver-major"
ANNOUNCED_UPDATE = re.compile(r"^(?:Updates `([^`\s]+)`|Bumps \[([^\]\s]+)\]\([^)]*\)) from v?(\S+?) to v?(\S+?)\.?$", re.M)


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
    return (pr["state"] == "open" and pr["user"]["login"] == "dependabot[bot]"
            and (pr["head"]["repo"] or {}).get("full_name") == repository()
            and pr["head"]["ref"].startswith("dependabot/") and pr["base"]["ref"] == "main")


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
    pages = api(f"repos/{repository()}/pulls?state=open&head={quote(owner + ':' + branch, safe='')}&per_page=100", paginate=True)
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
            api(f"repos/{repository()}/issues/{pr['number']}/comments", "POST",
                {"body": f"{MAJOR_NOTICE}\nThis pull request contains a major update, so it is not merged "
                         f"automatically. Review it, then add the `{os.environ['MERGE_LABEL']}` label to merge "
                         "it through the signed merge queue."},
                token=pr_write_token())
        print(f"#{pr['number']} is a major update and waits for a maintainer's label")
        return
    add_merge_label(pr["number"])
    print(f"#{pr['number']} queued for merging")


def pr_write_token():
    """Comments and labels use the workflow token, so the merge PAT needs no issue access."""
    token = os.environ.get("PR_WRITE_TOKEN")
    if not token:
        raise ValueError("PR_WRITE_TOKEN is required to comment on or relabel the pull request")
    return token


def merge_authorized(pr, sha):
    return (pr["state"] == "open" and not pr["draft"]
            and (pr["head"]["repo"] or {}).get("full_name") == repository()
            and pr["head"]["sha"] == sha and pr["head"]["ref"] == os.environ["HEAD_REF"]
            and pr["base"]["ref"] == "main" and pr["head"]["ref"] != "main"
            and any(label["name"] == os.environ["MERGE_LABEL"] for label in pr["labels"]))


def check_labeller():
    permission = api(f"repos/{repository()}/collaborators/{quote(os.environ['LABELLER'], safe='')}/permission")["permission"]
    if permission not in {"admin", "maintain", "write"}:
        raise ValueError("The labeller no longer has write access")


def validate_merge():
    validate_head(os.environ["HEAD_SHA"], os.environ["HEAD_REF"])
    if not os.environ.get("GH_TOKEN") or not os.environ.get("GPG_PRIVATE_KEY"):
        raise ValueError("Merge token and signing key are required")
    if not merge_authorized(pull_request(), os.environ["HEAD_SHA"]):
        raise ValueError("Pull request changed or merge authorization was removed")
    check_labeller()


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
    subprocess.run(["gpg", "--batch", "--import"],
                   input=os.environ["GPG_PRIVATE_KEY"].encode(), check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(["gpg", "--batch", "--list-secret-keys", os.environ["GIT_SIGNING_KEY"].rstrip("!")],
                   check=True, stdout=subprocess.DEVNULL)
    passphrase = home / "passphrase"
    passphrase.write_text(os.environ.get("GPG_PASSPHRASE", ""))
    passphrase.chmod(0o600)
    wrapper = home / "git-gpg"
    wrapper.write_text("#!/usr/bin/env python3\nimport os, sys\n"
                       "os.execvp('gpg', ['gpg', '--batch', '--pinentry-mode', 'loopback', "
                       "'--passphrase-file', os.path.join(os.environ['GNUPGHOME'], 'passphrase'), *sys.argv[1:]])\n")
    wrapper.chmod(0o700)
    for key, value in (("user.name", os.environ["GIT_USER_NAME"]),
                       ("user.email", os.environ["GIT_USER_EMAIL"]),
                       ("user.signingkey", os.environ["GIT_SIGNING_KEY"]),
                       ("gpg.program", str(wrapper)), ("commit.gpgsign", "true"),
                       ("core.hooksPath", "/dev/null")):
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
        git("fetch", "origin", "main")
        base = git("rev-parse", "origin/main")
        Path(os.environ["RUNNER_TEMP"], "merge-base-sha").write_text(base)
        script = Path(__file__).with_name("sign_commit.py")
    else:
        base = git("merge-base", f"origin/{os.environ['BASE_BRANCH']}", "HEAD")
        script = Path(__file__).with_name("rewrite_dependabot_commit.py")
    if git("rev-list", "--merges", f"{base}..HEAD"):
        raise ValueError("Merge commits cannot be automatically replayed")
    subprocess.run(["git", "rebase", "--force-rebase", "--empty=keep", base,
                    "--exec", shlex.join(["python3", str(script)])], check=True)


def push_signed(mode):
    sha = os.environ.get("EXPECTED_SHA") or os.environ["HEAD_SHA"]
    pr = pull_request()
    branch = os.environ["HEAD_REF"]
    validate_head(sha, branch)
    if mode == "merge":
        if not merge_authorized(pr, sha):
            raise ValueError("Pull request changed or merge authorization was removed")
        check_labeller()
    elif not (pr["state"] == "open" and pr["user"]["login"] == "dependabot[bot]"
              and (pr["head"]["repo"] or {}).get("full_name") == repository()
              and pr["head"]["sha"] == sha and pr["head"]["ref"] == branch):
        raise ValueError("Dependabot PR is stale")
    authenticated_git("push", f"--force-with-lease=refs/heads/{branch}:{sha}", "origin", f"HEAD:refs/heads/{branch}")


def authenticated_git(*args):
    subprocess.run(["git", "-c", "credential.helper=", "-c",
                    "credential.helper=!gh auth git-credential", *args], check=True)


REPOSITORY_CONFIG = Path(__file__).resolve().parent.parent / ".github/repository.toml"


def required_checks(config=REPOSITORY_CONFIG):
    """Return the status checks the branch rulesets require before merging."""
    rulesets = tomllib.loads(Path(config).read_text()).get("rulesets", [])
    return {check["context"] for ruleset in rulesets for rule in ruleset.get("rules", [])
            if rule["type"] == "required_status_checks"
            for check in rule["parameters"]["required_status_checks"]}


def checks_ready(runs, statuses, required=None):
    required = required_checks() if required is None else set(required)
    # A rerun supersedes older attempts with the same app and check name.
    latest = {}
    for check in runs:
        key = (check.get("app", {}).get("id"), check["name"])
        if key not in latest or check["id"] > latest[key]["id"]:
            latest[key] = check
    checks = [c for c in latest.values() if c["name"] != "rebase, sign and fast-forward into main"]
    if any(c["status"] == "completed" and c["conclusion"] not in {"success", "neutral", "skipped"} for c in checks):
        raise ValueError("A check failed")
    if statuses.get("statuses") and statuses["state"] in {"failure", "error"}:
        raise ValueError("A commit status failed")
    # Every required check must be reported, as the ruleset demands, and at
    # least one must have succeeded so a fully skipped run cannot merge.
    names = {c["name"]: c for c in checks}
    return (bool(required) and required <= names.keys()
            and any(names[name]["conclusion"] == "success" for name in required)
            and all(c["status"] == "completed" for c in checks)
            and (not statuses.get("statuses") or statuses["state"] == "success"))


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
        pages = api(f"repos/{repository()}/commits/{sha}/check-runs?per_page=100&filter=latest", paginate=True, token=token)
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
        labelled = [event["created_at"] for page in events for event in page
                    if event["event"] == "labeled" and (event.get("label") or {}).get("name") == label]
        if labelled:
            queued.append((max(labelled), issue["number"]))
    order = [entry for _, entry in sorted(queued)]
    return order[:order.index(number)] if number in order else order


def pending_release(directory):
    """Return the components ``multicz plan`` would still bump on the latest main."""
    authenticated_git("-C", str(directory), "fetch", "--quiet", "--tags", "--force", "origin", "main")
    git("-C", str(directory), "checkout", "--quiet", "--detach", "FETCH_HEAD")
    return sorted(json.loads(run("multicz", "plan", "--output", "json", cwd=directory))["bumps"])


def wait_turn(poll=30, limit=5 * 60 * 60):
    """Wait until this pull request heads the queue and main has no pending release."""
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
            if not pending:
                print("First in the merge queue and main has no pending release")
                return
            print(f"Waiting for the release of {', '.join(pending)} on main")
        time.sleep(poll)
    raise TimeoutError("Timed out waiting in the merge queue")


def merge():
    sha = git("rev-parse", "HEAD")
    if not merge_authorized(pull_request(), sha):
        raise ValueError("Pull request changed or merge authorization was removed")
    check_labeller()
    git("fetch", "origin", "main")
    if git("rev-parse", "origin/main") != Path(os.environ["RUNNER_TEMP"], "merge-base-sha").read_text():
        raise ValueError("Main advanced; re-apply the auto-merge label")
    authenticated_git("push", "origin", "HEAD:refs/heads/main")
    api(f"repos/{repository()}/issues/{pr_number()}/comments", "POST",
        {"body": f"Merged into `main` as `{sha}`, GPG-signed with the maintainer key. History stays linear."},
        token=pr_write_token())


JOB_MARKER = "<!-- ci-job: {} -->"
ERROR_LINE = re.compile(r"##\[error\]|^FAILED |^E\s|^--- FAIL|^FAIL\b|\b(?:Error|error|ERROR):")
TIMESTAMP = re.compile(r"^\d{4}-\d\d-\d\dT[\d:.]+Z ")


def error_lines(log, limit=30):
    """The lines of a job log that explain its failure, without timestamps or repeats."""
    lines = []
    for raw in log.splitlines():
        line = TIMESTAMP.sub("", raw).replace("##[error]", "").rstrip()
        if ERROR_LINE.search(TIMESTAMP.sub("", raw)) and line.strip() and line not in lines:
            lines.append(line[:200])
    return lines[-limit:]


def report_jobs():
    """Keep one pull request comment per CI job that failed, updated in place.

    A failing job gets a comment with its failed step, the error lines of its
    log and a link to it; the next run edits that same comment, and marks it
    resolved once the job passes again. Jobs that never failed stay silent.
    """
    number, sha = pr_number(), os.environ["HEAD_SHA"][:7]
    run_jobs = api(f"repos/{repository()}/actions/runs/{os.environ['RUN_ID']}/attempts/"
                   f"{os.environ['RUN_ATTEMPT']}/jobs?per_page=100", paginate=True)
    jobs = [job for page in run_jobs for job in page["jobs"]
            if job["name"] != os.environ["REPORT_JOB"] and job["status"] == "completed"]
    pages = api(f"repos/{repository()}/issues/{number}/comments?per_page=100", paginate=True)
    existing = {comment["body"].splitlines()[0]: comment for page in pages for comment in page
                if comment["user"]["login"] == "github-actions[bot]" and comment["body"].startswith("<!-- ci-job: ")}
    for job in jobs:
        marker = JOB_MARKER.format(job["name"])
        comment = existing.get(marker)
        if job["conclusion"] in {"failure", "timed_out"}:
            steps = [step["name"] for step in job.get("steps", []) if step["conclusion"] == "failure"]
            lines = error_lines(run("gh", "api", f"repos/{repository()}/actions/jobs/{job['id']}/logs"))
            body = (f"{marker}\n**CI job `{job['name']}` failed** on `{sha}`"
                    + (f", in step `{steps[0]}`" if steps else "") + ".\n\n"
                    + ("```\n" + "\n".join(lines) + "\n```\n\n" if lines else "")
                    + f"[Job logs]({job['html_url']})")
        elif job["conclusion"] == "success" and comment and "passes again" not in comment["body"]:
            body = (f"{marker}\n**CI job `{job['name']}` passes again** on `{sha}`; the failure "
                    f"reported here is resolved. [Job logs]({job['html_url']})")
        else:
            continue
        if comment:
            api(f"repos/{repository()}/issues/comments/{comment['id']}", "PATCH", {"body": body})
        else:
            api(f"repos/{repository()}/issues/{number}/comments", "POST", {"body": body})


def summary(title, body=""):
    text = f"## {title}\n\n**Result:** {os.environ.get('JOB_STATUS', 'unknown')}\n"
    if "PR_NUMBER" in os.environ or "PR" in os.environ:
        number = os.environ.get("PR_NUMBER") or os.environ.get("PR") or "not validated"
        text += f"\nPull request: {number}\n"
    if os.environ.get("GIT_USER_NAME") or os.environ.get("GIT_USER_EMAIL"):
        text += (f"\nIdentity: {os.environ.get('GIT_USER_NAME', '')} "
                 f"<{os.environ.get('GIT_USER_EMAIL', '')}>\n")
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
            matches = [str(test) for test in sorted(Path("scripts/tests").glob("test_*.py"))
                       if re.search(rf"^\s*import {re.escape(module)}\b", test.read_text(), re.M)]
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
    summary("Commit signatures", f"{len(commits)} commits checked.\n\n"
            + ("\n".join(f"- `{failure}`" for failure in failures) or "All signatures verified."))
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["validate-dependabot", "label-dependabot", "report-jobs",
                        "validate-merge", "configure-signing",
                        "cleanup", "queue", "rebase", "push", "wait", "merge", "summary", "changed-components",
                        "script-checks", "verify-signatures"])
    parser.add_argument("--mode", choices=["merge", "dependabot"], default="dependabot")
    parser.add_argument("--signed", action="store_true")
    parser.add_argument("--title", default="GitHub automation")
    parser.add_argument("--body", default="", help="Additional job summary text")
    parser.add_argument("--since", help="Git reference for multicz component comparison")
    parser.add_argument("--ci", action="store_true", help="Compare the current CI event; validate all when no safe base exists")
    args = parser.parse_args()
    actions = {"validate-dependabot": validate_dependabot, "label-dependabot": label_dependabot,
               "report-jobs": report_jobs, "validate-merge": validate_merge,
               "configure-signing": configure_signing, "cleanup": cleanup,
               "rebase": lambda: rebase(args.mode), "push": lambda: push_signed(args.mode),
               "wait": lambda: wait_checks(args.signed), "merge": merge, "queue": wait_turn,
               "summary": lambda: summary(args.title, args.body),
               "changed-components": lambda: changed_components(args.since, args.ci),
               "script-checks": lambda: script_checks(args.since),
               "verify-signatures": verify_signatures}
    try:
        actions[args.command]()
    except (ValueError, TimeoutError, subprocess.CalledProcessError) as error:
        if args.mode == "merge" and args.command in {"validate-merge", "queue", "wait", "rebase", "push", "merge"}:
            # Retrying is explicit: fix the problem, then re-apply the label.
            # No PR code runs here, including when a rebase fails.
            try:
                api(f"repos/{repository()}/issues/{pr_number()}/comments", "POST",
                    {"body": f"auto-merge-signed: {error}. Fix the problem and re-apply the `{os.environ['MERGE_LABEL']}` label."},
                    token=pr_write_token())
                api(f"repos/{repository()}/issues/{pr_number()}/labels/{quote(os.environ['MERGE_LABEL'], safe='')}",
                    "DELETE", token=pr_write_token())
            except subprocess.CalledProcessError:
                pass
        raise


if __name__ == "__main__":
    main()
