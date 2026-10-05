# GitHub automation

Repository configuration is declared in `.github/repository.toml`. It covers
the labels used by issue templates and Dependabot, signing identity variables,
required signing secrets, workflow token permissions and the `main` ruleset.

## Check the repository

Authenticate `gh` with an account that can read repository settings and Actions
secrets, then run:

```console
python3 scripts/init_github.py check --repo goabonga/cloud
```

`check` performs reads only. It exits with status 1 when managed configuration
is missing or differs. GitHub exposes secret names, not secret values: presence
checks cannot prove that a token has the right scopes or that a private key
matches the configured signing identity.

## Tokens by responsibility

Create separate fine-grained PATs for the owner `goabonga`, restricted to the
`cloud` repository. Save them as **Actions repository secrets**, not Dependabot
secrets: these workflows execute in a trusted Actions context.

| Secret | Responsibility | Repository permissions |
| --- | --- | --- |
| `DEPENDABOT_PAT` | Push rewritten dependency commits, read the current PR, open and close its tracking issue as the maintainer | Contents: write; Pull requests: read; Workflows: write; Issues: write |
| `AUTO_MERGE_PAT` | Check authorization, push signed commits, fast-forward `main` and `develop`, sync `develop` with `main`, open release promotions, re-sign pull request branches, label Dependabot pull requests, post pull request comments and scanner review threads as the maintainer, add requested exceptions, open exception reviews | Contents: write; Pull requests: write; Workflows: write; Issues: write |
| `RELEASE_PAT` | Push signed documentation version commits and tags; create GitHub releases | Contents: write |

PAT setup instructions are declared under `[tokens]` in
`.github/repository.toml`. Both `check` and `init` display the instructions for
missing PATs: creation URL, resource owner, selected repository, permission
levels and the token owner's required repository role. These declarations are
guidance; the script cannot inspect permissions inside an existing secret.

Metadata read access is implicit. The merge and release tokens must belong to the maintainer
whose repository role is allowed by the ruleset's administrator bypass. They do
not need permission to change policies. Tracking issues are written with the
dependency token, so they appear under the maintainer account; PR linking and
title edits use the workflow's own `GITHUB_TOKEN`, with explicitly declared permissions.
Besides those issues, the dependency token is used only for the signed push and its PR validation.
The merge workflow reads check runs and commit statuses using its own
`GITHUB_TOKEN`, with `checks: read` and `statuses: read`. Its dedicated PAT handles
PR authorization, Git pushes, and the comments and label removals it writes on the
pull request, so they appear under the maintainer account rather than the Actions
bot. Select only the PAT permissions in the table; the workflow token supplies the
CI read permissions.
See GitHub's [PAT permission reference](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens),
[check-run permissions](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference)
and [collaborator permission checks](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user).

For local initialization, use a separate administrative `gh` login. `check` needs
Administration, Secrets, Variables and Issues read access; `init` needs
write access for those settings. No automation PAT needs Secrets or Variables
access. GitHub's [PAT setup guide](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
explains creation, repository selection and expiration.

## Initialize or repair configuration

Review `.github/repository.toml`, then apply it:

```console
python3 scripts/init_github.py init --repo goabonga/cloud
```

To apply only the branch protection, which needs no secrets and can run on
a fresh repository, use `protect`. It reconciles the `main-protection` ruleset —
covering `main` and `develop` — and leaves settings, labels, variables and
secrets untouched:

```console
python3 scripts/init_github.py protect --repo goabonga/cloud
```

Existing secrets are preserved unless their names are explicitly supplied as
environment variables. Values are passed to `gh secret set` through standard
input; they are never stored in the configuration or printed. Initialization
requires write access to repository administration, Actions secrets and variables.

`DEPENDABOT_PAT`, `AUTO_MERGE_PAT` and `RELEASE_PAT` must be configured before
enabling their workflows on `main`. Each workflow uses only its dedicated token. Supply each value through its matching environment variable when running
`init`, or store it directly with `gh secret set NAME --repo goabonga/cloud`.
Creating a PAT is a separate GitHub account operation; the script stores supplied
values but cannot generate tokens or recover existing secret values.

`init` always applies the `main-protection` ruleset first, then every other managed
item independently: a failed write, a section it cannot read or a missing secret is
recorded and the run goes on. It ends with a single report — `ERROR:` for what it
could not apply, `FAIL:` for what still differs, the `gh secret set` commands and
PAT permissions for missing secrets — and exits with status 1 if anything is left.
`check` reads each section on its own too, so one unreadable section never hides
the others. Create each dedicated PAT with the permissions listed above, then paste its value into
the GitHub CLI's interactive prompt:

```console
gh secret set DEPENDABOT_PAT --repo goabonga/cloud
gh secret set AUTO_MERGE_PAT --repo goabonga/cloud
python3 scripts/init_github.py init --repo goabonga/cloud
python3 scripts/init_github.py check --repo goabonga/cloud
```

See the [GitHub CLI secret command](https://cli.github.com/manual/gh_secret_set).
The initializer preserves existing secrets; it cannot retrieve their values or
automatically create personal access tokens.

The command updates existing managed labels, variables and rulesets rather than
creating duplicates. It leaves unrelated labels, variables, secrets and rulesets
alone. A failed run can be retried: it reads the actual remote configuration again
and applies the remaining differences. The named managed ruleset is authoritative;
review its complete definition before applying changes to an existing repository.

The administrator bypass is retained from the source so the signed merge and
release workflows can fast-forward `main`. The merge workflow verifies the
maintainer's authorization, the current PR head and successful CI before and
after signing. The release workflow runs after successful CI validation
and refuses to release an unvalidated newer commit.

## Dependabot updates

Dependabot opens grouped pull requests every Monday, one per ecosystem and, for `uv`,
one for runtime and one for development dependencies:

| Ecosystem | Directory | Rewritten subject |
| --- | --- | --- |
| `github-actions` | `/` | `ci: …`, which patches every component owning the workflows |
| `gomod` | `/` | `fix(deps): …`; modules are linked into the shipped binaries |
| `uv` | `/scripts` | `fix(deps): …` for runtime dependencies, which release `cloud-scripts`; `chore(deps): …` when only development dependencies change |

The rewrite workflow turns each grouped Dependabot commit into one signed commit
per updated dependency, without a body, such as
``ci: bump `actions/setup-go` from 6.5.0 to 7.0.0``. Each changed line is tied to
the dependency it names or, in `uv.lock`, to the `[[package]]` block it belongs to.
When a line matches no announced dependency or several of them, for example an
indirect Go module, the update stays a single commit, also without a body. The
final tree is always the one Dependabot produced. The workflow refuses a commit
whose files span ecosystems or fall outside them, leaving the pull request
unchanged.

### Automatic merge of Dependabot updates

`dependabot-auto-merge` runs trusted code from `main` whenever `ci` succeeds on a
Dependabot pull request. When CI passed on the current head and every commit is
rewritten and signed, it applies the `auto-merge` label with `AUTO_MERGE_PAT`, so
the pull request joins the [merge queue](#signed-automatic-merge). A label added
with the workflow token would not start `auto-merge-signed`, because GitHub does not
chain workflows triggered by that token.

Major updates, recognized by a change of the leading version number in the
``Updates `x` from A to B`` lines of the Dependabot pull request description, are
not labelled. The pull request gets a
single comment explaining that it waits for review, and it and its tracking issue are
assigned to the maintainer: a maintainer with write access merges it by adding the
`auto-merge` label, which queues it like any other pull request.

## Dependabot tracking issues

The rewrite workflow drops the Conventional Commit type and scope from the
Dependabot pull request title, which only the rewritten commits carry, and again
whenever Dependabot restores it. It creates one tracking issue per PR, titled like
the pull request and labelled like it
(`dependencies` plus `go`, `github-actions` or `python`). Its body follows the
`Dependency update` issue form (`.github/ISSUE_TEMPLATE/dependency_update.yml`) as
GitHub renders a submitted form: the ecosystem, one line per update with its versions,
Dependabot's release notes and commits without its command help, and the pull
request. Its durable identity
includes the repository and PR number and is stored in the issue body. It scans
all pages of open and closed issues before creating anything, including legacy
tracking issues from the source. Changing the PR title or replacing its description
does not create another issue.

The pull request description is rebuilt once from the pull request template,
without its contributor checklist: Dependabot's whole description, command help
included, under `Description`, the tracking link under `Related issues`, and the
`Pipeline` boxes CI ticks. Later runs
only repair the link, so ticked boxes stay; when Dependabot rewrites its description,
the next run rebuilds it.

Issue creation and PR linking happen before pushing signed commits. If a response
is lost or linking fails, rerunning the workflow reuses the persisted issue and
repairs the link. `Closes #…` in the PR description closes the issue on merge;
the script also repairs closure if merging races with link repair. Existing
duplicates are reported and the oldest issue is reused; they are not deleted.

## Changed components in CI

Each validation job runs only for what the change touches. Jobs are:

| Job | Runs when | Checks |
| --- | --- | --- |
| `audit` | always | zizmor workflow security audit, gitleaks secret scan of the commits the pull request or push brings, Plumber compliance |
| `check commit signatures` | every pull request and push, after `audit` | each PR or pushed commit carries a signature verified by GitHub; a failure stops the pipeline |
| `detect changed components` | always, after `audit` and, on pull requests and pushes, after a successful signature check | multicz configuration, changed components |
| `check license headers` | at least one component changed, after detection | SPDX headers |
| `validate scripts` | `cloud-scripts` changed | byte-compilation and pytest on the tests the change affects, ruff formatting, bandit and osv-scanner on `scripts/uv.lock` |
| `validate go` | a registered Go component changed, through its own paths or its imports | formatting and vet, race tests, build and gosec, each step for every changed component; `go mod verify`, govulncheck, golangci-lint and osv-scanner on `go.mod` |
| `validate documentation` | `cloud-docs` changed (`docs/**`, `zensical.toml`, `assets/cloud.svg`) | generated branding, documentation build |

Detection runs `multicz changed` and nothing else: a component changed when it has
commits since its latest tag, exactly as in the reference pipeline. A commit that changes
no component, such as a `chore`, therefore runs no validation, no release and no
documentation publication. Only `audit` and detection itself always run; on a pull
request or a push, an unsigned commit stops the pipeline before detection, so nothing is
validated or released.
Scripts and their tests are selected from the diff against the PR base or the previous
push, which only narrows which test files run.

Every job selects its work from the `changed` output of `multicz changed`, which is the
only source of component changes. Its inputs are:

- each component's `paths` globs;
- the `go-deps` plugin, which adds the files a Go component imports (its command
  packages are registered under `plugins.go-deps.packages`). A change in
  `internal/transport` therefore selects `cloud` and `cloud-svc`. With `strict = true`,
  a package `go list` cannot load (a corrupted `go.sum`, a build error) fails
  `multicz validate` in `detect changed components` instead of selecting nothing;
- `overlap_policy = "all"` for shared module or workflow changes.

`cloud-docs` declares `depends_on` on `cloud`, `cloud-svc` and `cloud-scripts`: a change
to any of them also selects the documentation, and its release patches `cloud-docs`, so
the published version table is rebuilt with the new version.

Script validation compiles every script and runs the test files that import a changed
module. Changes to `scripts/ci_automation.py`, `scripts/pyproject.toml`,
`scripts/uv.lock` or `pytest.ini` run the whole suite, as does any script without a
test that imports it.

Go validation is a single job rather than a matrix. Matrix job names depend on the
components that changed, so they cannot be required checks.

### Security tools

Each security tool, and the pytest, go test and ruff checks, runs through
`scripts/ci_automation.py security <tool> -- <command>`
from its own `make` target, with a pinned version. The wrapper keeps the tool's exit
status, adds a section to the job summary (result, conclusion and the end of its
output) and records one `security-report:` line in the job log for the pull request
report. osv-scanner reports every vulnerable dependency a lockfile declares; for Go its
own call analysis is off, since govulncheck already tells which vulnerabilities the code
reaches. Accepted exceptions are documented next to their configuration:
`scripts/pyproject.toml` for bandit (subprocess calls with argument lists, never a
shell), `# zizmor: ignore[...]` comments and `.github/zizmor.yml` for the trusted
`pull_request_target` / `workflow_run` workflows and the rewrite's loop guard.

### Reports on pull requests

On a pull request, the `report job failures` job runs last, whatever the other jobs
concluded, and keeps one comment per check. Each security tool a job ran gets its
own comment, marked `<!-- ci-check: <job> / <tool> -->`: ✅ or 🟥, the tool's verdict
and, on failure, what it found, with a link to the job log. A job gets a comment of
its own, marked `<!-- ci-job: <name> -->`, when it runs no security tool or fails
outside one (a test, a build), with the failed step and the error lines of its log;
once that job passes again, its comment says so instead of staying behind. Later
runs edit the same comments, only when their content changes; skipped and cancelled
jobs are left alone. It runs trusted code from `main`, reads job logs with the
workflow token (`actions: read`) and posts as the maintainer with `AUTO_MERGE_PAT`,
falling back to the workflow token when the PAT is unavailable, as on fork pull
requests. It is not a required check.

The same job ticks the `Pipeline` boxes of the pull request template. Each box
carries a `<!-- ci-box: <job> -->` or `<!-- ci-box: <job> / <tool> -->` marker and,
after each run on the pull request head, is ticked with `passed` or `not needed` (a
job detection skipped) or left empty with `failed` or `not run` (a tool after an
earlier failing step, a job after a failed detection), followed by the commit. A
description without markers, such as Dependabot's, is left untouched, and so is a box
whose job did not complete in that run.

### Required checks

The branch ruleset requires the seven job names above. A job skipped because its
component did not change counts as passing. A failed or cancelled job blocks the
merge, and a skipped job is still allowed only if its `if` condition was false.

The `release-bump` job runs only when some component changed and no validation job failed
or was cancelled (`!failure() && !cancelled()`), so a failing check stops the release.

## Signed automatic merge

A maintainer with write access merges a pull request into `main`, or into
`develop` once it exists, by applying the `auto-merge` label. The
`auto-merge-signed` workflow then runs only trusted code from `main`:

1. It checks the label, the pull request head and the labeller's access.
2. It waits for its turn in the merge queue. Pull requests are served in the order
   of their latest `auto-merge` label, one at a time, and only once `multicz plan`
   on `main` is empty, so a release left by the previous merge is published first.
   A merge that bumps nothing lets the next pull request start right away.
3. It waits for the pull request checks, rebases onto `main`, signs every commit
   with the maintainer key and pushes the branch with an explicit lease.
4. It waits for the checks of the signed commits, then fast-forwards `main`.

Any failure comments on the pull request and removes the label, which also takes
the pull request out of the queue; re-apply the label to retry. Removing the label
by hand skips a pull request that is blocking the queue.

A merge into `develop` also waits until `develop` contains `main`, so the
`develop` sync after a release always comes first. Every merge shares one queue.

## Promotion of develop to main

Work lands on `develop`; releases happen on `main`. Opening a **Release promotion**
issue (`.github/ISSUE_TEMPLATE/release_promotion.yml`, label `release`), or running
the `promotion` workflow by hand, proposes `develop` for release:

1. Trusted code from `main` checks that the issue author has write access; others
   get a comment and the issue is closed. A new promotion issue supersedes the open
   promotion: its pull request and every other open promotion issue are closed with
   a comment.
2. On a checkout of `develop`, `multicz plan` gives the components that would be
   released with their current and next versions, and `git log main..develop` the
   commits. Both are added to the issue, below what its author wrote. When
   `develop` has nothing `main` lacks, the issue is closed instead.
3. The promotion pull request, `develop` into `main`, is opened from the pull request
   template, without its contributor checklist, with the same content and
   `Closes #<issue>`, under the maintainer account so CI runs on it. Like the issue,
   it is labelled `release` and assigned to whoever proposed the release: the issue
   author, or who ran the workflow. A manual run creates the issue too.
4. Every push to `develop` refreshes the issue and the generated sections of an open
   promotion; the pipeline boxes CI ticks stay.

The workflow never labels the promotion. A maintainer reviews it and applies
`auto-merge`: the queue then checks that `main` is an ancestor of the validated head
and that every commit in between is verified, and fast-forwards `main` to it
without rewriting a commit. The release jobs on `main` bump the components, and
`sync develop with main`, the last CI job of every push to `main`, moves `develop`
forward to the release commit. When `main` received commits of its own (a fix
merged straight into `main`), `develop`'s commits are replayed on `main`, re-signed
with the maintainer key, and `develop` is pushed with a lease. Without a `develop`
branch, the sync does nothing. Create it from `main`, for example with
`git push origin main:develop`.

The `promotion` workflow uses the `issues` event, which the repository's Actions
event policy must allow.

## Security scanner exceptions

zizmor, gitleaks, bandit, osv-scanner, govulncheck, golangci-lint and gosec run
through `scripts/ci_automation.py security`, which reads each scanner's JSON report,
normalizes its findings (tool, rule, path, line) and compares them with
`.github/exceptions.toml`. Each exception accepts one rule of one tool, optionally
for paths matching a glob, with a reason, an `added` date and an `expires` date at
most 90 days later; osv-scanner rules also match a vulnerability's aliases. A finding
no exception accepts fails the check. An expired exception still applies but is
reported as a warning, so a lapsed review never blocks unrelated work.

On a pull request, each finding that blocks gets its own review thread: on its line
when the diff has it, on its file otherwise. A finding in a file the pull request
does not change has no thread; the check's report lists it. Replying
`/exception <days>d <reason>` to a finding's comment, with write access, starts
`exception-signal`, which has no secrets, then `exception`, which runs trusted code
from `main`: it adds the exception for at most 90 days in a signed commit on the
pull request branch, answers in the thread and resolves it; the checks then run
again. A finding
that disappears gets a reply saying so.

Every Monday, `exceptions-review` opens a `security-exception` issue and a pull
request, both assigned to the maintainer, removing each exception that expires within
seven days, from the registry
of `develop` when that branch exists and of `main` otherwise. That pull request
reports the finding again: fix it there, or renew the exception with `/exception`.
`/exception` answers findings on pull requests into `main` or `develop`, never on
the promotion, whose head is `develop` itself.

## Re-signing pull request branches

GitHub's "Update branch" rebase, like a commit made in the web editor, leaves
unsigned commits, which `check commit signatures` then rejects. Any push to a pull
request branch other than Dependabot's starts `resign-signal`, which has no secrets
and runs no pull request code. `resign` then runs trusted code from `main`:

1. It stops unless the pull request is open, comes from this repository, targets
   `main` or `develop`, is still at the pushed commit, and its branch is neither of
   them.
2. It stops when every commit is already signed. Its own signed push therefore
   starts it again with nothing left to do.
3. It checks that the pusher has write access, the trust the `auto-merge` label
   also asks for.
4. It re-signs every commit in place, on the same base and in the same order, as
   the maintainer, pushes with an explicit lease and comments on the pull request.

Dependabot's own pushes go through the Dependabot rewrite instead. On a Dependabot
pull request, `@dependabot rebase` also avoids unsigned commits: Dependabot rebases
and the rewrite re-signs.

## Component releases and documentation publication

Releases are driven entirely by multicz. Nothing in the repository reads version
files or computes tags.

1. `release-bump` runs only on `main`, after every validation job succeeded or was
   skipped, and never on a `chore(release):` commit. It runs `multicz plan`. If the
   plan is empty, the job stops: no commit, no tag, no release. Otherwise it runs
   `multicz bump --commit --tag --sign --push`, which writes the version files and
   changelogs, creates one signed tag per bumped component, and pushes them. The bump
   commit message comes from `release_commit_message` in `multicz.toml`: the subject
   `chore(release): bump changed components`, the list of bumps in the body, and a
   `[skip ci]` footer, so pushing it starts no new pipeline.
2. `release` runs one job per bumped tag. It checks out the tag, builds the assets
   with steps in the job, and creates the GitHub release with
   `multicz release-notes --tag` as notes.
3. `pages` builds and deploys the documentation from the bump commit, which contains the
   refreshed version table.

Assets are linux `amd64` and `arm64` binaries for `cloud` and `cloud-svc`, a
`tar.gz` of the built site for `cloud-docs`, and a `-checksums.txt` file with SHA-256
digests. `cloud-scripts` ships no asset.

A component is first released by its first `feat` or `fix` commit, as `0.1.0` or
`0.0.1` respectively. A `chore` commit never bumps a component.

The release PAT pushes the bump commit and creates the releases. The Pages job uses the
workflow's `GITHUB_TOKEN` with `pages: write` and `id-token: write`, independently of the
release PAT. Configure the repository's Pages publishing source as **GitHub Actions**,
with the `github-pages` environment permitting `main`. See GitHub's
[custom Pages workflow requirements](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

With no planned bump, no untagged new component and no pending recovery, no new commit, tag, release or
deployment is created.
The signed release commit's follow-up CI validates it but skips the release job
so it cannot start a release loop. Runs on `main` are not cancelled by newer pushes.

If release creation fails after the atomic push, rerun the workflow: it reuses
the existing signed tag and commit and repairs a missing or draft GitHub release.
An already persisted release is reused even if its creation response was lost.
If Pages fails, rerun the failed job. A manual **Run workflow** on `main` can also
republish the current tagged documentation version without bumping it. Manual
runs on other branches cannot publish. A run whose validated commit has been
superseded leaves publishing to a workflow validating the newer `main`.

## Test the scripts

Every workflow writes its job summary through `ci_automation.py summary` in an
`always()` step. The signing workflows and Dependabot signal use the script from
the trusted default-branch checkout. Summaries append to existing output and
include the result, PR and signing identity when that context is available.

```console
uv tool install 'multicz>=1.8' --with 'multicz-go-deps-plugin>=0.4'
make check
# Run only the script checks:
make scripts-check
```

Tests use pytest functions and fixtures. The suite uses disposable repositories
and test GPG keys for signing,
fake GitHub clients for mutations and real SVG conversion for favicon validation.
It never changes repository settings, creates GitHub issues or merges a PR.
